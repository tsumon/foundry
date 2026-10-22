package platformfs

import "os"

// Retain validated ancestor handles for the entire file read on Windows.
type File struct {
	*os.File
	parents []*os.File
}

func (f *File) Close() error {
	e := f.File.Close()
	for i := len(f.parents) - 1; i >= 0; i-- {
		if x := f.parents[i].Close(); e == nil {
			e = x
		}
	}
	return e
}
