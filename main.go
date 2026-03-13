package main

import (
	"fmt"
	"imgur-at-edge/api"
	"os"

	"github.com/fastly/compute-sdk-go/fsthttp"
)

func main() {
	fmt.Println("FASTLY_SERVICE_VERSION:", os.Getenv("FASTLY_SERVICE_VERSION"))

	app := api.App{
		MaxLength:   1024 * 1024 * 25,
		KVStoreName: "images",
	}

	fsthttp.Serve(fsthttp.Adapt(app.NewServeMux()))
}
