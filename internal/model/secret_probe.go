package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
)

const secretProbePrefix = "foundry-openai-stage0-"

var secretProbePattern = regexp.MustCompile(`^foundry-openai-stage0-[0-9a-f]{32}$`)

type SecretProbeReport struct {
	ProbeID    string `json:"probe_id"`
	Status     string `json:"status"`
	FailedStep string `json:"failed_step,omitempty"`
	Cleanup    string `json:"cleanup"`
}

func (r SecretProbeReport) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

func GenerateSecretProbeID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("generate diagnostic identifier")
	}
	return secretProbePrefix + hex.EncodeToString(b), nil
}

func ValidateSecretProbeID(id string) error {
	if !secretProbePattern.MatchString(id) {
		return errors.New("invalid diagnostic identifier")
	}
	return nil
}

func RunSecretProbe(store Secrets, id string) SecretProbeReport {
	report := SecretProbeReport{ProbeID: id, Status: "failed", FailedStep: "identifier", Cleanup: "unconfirmed"}
	if store == nil || ValidateSecretProbeID(id) != nil {
		return report
	}
	const first = "synthetic-foundry-stage0-a"
	const second = "synthetic-foundry-stage0-b"
	if err := store.Set(id, first); err != nil {
		report.FailedStep = "write_initial"
		return finishSecretProbeCleanup(store, report)
	}
	value, err := store.Get(id)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			report.FailedStep = "read_initial_missing"
		} else {
			report.FailedStep = "read_initial_failed"
		}
		return finishSecretProbeCleanup(store, report)
	}
	if value != first {
		report.FailedStep = "read_initial_mismatch"
		return finishSecretProbeCleanup(store, report)
	}
	if err = store.Set(id, second); err != nil {
		report.FailedStep = "write_replace"
		return finishSecretProbeCleanup(store, report)
	}
	value, err = store.Get(id)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			report.FailedStep = "read_replace_missing"
		} else {
			report.FailedStep = "read_replace_failed"
		}
		return finishSecretProbeCleanup(store, report)
	}
	if value != second {
		report.FailedStep = "read_replace_mismatch"
		return finishSecretProbeCleanup(store, report)
	}
	report.Status, report.FailedStep = "passed", ""
	return finishSecretProbeCleanup(store, report)
}

func CleanupSecretProbe(store Secrets, id string) SecretProbeReport {
	report := SecretProbeReport{ProbeID: id, Status: "failed", FailedStep: "identifier", Cleanup: "unconfirmed"}
	if store == nil || ValidateSecretProbeID(id) != nil {
		return report
	}
	report.FailedStep = ""
	return finishSecretProbeCleanup(store, report)
}

func finishSecretProbeCleanup(store Secrets, report SecretProbeReport) SecretProbeReport {
	deleteErr := store.Delete(report.ProbeID)
	if deleteErr != nil {
		if report.FailedStep == "" {
			report.FailedStep = "cleanup_delete"
		}
		report.Status = "failed"
	}
	_, err := store.Get(report.ProbeID)
	if deleteErr == nil && errors.Is(err, ErrSecretNotFound) {
		report.Cleanup = "confirmed"
		return report
	}
	report.Cleanup = "unconfirmed"
	if report.FailedStep == "" {
		report.FailedStep = "cleanup_verify_failed"
	}
	report.Status = "failed"
	return report
}
