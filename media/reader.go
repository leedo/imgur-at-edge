package media

import (
	"crypto/sha1"
	"errors"
	"io"
)

type ValidMediaReader struct {
	reader            io.ReadCloser
	validators        []validator
	buf               []byte
	validated         bool
	kind              string
	pos               int
	eof               bool
	validateBufLength int
	Hash              []byte
}

var (
	ErrMustValidate        = errors.New("must call Validate before Read")
	ErrNoValidatorsForType = errors.New("no validators for extension")
)

func (v *ValidMediaReader) Read(p []byte) (int, error) {
	if !v.validated {
		return 0, ErrMustValidate
	}

	if v.pos < len(v.buf) {
		end := min(len(v.buf), v.pos+cap(p))
		n := copy(p, v.buf[v.pos:end])
		v.pos += n
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

	b := make([]byte, v.validateBufLength)
	var n int
	var err error

	for n < v.validateBufLength && err == nil {
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

	v.Hash = sha.Sum(nil)
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

func NewValidMediaReader(r io.ReadCloser, kind string, validateBufLength int) (*ValidMediaReader, error) {
	validators, ok := magic[kind]
	if !ok {
		return nil, ErrNoValidatorsForType
	}
	return &ValidMediaReader{
		reader:            r,
		validators:        validators,
		kind:              kind,
		validateBufLength: validateBufLength,
	}, nil
}
