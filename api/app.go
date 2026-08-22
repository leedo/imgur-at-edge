package api

import (
	"context"
	"errors"
	"fmt"
	"imgur-at-edge/media"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fastly/compute-sdk-go/cache/simple"
	"github.com/fastly/compute-sdk-go/fsthttp"
	"github.com/fastly/compute-sdk-go/kvstore"
)

const (
	jsonType  = "application/json"
	cacheHit  = "HIT"
	cacheMiss = "MISS"
)

type App struct {
	MaxLength         uint64
	ValidateBufLength int
	KVStore           *kvstore.Store
	TTL               time.Duration
}

func (a *App) putHandler(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
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

func sendPutOK(w fsthttp.ResponseWriter, r *fsthttp.Request, key string, ext string) {
	w.Header().Add("Content-Type", jsonType)

	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Add("Access-Control-Allow-Origin", origin)
	}

	w.WriteHeader(fsthttp.StatusOK)

	const js = `{"status": "ok", "data": {"id": "%s", "link": "https://%s/img/v2/%s.%s"}}`
	fmt.Fprintf(w, js, key, r.URL.Host, key, ext)
}

func (a *App) getHandlerV2(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
	key, ext, err := parseGetURL(r.URL.Path)
	if err != nil {
		notFoundError(w)
	}

	k, err := decodeKey(key)
	if err != nil {
		internalError(w, "unable to decode key: "+err.Error())
		return
	}

	if checkIfNoneMatch(r.Header.Get("If-None-Match"), k.Hash) {
		w.WriteHeader(fsthttp.StatusNotModified)
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

	res, hit, err := a.getOrSet(key)
	if err != nil {
		if err == kvstore.ErrKeyNotFound {
			notFoundError(w)
			return
		}
		internalError(w, err.Error())
		return
	}

	w.SetManualFramingMode(true)

	w.Header().Set("X-Cache", hit)
	w.Header().Add("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(int(k.GetSize())))
	w.Header().Set("Etag", `"`+strconv.FormatUint(*k.Hash, 16)+`"`)
	w.WriteHeader(fsthttp.StatusOK)
	io.Copy(w, res)
	return
}

var errInvalidImageURL = errors.New("invalid image URL")

func parseGetURL(path string) (string, string, error) {
	slash := strings.LastIndex(path, "/")
	if slash == -1 {
		return "", "", errInvalidImageURL
	}
	dot := strings.Index(path, ".")
	if dot == -1 {
		return "", "", errInvalidImageURL
	}
	return path[slash+1 : dot], path[dot+1:], nil
}

func (a *App) getHandlerV1(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
	hash, ext, err := parseGetURL(r.URL.Path)
	if err != nil {
		notFoundError(w)
	}

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
	w.WriteHeader(fsthttp.StatusOK)
	io.Copy(w, res)
	return
}

func (a *App) optionsHandler(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
	origin := r.Header.Get("Origin")
	headers := r.Header.Get("Access-Control-Request-Headers")
	methods := r.Header.Get("Access-Control-Request-Method")

	if origin != "" && headers != "" && methods != "" {
		w.Header().Add("Access-Control-Allow-Origin", origin)
		w.Header().Add("Access-Control-Allow-Methods", "GET,HEAD,PUT,OPTIONS")
		w.Header().Add("Access-Control-Allow-Headers", headers)
		w.Header().Add("Access-Control-Max-Age", "86400")
		w.WriteHeader(fsthttp.StatusOK)
	} else {
		w.WriteHeader(fsthttp.StatusBadRequest)
	}
	return
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
			TTL:  a.TTL,
		}, nil
	})

	return res, hit, err
}

func (a *App) API() func(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
	return func(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
		const (
			root      = "/"
			imgPrefix = "/img"
			v2Prefix  = "/v2"
		)

		if r.URL.Path[0:4] == imgPrefix {
			r.URL.Path = r.URL.Path[4:]
		}

		if r.Method == fsthttp.MethodGet {
			if r.URL.Path[0:len(v2Prefix)] == v2Prefix {
				a.getHandlerV2(ctx, w, r)
				return
			}
			if r.URL.Path[0:1] == root {
				a.getHandlerV1(ctx, w, r)
				return
			}
		}
		if r.Method == fsthttp.MethodOptions && r.URL.Path == root {
			a.optionsHandler(ctx, w, r)
			return
		}
		if r.Method == fsthttp.MethodPut && r.URL.Path == root {
			a.putHandler(ctx, w, r)
			return
		}

		notFoundError(w)
	}
}
