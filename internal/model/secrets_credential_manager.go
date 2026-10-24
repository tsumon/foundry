package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	credentialManagerSecretNamespace = "OpenAI Foundry/model-profile/"
	credentialManagerProbeNamespace  = "OpenAI Foundry/openai-stage0-diagnostic/"
	credentialManagerMaxIDSize       = 256
	credentialManagerMaxBlobSize     = 5 * 512
	credentialManagerMaxPayloadSize  = 256 * 1024
	credentialManagerMaxChunks       = (credentialManagerMaxPayloadSize + credentialManagerMaxBlobSize - 1) / credentialManagerMaxBlobSize
	// A NUL-prefixed reserved family cannot be confused with a token bundle.
	// All records in this family (including unknown versions) fail closed.
	credentialManagerEnvelopePrefix = "\x00Foundry/segmented/"
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
	if value == "" || len(value) > credentialManagerMaxPayloadSize || value[0] == 0 {
		return errors.New("invalid secret value size")
	}
	blob := []byte(value)
	defer clear(blob)
	if s.native == nil {
		return errors.New("store secret in Windows Credential Manager")
	}
	writeErr := errors.New("store secret in Windows Credential Manager")
	// Account authorization owns a durable write intent and unique version ref.
	// This wrapper never publishes a partial version or invents other references.
	if len(blob) <= credentialManagerMaxBlobSize {
		if s.native.set(target, blob) != nil {
			_ = s.Delete(id)
			return writeErr
		}
		return nil
	}
	pending := []byte(credentialManagerEnvelopePrefix + "1/pending")
	if s.native.set(target, pending) != nil {
		_ = s.Delete(id)
		return writeErr
	}
	for offset, index := 0, 0; offset < len(blob); offset, index = offset+credentialManagerMaxBlobSize, index+1 {
		end := min(offset+credentialManagerMaxBlobSize, len(blob))
		if s.native.set(credentialManagerChunkTarget(target, index), blob[offset:end]) != nil {
			_ = s.Delete(id)
			return writeErr
		}
	}
	// Fixed-width envelope: family, version/state, total length, count, SHA-256.
	envelope := append([]byte(credentialManagerEnvelopePrefix), 1, 1)
	envelope = binary.BigEndian.AppendUint32(envelope, uint32(len(blob)))
	envelope = binary.BigEndian.AppendUint32(envelope, uint32((len(blob)+credentialManagerMaxBlobSize-1)/credentialManagerMaxBlobSize))
	digest := sha256.Sum256(blob)
	envelope = append(envelope, digest[:]...)
	if s.native.set(target, envelope) != nil {
		_ = s.Delete(id)
		return writeErr
	}
	return nil
}

func credentialManagerChunkTarget(root string, index int) string {
	return root + "/segment-v1/" + strconv.Itoa(index)
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
	if blob[0] == 0 {
		invalid := errors.New("invalid segmented Windows credential")
		header := len(credentialManagerEnvelopePrefix)
		if !bytes.HasPrefix(blob, []byte(credentialManagerEnvelopePrefix)) || len(blob) != header+42 || blob[header] != 1 || blob[header+1] != 1 {
			return "", invalid
		}
		size := int(binary.BigEndian.Uint32(blob[header+2:]))
		count := int(binary.BigEndian.Uint32(blob[header+6:]))
		if size <= credentialManagerMaxBlobSize || size > credentialManagerMaxPayloadSize || count != (size+credentialManagerMaxBlobSize-1)/credentialManagerMaxBlobSize {
			return "", invalid
		}
		value := make([]byte, 0, size)
		defer func() { clear(value) }()
		for index := 0; index < count; index++ {
			chunk, err := s.native.get(credentialManagerChunkTarget(target, index))
			want := min(credentialManagerMaxBlobSize, size-len(value))
			if err != nil || len(chunk) != want {
				clear(chunk)
				return "", invalid
			}
			value = append(value, chunk...)
			clear(chunk)
		}
		digest := sha256.Sum256(value)
		if !bytes.Equal(digest[:], blob[header+10:]) {
			return "", invalid
		}
		return string(value), nil
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
	failed := false
	// Root first prevents any successfully invalidated version being read while
	// cleanup proceeds. Sweep every exact bounded target even without a root.
	for index := -1; index < credentialManagerMaxChunks; index++ {
		item := target
		if index >= 0 {
			item = credentialManagerChunkTarget(target, index)
		}
		err := s.native.delete(item)
		if err != nil && !errors.Is(err, errCredentialManagerNotFound) {
			failed = true
		}
		remaining, readErr := s.native.get(item)
		clear(remaining)
		if !errors.Is(readErr, errCredentialManagerNotFound) {
			failed = true
		}
	}
	if failed {
		return errors.New("delete secret from Windows Credential Manager")
	}
	return nil
}
