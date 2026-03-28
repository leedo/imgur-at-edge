package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

var (
	ErrMissingContentLength  = errors.New("missing content-length")
	ErrInvalidContentLength  = errors.New("invalid content-length")
	ErrContentLengthTooLarge = errors.New("content-length too large")
	ErrUnknownContentType    = errors.New("unknown content-type")
)

func badRequest(w http.ResponseWriter, msg string) {
	jsonError(w, http.StatusBadRequest, msg)
}

func internalError(w http.ResponseWriter, msg string) {
	log.Println(msg)
	jsonError(w, http.StatusInternalServerError, "internal error")
}

func notFoundError(w http.ResponseWriter) {
	jsonError(w, http.StatusNotFound, "not found")
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	type jsError struct {
		Err string `json:"error"`
	}
	w.Header().Add("Content-Type", jsonType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(jsError{msg}); err != nil {
		log.Printf("error writing error: %s", err)
	}
}
