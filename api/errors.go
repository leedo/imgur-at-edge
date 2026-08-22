package api

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/fastly/compute-sdk-go/fsthttp"
)

var (
	ErrMissingContentLength  = errors.New("missing content-length")
	ErrInvalidContentLength  = errors.New("invalid content-length")
	ErrContentLengthTooLarge = errors.New("content-length too large")
	ErrUnknownContentType    = errors.New("unknown content-type")
)

func badRequest(w fsthttp.ResponseWriter, msg string) {
	jsonError(w, fsthttp.StatusBadRequest, msg)
}

func internalError(w fsthttp.ResponseWriter, msg string) {
	log.Println(msg)
	jsonError(w, fsthttp.StatusInternalServerError, "internal error")
}

func notFoundError(w fsthttp.ResponseWriter) {
	jsonError(w, fsthttp.StatusNotFound, "not found")
}

func jsonError(w fsthttp.ResponseWriter, status int, msg string) {
	type jsError struct {
		Err string `json:"error"`
	}
	w.Header().Add("Content-Type", jsonType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(jsError{msg}); err != nil {
		log.Printf("error writing error: %s", err)
	}
}
