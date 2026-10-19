package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelCredentialJSONNeverExposesReference(t *testing.T) {
	b, err := json.Marshal(ModelCredentialVersion{AccountID: "account", Version: 1, OperationID: "operation", State: "available", SecretRef: "SYNTHETIC-PRIVATE-REFERENCE"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "REFERENCE") || strings.Contains(string(b), "secret_ref") {
		t.Fatalf("reference escaped: %s", b)
	}
}
