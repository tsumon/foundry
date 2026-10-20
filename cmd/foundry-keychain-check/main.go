package main

import (
	"foundry/internal/keychaincheck"
	"log"
	"os"
)

func main() {
	if err := keychaincheck.Run(os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}
