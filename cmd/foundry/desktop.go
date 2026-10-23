package main

import (
	"errors"
	"foundry/internal/model"
	"strings"
)

// Desktop alpha has no native credential activation. Do not touch Keychain,
// including recovery cleanup, through normal account/profile UI requests.
type desktopSecrets struct{}

func (desktopSecrets) Set(string, string) error   { return model.ErrSecretStoreUnavailable }
func (desktopSecrets) Get(string) (string, error) { return "", model.ErrSecretStoreUnavailable }
func (desktopSecrets) Delete(string) error        { return model.ErrSecretStoreUnavailable }

func desktopOptions(args []string) (bool, error) {
	for _, arg := range args {
		if strings.Contains(arg, "coding-workbench") {
			if len(args) == 2 && args[0] == "--desktop" && args[1] == "--coding-workbench" {
				return true, nil
			}
			return false, errors.New("coding workbench requires exactly --desktop --coding-workbench")
		}
	}
	desktop := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "--desktop") || strings.HasPrefix(arg, "-desktop") {
			desktop = true
		}
	}
	if desktop && (len(args) != 1 || args[0] != "--desktop") {
		return false, errors.New("desktop mode cannot be combined with operator options")
	}
	return desktop, nil
}
