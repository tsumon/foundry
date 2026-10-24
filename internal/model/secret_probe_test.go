package model

import (
	"errors"
	"strings"
	"testing"
)

type probeSecrets struct {
	value        string
	setCalls     int
	getCalls     int
	deleteCalls  int
	setErrors    map[int]error
	getErrors    map[int]error
	deleteError  error
	deleteClears bool
	wrongReads   map[int]string
	writtenSizes []int
}

func (s *probeSecrets) Set(_ string, value string) error {
	s.setCalls++
	s.writtenSizes = append(s.writtenSizes, len(value))
	s.value = value // model a backend that partly writes before reporting failure
	return s.setErrors[s.setCalls]
}
func (s *probeSecrets) Get(string) (string, error) {
	s.getCalls++
	if err := s.getErrors[s.getCalls]; err != nil {
		return "", err
	}
	if wrong, ok := s.wrongReads[s.getCalls]; ok {
		return wrong, nil
	}
	if s.value == "" {
		return "", ErrSecretNotFound
	}
	return s.value, nil
}
func (s *probeSecrets) Delete(string) error {
	s.deleteCalls++
	if s.deleteError == nil || s.deleteClears {
		s.value = ""
	}
	return s.deleteError
}

func TestSecretProbeRoundTripAndCleanup(t *testing.T) {
	store := &probeSecrets{}
	report := RunSecretProbe(store, "foundry-openai-stage0-0123456789abcdef0123456789abcdef")
	if report.Status != "passed" || report.FailedStep != "" || report.Cleanup != "confirmed" {
		t.Fatalf("report=%+v", report)
	}
	if store.setCalls != 2 || store.getCalls != 3 || store.deleteCalls != 1 || store.value != "" {
		t.Fatalf("calls=%+v", store)
	}
}

func TestSecretProbeExercisesSubscriptionSizedNativeValues(t *testing.T) {
	store := &probeSecrets{}
	report := RunSecretProbe(store, "foundry-openai-stage0-0123456789abcdef0123456789abcdef")
	if report.Status != "passed" || len(store.writtenSizes) != 2 {
		t.Fatal("probe failed")
	}
	for _, size := range store.writtenSizes {
		if size <= 2560 || size > 256*1024 {
			t.Fatalf("diagnostic skipped subscription-sized storage: %d bytes", size)
		}
	}
}

func TestSecretProbeFailuresStayFiniteAndAlwaysCleanup(t *testing.T) {
	denied := errors.New("permission denied raw-marker")
	tests := []struct {
		name        string
		store       *probeSecrets
		wantStep    string
		wantCleanup string
	}{
		{"partial initial write", &probeSecrets{setErrors: map[int]error{1: denied}}, "write_initial", "confirmed"},
		{"initial missing", &probeSecrets{getErrors: map[int]error{1: ErrSecretNotFound}}, "read_initial_missing", "confirmed"},
		{"initial denied", &probeSecrets{getErrors: map[int]error{1: denied}}, "read_initial_failed", "confirmed"},
		{"stale initial read", &probeSecrets{wrongReads: map[int]string{1: "stale"}}, "read_initial_mismatch", "confirmed"},
		{"replace failed", &probeSecrets{setErrors: map[int]error{2: denied}}, "write_replace", "confirmed"},
		{"replacement read denied", &probeSecrets{getErrors: map[int]error{2: denied}}, "read_replace_failed", "confirmed"},
		{"stale replacement read", &probeSecrets{wrongReads: map[int]string{2: "stale"}}, "read_replace_mismatch", "confirmed"},
		{"delete failed", &probeSecrets{deleteError: denied}, "cleanup_delete", "unconfirmed"},
		{"delete error despite absence", &probeSecrets{deleteError: denied, deleteClears: true}, "cleanup_delete", "unconfirmed"},
		{"verify denied", &probeSecrets{getErrors: map[int]error{3: denied}}, "cleanup_verify_failed", "unconfirmed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := RunSecretProbe(tt.store, "foundry-openai-stage0-0123456789abcdef0123456789abcdef")
			if report.Status != "failed" || report.FailedStep != tt.wantStep || report.Cleanup != tt.wantCleanup || tt.store.deleteCalls != 1 {
				t.Fatalf("report=%+v store=%+v", report, tt.store)
			}
			raw := report.String()
			if strings.Contains(raw, "raw-marker") || strings.Contains(raw, "permission denied") || strings.Contains(raw, tt.store.value) && tt.store.value != "" {
				t.Fatalf("unsafe report %q", raw)
			}
		})
	}
}

func TestCleanupSecretProbeRequiresTypedNotFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"missing", ErrSecretNotFound, "confirmed"},
		{"denied", errors.New("denied"), "unconfirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &probeSecrets{getErrors: map[int]error{1: tc.err}}
			report := CleanupSecretProbe(store, "foundry-openai-stage0-0123456789abcdef0123456789abcdef")
			if report.Cleanup != tc.want || store.setCalls != 0 || store.deleteCalls != 1 || store.getCalls != 1 {
				t.Fatalf("report=%+v store=%+v", report, store)
			}
		})
	}
}

func TestSecretProbeIdentifierIsStrict(t *testing.T) {
	valid := "foundry-openai-stage0-0123456789abcdef0123456789abcdef"
	if err := ValidateSecretProbeID(valid); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "account", valid + "0", "foundry-openai-stage0-../../account", "foundry-openai-stage0-0123456789ABCDEF0123456789ABCDEF"} {
		if ValidateSecretProbeID(id) == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}
