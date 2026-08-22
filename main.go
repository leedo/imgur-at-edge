package main

import (
	"fmt"
	"imgur-at-edge/api"
	"os"
	"time"

	"github.com/fastly/compute-sdk-go/fsthttp"
	"github.com/fastly/compute-sdk-go/kvstore"
)

const kvstoreName = "images"

func main() {
	fmt.Println("FASTLY_SERVICE_VERSION:", os.Getenv("FASTLY_SERVICE_VERSION"))

	s, err := kvstore.Open(kvstoreName)
	if err != nil {
		panic(err)
	}

	app := api.App{
		MaxLength:         1024 * 1024 * 25,
		ValidateBufLength: 1024 * 10,
		KVStore:           s,
		TTL:               24 * time.Hour,
	}

	fsthttp.ServeFunc(app.API())
}
