package main

import (
	"fmt"
	"foundry/internal/vmguest"
	"os"
)

func main() {
	if err := vmguest.Main(); err != nil {
		fmt.Fprintln(os.Stderr, "guest supervisor stopped:", err)
		os.Exit(1)
	}
}
