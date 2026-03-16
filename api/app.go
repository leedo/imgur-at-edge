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
	"github.com/fastly/compute-sdk-go/kvstore"
	"github.com/gorilla/mux"
)

const jsonType = "application/json"

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

		if err := a.checkContentLength(r.Header.Get("Content-Length")); err != nil {
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

		if _, err := a.KVStore.Lookup(v.Hash); err == nil {
			io.Copy(io.Discard, v)
			sendPutOK(w, r, v)
			return
		}

		if err := a.KVStore.Insert(v.Hash, v); err != nil {
			internalError(w, "error inserting: "+err.Error())
			return
		}

		sendPutOK(w, r, v)
		return
	}
}

func sendPutOK(w http.ResponseWriter, r *http.Request, v *media.ValidMediaReader) {
	w.Header().Add("Content-Type", jsonType)

	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Add("Access-Control-Allow-Origin", origin)
	}

	w.WriteHeader(http.StatusOK)

	const js = `{"status": "ok", "data": {"id": "%s", "link": "https://%s/%s"}}`
	fmt.Fprintf(w, js, v.Hash, r.URL.Host, v.Filename())
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

		w.Header().Set("X-Cache", "HIT")

		res, err := simple.GetOrSet([]byte(hash), func() (simple.CacheEntry, error) {
			res, err := a.KVStore.Lookup(string(hash))
			if err != nil {
				return simple.CacheEntry{}, err
			}

			w.Header().Set("X-Cache", "MISS")

			return simple.CacheEntry{
				Body: res,
				TTL:  24 * time.Hour,
			}, nil
		})

		if err != nil {
			if err == kvstore.ErrKeyNotFound {
				notFoundError(w)
				return
			}
			internalError(w, err.Error())
			return
		}

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

func (a *App) checkContentLength(cLen string) error {
	if cLen == "" {
		return ErrMissingContentLength
	}

	u, err := strconv.ParseUint(cLen, 10, 64)
	if err != nil {
		return ErrInvalidContentLength
	}

	if u > a.MaxLength {
		return ErrContentLengthTooLarge
	}

	return nil
}

func (a *App) Router() *mux.Router {
	r := mux.NewRouter()

	r.Path("/").
		Methods("PUT").
		HandlerFunc(a.putHandler())

	r.Path(`/{hash:[a-zA-Z0-9]{32,40}}.{ext:(?:` + strings.Join(media.GetExtensions(), "|") + `)}`).
		Methods("GET").
		HandlerFunc(a.getHandler())

	r.Path("/").
		Methods("OPTIONS").
		HandlerFunc(a.optionsHandler())

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notFoundError(w)
	})

	return r
}
