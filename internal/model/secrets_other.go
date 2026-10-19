//go:build !darwin || !cgo

package model

import "errors"

type unsupportedSecrets struct{}

func NewSecrets() Secrets {
	return unsupportedSecrets{}
}

func (unsupportedSecrets) Set(string, string) error {
	return errors.New("macOS Keychain is unavailable on this platform")
}

func (unsupportedSecrets) Get(string) (string, error) {
	return "", errors.New("macOS Keychain is unavailable on this platform")
}

func (unsupportedSecrets) Delete(string) error {
	return errors.New("macOS Keychain is unavailable on this platform")
}
