//go:build darwin && cgo

package model

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

// Security.framework's C-only no-prompt switch is deprecated on newer SDKs,
// but remains the compatible fail-closed policy for this implementation.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static CFTypeRef foundry_authentication_ui_fail(void) {
	return kSecUseAuthenticationUIFail;
}
#pragma clang diagnostic pop

static OSStatus foundry_keychain_set(const char *service, const char *account, const void *value, UInt32 value_len) {
	CFStringRef service_ref = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef account_ref = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFDataRef data = CFDataCreate(NULL, value, value_len);
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFMutableDictionaryRef update = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (!service_ref || !account_ref || !data || !query || !update) {
		if (service_ref) CFRelease(service_ref);
		if (account_ref) CFRelease(account_ref);
		if (data) CFRelease(data);
		if (query) CFRelease(query);
		if (update) CFRelease(update);
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, service_ref);
	CFDictionarySetValue(query, kSecAttrAccount, account_ref);
	CFDictionarySetValue(query, kSecUseAuthenticationUI, foundry_authentication_ui_fail());
	CFDictionarySetValue(update, kSecValueData, data);
	OSStatus status = SecItemUpdate(query, update);
	if (status == errSecItemNotFound) {
		CFDictionarySetValue(query, kSecValueData, data);
		status = SecItemAdd(query, NULL);
	}
	CFRelease(update);
	CFRelease(query);
	CFRelease(data);
	CFRelease(account_ref);
	CFRelease(service_ref);
	return status;
}

static OSStatus foundry_keychain_get(const char *service, const char *account, UInt32 *value_len, void **value) {
	CFStringRef service_ref = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef account_ref = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (!service_ref || !account_ref || !query) {
		if (service_ref) CFRelease(service_ref);
		if (account_ref) CFRelease(account_ref);
		if (query) CFRelease(query);
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, service_ref);
	CFDictionarySetValue(query, kSecAttrAccount, account_ref);
	CFDictionarySetValue(query, kSecUseAuthenticationUI, foundry_authentication_ui_fail());
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	if (status == errSecSuccess) {
		CFDataRef data = (CFDataRef)result;
		CFIndex length = CFDataGetLength(data);
		void *copy = malloc((size_t)length);
		if (!copy && length > 0) {
			status = errSecAllocate;
		} else {
			memcpy(copy, CFDataGetBytePtr(data), (size_t)length);
			*value = copy;
			*value_len = (UInt32)length;
		}
		CFRelease(result);
	}
	CFRelease(query);
	CFRelease(account_ref);
	CFRelease(service_ref);
	return status;
}

static OSStatus foundry_keychain_delete(const char *service, const char *account) {
	CFStringRef service_ref = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef account_ref = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (!service_ref || !account_ref || !query) {
		if (service_ref) CFRelease(service_ref);
		if (account_ref) CFRelease(account_ref);
		if (query) CFRelease(query);
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, service_ref);
	CFDictionarySetValue(query, kSecAttrAccount, account_ref);
	CFDictionarySetValue(query, kSecUseAuthenticationUI, foundry_authentication_ui_fail());
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	CFRelease(account_ref);
	CFRelease(service_ref);
	if (status == errSecItemNotFound) return errSecSuccess;
	return status;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

const keychainService = "com.openai.foundry.model-profile"
const keychainProbeService = "com.openai.foundry.openai-stage0-diagnostic"

type keychainSecrets struct {
	service string
	probeID string
}

func NewSecrets() Secrets {
	return &keychainSecrets{service: keychainService}
}

func NewSecretProbeStore(id string) (Secrets, error) {
	if err := ValidateSecretProbeID(id); err != nil {
		return nil, err
	}
	return &keychainSecrets{service: keychainProbeService, probeID: id}, nil
}

func (s *keychainSecrets) validateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("secret ID is required")
	}
	if s.probeID != "" && id != s.probeID {
		return errors.New("invalid diagnostic identifier")
	}
	return nil
}

func (s *keychainSecrets) Set(id, value string) error {
	if err := s.validateID(id); err != nil {
		return err
	}
	if value == "" {
		return errors.New("secret value is required")
	}
	service := C.CString(s.service)
	account := C.CString(id)
	secret := C.CBytes([]byte(value))
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	defer C.free(secret)
	status := C.foundry_keychain_set(service, account, secret, C.UInt32(len(value)))
	if status != C.errSecSuccess {
		return fmt.Errorf("store secret in Keychain: status %d", int32(status))
	}
	return nil
}

func (s *keychainSecrets) Get(id string) (string, error) {
	if err := s.validateID(id); err != nil {
		return "", err
	}
	service := C.CString(s.service)
	account := C.CString(id)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	var length C.UInt32
	var value unsafe.Pointer
	status := C.foundry_keychain_get(service, account, &length, &value)
	if status == C.errSecItemNotFound {
		return "", fmt.Errorf("read secret from Keychain: %w", ErrSecretNotFound)
	}
	if status != C.errSecSuccess {
		return "", fmt.Errorf("read secret from Keychain: status %d", int32(status))
	}
	defer C.free(value)
	return string(C.GoBytes(value, C.int(length))), nil
}

func (s *keychainSecrets) Delete(id string) error {
	if err := s.validateID(id); err != nil {
		return err
	}
	service := C.CString(s.service)
	account := C.CString(id)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	status := C.foundry_keychain_delete(service, account)
	if status != C.errSecSuccess {
		return fmt.Errorf("delete secret from Keychain: status %d", int32(status))
	}
	return nil
}
