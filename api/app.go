package api

import (
	"errors"
	"fmt"
	"imgur-at-edge/media"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fastly/compute-sdk-go/cache/simple"
	"github.com/fastly/compute-sdk-go/fsthttp"
	"github.com/fastly/compute-sdk-go/kvstore"
	"github.com/gorilla/mux"
)

const (
	jsonType  = "application/json"
	cacheHit  = "HIT"
	cacheMiss = "MISS"
)

var (
	ErrMissingContentLength  = errors.New("missing content-length")
	ErrInvalidContentLength  = errors.New("invalid content-length")
	ErrContentLengthTooLarge = errors.New("content-length too large")
	ErrUnknownContentType    = errors.New("unknown content-type")
)

type App struct {
	MaxLength         uint64
	ValidateBufLength int
	KVStore           *kvstore.Store
}

func (a *App) putHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ext, ok := media.GetExtension(r.Header.Get("Content-Type"))
		if !ok {
			badRequest(w, ErrUnknownContentType.Error())
			return
		}

		length, err := a.checkContentLength(r.Header.Get("Content-Length"))
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		v, err := media.NewValidMediaReader(r.Body, ext, a.ValidateBufLength)
		if err != nil {
			internalError(w, err.Error())
			return
		}

		defer v.Close()

		if err := v.Validate(); err != nil {
			var validatorErr media.ValidatorError
			if errors.As(err, &validatorErr) {
				badRequest(w, validatorErr.Error())
				return
			}
			internalError(w, "error validating: "+err.Error())
			return
		}

		key, err := encodeKey(v.Hash, ext, length)
		if err != nil {
			internalError(w, err.Error())
		}

		if _, err := a.KVStore.Lookup(key); err == nil {
			io.Copy(io.Discard, v)
			sendPutOK(w, r, key, ext)
			return
		}

		if err := a.KVStore.Insert(key, v); err != nil {
			internalError(w, "error inserting: "+err.Error())
			return
		}

		sendPutOK(w, r, key, ext)
		return
	}
}

func sendPutOK(w http.ResponseWriter, r *http.Request, key string, ext string) {
	w.Header().Add("Content-Type", jsonType)

	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Add("Access-Control-Allow-Origin", origin)
	}

	w.WriteHeader(http.StatusOK)

	const js = `{"status": "ok", "data": {"id": "%s", "link": "https://%s/v2/%s.%s"}}`
	fmt.Fprintf(w, js, key, r.URL.Host, key, ext)
}

func (a *App) getHandlerV2() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		key := vars["key"]
		ext := vars["ext"]

		k, err := decodeKey(key)
		if err != nil {
			internalError(w, "unable to decode key: "+err.Error())
			return
		}

		if checkIfNoneMatch(r.Header.Get("If-None-Match"), k.Hash) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		res, hit, err := a.getOrSet(key)
		if err != nil {
			if err == kvstore.ErrKeyNotFound {
				notFoundError(w)
				return
			}
			internalError(w, err.Error())
			return
		}

		pbext := k.Extension.String()
		if pbext != ext {
			badRequest(w, fmt.Sprintf("URL extension (%s) does not match content (%s)", ext, pbext))
			return
		}

		mime, ok := media.GetMimeType(pbext)
		if !ok {
			badRequest(w, "unknown extension "+k.Extension.String())
			return
		}

		if fw := fsthttp.ResponseWriterFromContext(r.Context()); fw != nil {
			fw.SetManualFramingMode(true)
		}

		w.Header().Set("X-Cache", hit)
		w.Header().Add("Content-Type", mime)
		w.Header().Set("Content-Length", strconv.Itoa(int(k.GetSize())))
		w.Header().Set("Etag", `"`+strconv.FormatUint(*k.Hash, 16)+`"`)
		w.WriteHeader(http.StatusOK)
		io.Copy(w, res)
		return

	}
}

func (a *App) getHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hash := vars["hash"]
		ext := vars["ext"]

		mime, ok := media.GetMimeType(ext)
		if !ok {
			badRequest(w, "unknown extension "+ext)
			return
		}

		res, hit, err := a.getOrSet(hash)
		if err != nil {
			if err == kvstore.ErrKeyNotFound {
				notFoundError(w)
				return
			}
			internalError(w, err.Error())
			return
		}

		w.Header().Set("X-Cache", hit)
		w.Header().Add("Content-Type", mime)
		w.WriteHeader(http.StatusOK)
		io.Copy(w, res)
		return
	}
}

func (a *App) optionsHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		headers := r.Header.Get("Access-Control-Request-Headers")
		methods := r.Header.Get("Access-Control-Request-Method")

		if origin != "" && headers != "" && methods != "" {
			w.Header().Add("Access-Control-Allow-Origin", origin)
			w.Header().Add("Access-Control-Allow-Methods", "GET,HEAD,PUT,OPTIONS")
			w.Header().Add("Access-Control-Allow-Headers", headers)
			w.Header().Add("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
}

func (a *App) checkContentLength(cLen string) (uint32, error) {
	if cLen == "" {
		return 0, ErrMissingContentLength
	}

	u, err := strconv.ParseUint(cLen, 10, 32)
	if err != nil {
		return 0, ErrInvalidContentLength
	}

	if u > a.MaxLength {
		return 0, ErrContentLengthTooLarge
	}

	return uint32(u), nil
}

func (a *App) getOrSet(key string) (io.Reader, string, error) {
	hit := cacheHit
	res, err := simple.GetOrSet([]byte(key), func() (simple.CacheEntry, error) {
		res, err := a.KVStore.Lookup(key)
		if err != nil {
			return simple.CacheEntry{}, err
		}

		hit = cacheMiss

		return simple.CacheEntry{
			Body: res,
			TTL:  24 * time.Hour,
		}, nil
	})

	return res, hit, err
}

func checkIfNoneMatch(inm string, hash *uint64) bool {
	if inm == "" || hash == nil {
		return false
	}

	if string(inm[0]) != "\"" || string(inm[len(inm)-1:]) != `"` {
		return false
	}

	i, err := strconv.ParseUint(inm[1:len(inm)-1], 16, 64)
	if err != nil {
		return false
	}

	return i == *hash
}

func (a *App) Router() *mux.Router {
	r := mux.NewRouter()

	r.Path("/").
		Methods("PUT").
		HandlerFunc(a.putHandler())

	r.Path(`/{hash:[a-zA-Z0-9]{32,40}}.{ext:(?:` + strings.Join(media.GetExtensions(), "|") + `)}`).
		Methods("GET").
		HandlerFunc(a.getHandler())

	r.Path(`/v2/{key:[a-zA-Z0-9]{32,40}}.{ext:(?:` + strings.Join(media.GetExtensions(), "|") + `)}`).
		Methods("GET").
		HandlerFunc(a.getHandlerV2())

	r.Path("/").
		Methods("OPTIONS").
		HandlerFunc(a.optionsHandler())

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notFoundError(w)
	})

	return r
}
