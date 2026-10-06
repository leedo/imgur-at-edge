package main

import (
	"fmt"
	"imgur-at-edge/api"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Please provide a key as an argument.")
		return
	}
	key := os.Args[1]
	k, err := api.DecodeKey(key)
	if err != nil {
		fmt.Println("Error decoding key:", err)
		return
	}
	fmt.Printf("Decoded Key: %+v\n", k)
}
