// Package keychaincheck does not implement a Windows credential backend.
package keychaincheck

import (
	"errors"
	"io"
)

func Run([]string, io.Writer) error {
	return errors.New("native credential activation is unsupported on Windows")
}
func Probe(string) error { return errors.New("native credential activation is unsupported on Windows") }
func ProbeDesktop(string) error {
	return errors.New("native credential activation is unsupported on Windows")
}
