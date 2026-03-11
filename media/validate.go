package media

import "bytes"

const minValidationLen = 1024

type validator struct {
	bytes  []byte
	offset uint32
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
