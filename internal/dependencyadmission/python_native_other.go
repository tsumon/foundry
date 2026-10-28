//go:build !darwin && !linux && !windows

package dependencyadmission

import "os"

func pythonHoldDirectory(string) (pythonDirectoryHandle, error) { return nil, errInvalid }
func pythonSingleLink(*os.File, os.FileInfo) bool               { return false }
func pythonPrivateDirectory(string, os.FileInfo) error          { return errInvalid }
