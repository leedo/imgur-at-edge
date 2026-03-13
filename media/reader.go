package media

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
)

const defaultValidateLength = 1024

type ValidMediaReader struct {
	reader         io.ReadCloser
	validators     []validator
	buf            []byte
	validated      bool
	kind           string
	eof            bool
	ValidateLength int
	Hash           string
}

var ErrMustValidate = errors.New("must call Validate before Read")

func (v *ValidMediaReader) Read(p []byte) (int, error) {
	if !v.validated {
		return 0, ErrMustValidate
	}

	if len(v.buf) > 0 {
		// todo check p capacity
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

	b := make([]byte, v.ValidateLength)
	var n int
	var err error

	for n < v.ValidateLength && err == nil {
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

	v.Hash = hex.EncodeToString(sha.Sum(nil))
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

func NewValidMediaReader(r io.ReadCloser, kind string) (*ValidMediaReader, error) {
	validators, ok := magic[kind]
	if !ok {
		return nil, errors.New("no validators for " + kind)
	}
	return &ValidMediaReader{
		reader:         r,
		validators:     validators,
		kind:           kind,
		ValidateLength: defaultValidateLength,
	}, nil
}
