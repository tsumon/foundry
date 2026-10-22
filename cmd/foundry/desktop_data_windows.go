package main

import (
	"errors"
	"fmt"
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func prepareDesktopData(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "--desktop-prepare-data" {
		return false, nil
	}
	if len(args) > 2 {
		return true, errors.New("invalid desktop storage options")
	}
	root := ""
	if len(args) == 2 {
		root = args[1]
	} else {
		base, e := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
		if e != nil {
			return true, e
		}
		root = filepath.Join(base, "Foundry")
	}
	if e := platformfs.PrivateDirectory(root); e != nil {
		return true, e
	}
	for _, name := range []string{"workbench", "simulations", "electron"} {
		if e := platformfs.PrivateDirectory(filepath.Join(root, name)); e != nil {
			return true, e
		}
	}
	_, e := fmt.Fprintln(os.Stdout, root)
	return true, e
}
