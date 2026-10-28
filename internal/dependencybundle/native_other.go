//go:build !darwin && !linux && !windows

package dependencybundle

import (
	"os"
)

// Unsupported platforms fail closed rather than weakening publication or links.
func holdDirectory(string) (directoryHandle, error) { return nil, ErrIO }
func privateDirectory(string, os.FileInfo) error    { return ErrIO }
func payloadMode(os.FileMode) bool                  { return false }
func singleLink(*os.File, os.FileInfo) error        { return ErrIO }
func makeDirectory(*os.Root, string) error          { return ErrIO }
func syncFile(*os.File) error                       { return ErrIO }
func syncRoot(*os.Root) error                       { return ErrIO }
func publishAbsent(*os.Root, string, string) error  { return ErrIO }

func makeStage(*os.Root) (string, error) { return "", ErrIO }
