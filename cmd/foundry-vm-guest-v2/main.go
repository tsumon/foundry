package main

import (
	"foundry/internal/vmguest"
	"os"
)

func main() {
	if vmguest.MainV2() != nil {
		os.Exit(1)
	}
}
