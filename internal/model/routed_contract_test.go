package model

import (
	"reflect"
	"testing"
)

func TestRoutedContractsCarryFrozenSelectionAndSafeOutcome(t *testing.T) {
	for _, pair := range []struct {
		value  any
		fields []string
	}{
		{Request{}, []string{"SnapshotSchema", "Selection"}},
		{Result{}, []string{"RequestID", "ResolvedModel", "CredentialGeneration"}},
	} {
		for _, name := range pair.fields {
			if _, ok := reflect.TypeOf(pair.value).FieldByName(name); !ok {
				t.Errorf("missing routed contract field %s", name)
			}
		}
	}
}
