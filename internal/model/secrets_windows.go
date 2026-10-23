//go:build windows

package model

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsCredentialTypeGeneric         = 1
	windowsCredentialPersistLocalMachine = 2
)

var (
	credentialManagerDLL    = windows.NewLazySystemDLL("advapi32.dll")
	credentialManagerWrite  = credentialManagerDLL.NewProc("CredWriteW")
	credentialManagerRead   = credentialManagerDLL.NewProc("CredReadW")
	credentialManagerDelete = credentialManagerDLL.NewProc("CredDeleteW")
	credentialManagerFree   = credentialManagerDLL.NewProc("CredFree")
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsCredentialManager struct{}

func NewSecrets() Secrets {
	return newCredentialManagerSecrets(windowsCredentialManager{}, credentialManagerSecretNamespace, "")
}

func NewSecretProbeStore(id string) (Secrets, error) {
	if err := ValidateSecretProbeID(id); err != nil {
		return nil, err
	}
	return newCredentialManagerSecrets(windowsCredentialManager{}, credentialManagerProbeNamespace, id), nil
}

func (windowsCredentialManager) set(target string, value []byte) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil || len(value) == 0 || len(value) > credentialManagerMaxBlobSize {
		return errors.New("invalid credential input")
	}
	credential := windowsCredential{
		Type:               windowsCredentialTypeGeneric,
		TargetName:         name,
		CredentialBlobSize: uint32(len(value)),
		CredentialBlob:     &value[0],
		Persist:            windowsCredentialPersistLocalMachine,
	}
	ok, _, callErr := credentialManagerWrite.Call(uintptr(unsafe.Pointer(&credential)), 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(value)
	if ok == 0 {
		return callErr
	}
	return nil
}

func (windowsCredentialManager) get(target string) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, errors.New("invalid credential input")
	}
	var credential *windowsCredential
	ok, _, callErr := credentialManagerRead.Call(
		uintptr(unsafe.Pointer(name)),
		windowsCredentialTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	runtime.KeepAlive(name)
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, errCredentialManagerNotFound
		}
		return nil, callErr
	}
	defer credentialManagerFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlobSize == 0 || credential.CredentialBlobSize > credentialManagerMaxBlobSize || credential.CredentialBlob == nil {
		return nil, errors.New("invalid credential blob")
	}
	value := append([]byte(nil), unsafe.Slice(credential.CredentialBlob, int(credential.CredentialBlobSize))...)
	return value, nil
}

func (windowsCredentialManager) delete(target string) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return errors.New("invalid credential input")
	}
	ok, _, callErr := credentialManagerDelete.Call(
		uintptr(unsafe.Pointer(name)),
		windowsCredentialTypeGeneric,
		0,
	)
	runtime.KeepAlive(name)
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return errCredentialManagerNotFound
		}
		return callErr
	}
	return nil
}
