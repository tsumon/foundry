package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	credentialManagerSecretNamespace = "OpenAI Foundry/model-profile/"
	credentialManagerProbeNamespace  = "OpenAI Foundry/openai-stage0-diagnostic/"
	credentialManagerMaxIDSize       = 256
	credentialManagerMaxBlobSize     = 5 * 512
)

var errCredentialManagerNotFound = errors.New("credential manager item not found")

type credentialManagerNative interface {
	set(target string, value []byte) error
	get(target string) ([]byte, error)
	delete(target string) error
}

type credentialManagerSecrets struct {
	native    credentialManagerNative
	namespace string
	probeID   string
}

func newCredentialManagerSecrets(native credentialManagerNative, namespace, probeID string) *credentialManagerSecrets {
	return &credentialManagerSecrets{native: native, namespace: namespace, probeID: probeID}
}

func (s *credentialManagerSecrets) target(id string) (string, error) {
	if !utf8.ValidString(id) || strings.TrimSpace(id) == "" || len(id) > credentialManagerMaxIDSize || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return "", errors.New("invalid secret identifier")
	}
	if s.probeID != "" && id != s.probeID {
		return "", errors.New("invalid diagnostic identifier")
	}
	digest := sha256.Sum256([]byte(id))
	return s.namespace + hex.EncodeToString(digest[:]), nil
}

func (s *credentialManagerSecrets) Set(id, value string) error {
	target, err := s.target(id)
	if err != nil {
		return err
	}
	if value == "" || len(value) > credentialManagerMaxBlobSize {
		return errors.New("invalid secret value size")
	}
	blob := []byte(value)
	defer clear(blob)
	if s.native == nil || s.native.set(target, blob) != nil {
		return errors.New("store secret in Windows Credential Manager")
	}
	return nil
}

func (s *credentialManagerSecrets) Get(id string) (string, error) {
	target, err := s.target(id)
	if err != nil {
		return "", err
	}
	if s.native == nil {
		return "", errors.New("read secret from Windows Credential Manager")
	}
	blob, nativeErr := s.native.get(target)
	defer clear(blob)
	if errors.Is(nativeErr, errCredentialManagerNotFound) {
		return "", errors.Join(errors.New("read secret from Windows Credential Manager"), ErrSecretNotFound)
	}
	if nativeErr != nil || len(blob) == 0 || len(blob) > credentialManagerMaxBlobSize {
		return "", errors.New("read secret from Windows Credential Manager")
	}
	return string(blob), nil
}

func (s *credentialManagerSecrets) Delete(id string) error {
	target, err := s.target(id)
	if err != nil {
		return err
	}
	if s.native == nil {
		return errors.New("delete secret from Windows Credential Manager")
	}
	err = s.native.delete(target)
	if err == nil || errors.Is(err, errCredentialManagerNotFound) {
		return nil
	}
	return errors.New("delete secret from Windows Credential Manager")
}
