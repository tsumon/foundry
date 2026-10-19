package model

import "errors"

var ErrSecretNotFound = errors.New("secret not found")
var ErrSecretStoreUnavailable = errors.New("secret store unavailable")

type Secrets interface {
	Set(id, value string) error
	Get(id string) (string, error)
	Delete(id string) error
}
