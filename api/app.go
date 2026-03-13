package api

import (
	"errors"
	"fmt"
	"imgur-at-edge/media"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/fastly/compute-sdk-go/cache/simple"
	"github.com/fastly/compute-sdk-go/fsthttp"
	"github.com/fastly/compute-sdk-go/kvstore"
)

const jsonType = "application/json"

type App struct {
	MaxLength   uint64
	KVStoreName string
}

func (a *App) putHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ext, ok := media.GetExtension(r.Header.Get("Content-Type"))
		if !ok {
			badRequest(w, "unknown content-type")
			return
		}

		if err := a.checkContentLength(r.Header.Get("Content-Length")); err != nil {
			badRequest(w, err.Error())
			return
		}

		v, err := media.NewValidMediaReader(r.Body, ext)
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

		s, err := kvstore.Open(a.KVStoreName)
		if err != nil {
			internalError(w, "error opening: "+err.Error())
			return
		}

		if err := s.Insert(v.Hash, v); err != nil {
			internalError(w, "error inserting: "+err.Error())
			return
		}

		w.Header().Add("Content-Type", jsonType)

		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Add("Access-Control-Allow-Origin", origin)
		}

		w.WriteHeader(fsthttp.StatusOK)
		const js = `{"status": "ok", "data": {"id": "%s", "link": "https://%s/%s.%s"}}`
		fmt.Fprintf(w, js, v.Hash, r.URL.Host, v.Hash, ext)
		return
	}
}

func (a *App) getHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		file := r.URL.Path[1:]
		id := file[:len(file)-4]
		ext := file[len(file)-3:]
		mime, ok := media.GetMimeType(ext)
		if !ok {
			badRequest(w, "unknown extension "+ext)
			return
		}

		w.Header().Set("X-Cache", "HIT")

		res, err := simple.GetOrSet([]byte(id), func() (simple.CacheEntry, error) {
			s, err := kvstore.Open(a.KVStoreName)
			if err != nil {
				return simple.CacheEntry{}, err
			}

			res, err := s.Lookup(string(id))
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
		return errors.New("missing content-length")
	}

	u, err := strconv.ParseUint(cLen, 10, 64)
	if err != nil {
		return errors.New("invalid content-length")
	}

	if u > a.MaxLength {
		return errors.New("content-length too large")
	}

	return nil
}

func (a *App) NewServeMux() *http.ServeMux {

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /{$}", a.putHandler())
	mux.HandleFunc("GET /{hash}.{ext}", a.getHandler())
	mux.HandleFunc("OPTIONS /{$}", a.optionsHandler())

	return mux
}
