package api

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	pbkey "imgur-at-edge/protos/key"
	"strconv"

	"google.golang.org/protobuf/proto"
)

func decodeKey(key string) (*pbkey.Key, error) {
	keydec, err := hex.DecodeString(key)
	if err != nil {
		return nil, err
	}

	var k pbkey.Key
	if err := proto.Unmarshal(keydec, &k); err != nil {
		return nil, err
	}

	return &k, nil
}

func encodeKey(hash []byte, ext string, length uint32) (string, error) {
	extt, ok := pbkey.Extension_value[ext]
	if !ok {
		return "", errors.New("unknown extension")
	}

	extenum := pbkey.Extension(extt)
	i := binary.BigEndian.Uint64(hash)
	key := pbkey.Key{
		Hash:      &i,
		Extension: &extenum,
		Size:      &length,
	}

	kenc, err := proto.Marshal(&key)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(kenc), nil
}

func checkIfNoneMatch(inm string, hash *uint64) bool {
	if inm == "" || hash == nil || len(inm) < 3 {
		return false
	}

	if inm[0:1] != "\"" || inm[len(inm)-1:] != "\"" {
		return false
	}

	i, err := strconv.ParseUint(inm[1:len(inm)-1], 16, 64)
	if err != nil {
		return false
	}

	return i == *hash
}
