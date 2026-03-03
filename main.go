package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"

	"github.com/fastly/compute-sdk-go/fsthttp"
	"github.com/fastly/compute-sdk-go/kvstore"
)

const minValidationLen = 1024
const maxLength = 1024 * 1024 * 25

var types = map[string]string{
	"image/jpeg":      "jpg",
	"image/gif":       "gif",
	"image/x-png":     "png",
	"image/png":       "png",
	"video/quicktime": "mov",
	"video/mp4":       "mp4",
}

var mimes = map[string]string{
	"jpg": "image/jpeg",
	"gif": "image/gif",
	"png": "image/png",
	"mov": "video/quicktime",
	"mp4": "video/mp4",
}

type validator struct {
	bytes  []byte
	offset uint32
}

var magic = map[string][]validator{
	"png": []validator{
		validator{[]byte{0x89, 0x50, 0x4E, 0x47}, 0},
	},
	"gif": []validator{
		validator{[]byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}, 0},
		validator{[]byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, 0},
	},
	"jpg": []validator{
		validator{[]byte{0xFF, 0xD8, 0xFF, 0xDB}, 0},
		validator{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, 0},
		validator{[]byte{0xFF, 0xD8, 0xFF, 0xEE}, 0},
		validator{[]byte{0xFF, 0xD8, 0xFF, 0xE1}, 0},
	},
	"mp4": []validator{
		validator{[]byte{0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6F, 0x6D}, 4},
		validator{[]byte{0x66, 0x74, 0x79, 0x70, 0x4D, 0x53, 0x4E, 0x56}, 4},
		validator{[]byte{0x66, 0x74, 0x79, 0x70, 0x69, 0x73}, 4},
		validator{[]byte{0x66, 0x74, 0x79, 0x70, 0x6D, 0x70}, 4},
	},
	"mov": []validator{
		validator{[]byte{0x66, 0x74, 0x79, 0x70, 0x71, 0x74, 0x20, 0x20}, 4},
	},
}

func (v validator) validate(b []byte) bool {
	return bytes.Equal(b[v.offset:int(v.offset)+len(v.bytes)], v.bytes)
}

type ValidatorError struct {
	kind string
}

func (e ValidatorError) Error() string {
	return "invalid " + e.kind
}

type ValidMediaReader struct {
	reader     io.ReadCloser
	validators []validator
	buf        []byte
	validated  bool
	kind       string
	name       string
	eof        bool
	hash       string
}

var ErrMustValidate = errors.New("must call Validate before Read")

func (v *ValidMediaReader) Read(p []byte) (int, error) {
	if !v.validated {
		return 0, ErrMustValidate
	}

	if len(v.buf) > 0 {
		n := copy(p, v.buf)
		v.buf = nil
		return n, nil
	}

	if v.eof {
		return 0, io.EOF
	}

	return v.reader.Read(p)
}

func (v *ValidMediaReader) Validate() error {
	sha := sha1.New()
	r := io.TeeReader(v.reader, sha)

	b := make([]byte, minValidationLen)
	var n int
	var err error

	for n < minValidationLen && err == nil {
		var nn int
		nn, err = r.Read(b[n:])
		n += nn
	}
	if err != nil {
		if n > 0 && err == io.EOF {
			// swallow eof error for returning from Read call
			v.eof = true
		} else {
			return err
		}
	}

	v.hash = hex.EncodeToString(sha.Sum(nil))
	v.validated = true

	for _, validator := range v.validators {
		if ok := validator.validate(b); ok {
			v.buf = b
			return nil
		}
	}
	return ValidatorError{v.kind}
}

func (v *ValidMediaReader) Close() error {
	return v.reader.Close()
}

func NewValidMediaReader(r io.ReadCloser, validators []validator, kind string) *ValidMediaReader {
	return &ValidMediaReader{
		reader:     r,
		validators: validators,
		kind:       kind,
	}
}

var getPath = regexp.MustCompile(`^/([a-zA-Z0-9]+)\.(jpg|png|gif|mp4|mov)$`)

func badRequest(w fsthttp.ResponseWriter, msg string) {
	type jsError struct {
		Err string `json:"error"`
	}
	w.WriteHeader(fsthttp.StatusBadRequest)
	if err := json.NewEncoder(w).Encode(jsError{msg}); err != nil {
		log.Printf("error writing error: %s", err)
	}
}

func internalError(w fsthttp.ResponseWriter, msg string) {
	log.Println(msg)
	w.WriteHeader(fsthttp.StatusInternalServerError)
	fmt.Fprintf(w, `{"error":"internal error"}`)
}

func main() {
	fmt.Println("FASTLY_SERVICE_VERSION:", os.Getenv("FASTLY_SERVICE_VERSION"))

	fsthttp.ServeFunc(func(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
		if r.Method == "PUT" {
			if r.URL.Path == "/" {
				// validate type
				cType := r.Header.Get("Content-Type")
				ext, extOk := types[cType]
				if !extOk {
					badRequest(w, "unknown content-type")
					return
				}

				validators, ok := magic[ext]
				if !ok {
					badRequest(w, "no validators for "+ext)
					return
				}

				// validate length
				cLen := r.Header.Get("Content-Length")
				if cLen == "" {
					badRequest(w, "missing content-length")
					return
				}
				u, err := strconv.ParseUint(cLen, 10, 64)
				if err != nil {
					badRequest(w, "invalid content-length")
					return
				}
				if u > maxLength {
					badRequest(w, "content-length too large")
					return
				}

				v := NewValidMediaReader(r.Body, validators, ext)
				defer v.Close()

				if err := v.Validate(); err != nil {
					var validatorErr ValidatorError
					if errors.As(err, &validatorErr) {
						badRequest(w, validatorErr.Error())
						return
					}
					internalError(w, "error validating: "+err.Error())
					return
				}

				s, err := kvstore.Open("images")
				if err != nil {
					internalError(w, "error opening: "+err.Error())
					return
				}

				//id := strings.ReplaceAll(uuid.New().String(), "-", "")
				id := v.hash
				if err := s.Insert(id, v); err != nil {
					internalError(w, "error inserting: "+err.Error())
					return
				}

				w.Header().Add("Content-Type", "application/json")

				if origin := r.Header.Get("Origin"); origin != "" {
					w.Header().Add("Access-Control-Allow-Origin", origin)
				}

				w.WriteHeader(fsthttp.StatusOK)
				const js = `{"status": "ok", "data": {"id": "%s", "link": "https://%s/%s.%s"}}`
				fmt.Fprintf(w, js, id, r.URL.Host, id, ext)
				return
			}
		} else if r.Method == "GET" {
			if getPath.MatchString(r.URL.Path) {
				s, err := kvstore.Open("images")
				if err != nil {
					internalError(w, "error opening "+err.Error())
					return
				}

				file := r.URL.Path[1:]
				id := string(file[:len(file)-4])
				ext := string(file[len(file)-3:])
				mime, ok := mimes[ext]
				if !ok {
					badRequest(w, "unknown extension "+ext)
					return
				}

				res, err := s.Lookup(id)
				if err != nil {
					internalError(w, fmt.Sprintf("error doing lookup: %s %s\n", id, err))
					return
				}

				w.Header().Add("Content-Type", mime)
				w.WriteHeader(fsthttp.StatusOK)
				io.Copy(w, res)
				return
			}
		} else if r.Method == "OPTIONS" {
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

		w.WriteHeader(fsthttp.StatusNotFound)
		fmt.Fprint(w, "not found")
	})
}
