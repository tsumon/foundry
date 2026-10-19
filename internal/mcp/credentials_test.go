package mcp

import (
	"errors"
	"fmt"
	"testing"
)

func TestResultRejectsLiteralPrimitiveCredentialReflection(t *testing.T) {
	for _, tc := range []struct{ name, credential, literal string }{
		{"integer", "123456", "123456"},
		{"large integer", "123456789012345678901234567890123456789", "123456789012345678901234567890123456789"},
		{"exponent beyond float64", "1e400", "1e400"},
		{"decimal lexeme", "0.123456789012345678901234567890", "0.123456789012345678901234567890"},
		{"true", "true", "true"},
		{"false", "false", "false"},
		{"null", "null", "null"},
		{"escaped string", "123456", `"\u0031\u0032\u0033\u0034\u0035\u0036"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &discovery{credential: tc.credential}
			raw := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"fixture","inputSchema":{"type":"object","properties":{"n":{"const":%s}}}}]}}`, tc.literal))
			result, _, err := d.message(raw, 2)
			var protocolError *Error
			if !errors.As(err, &protocolError) || protocolError.Code != "invalid_response" || result != nil {
				t.Fatalf("literal credential accepted: error=%v result_available=%v", err, result != nil)
			}
		})
	}
}

func TestPrimitiveSchemaWithoutReflectedCredentialRemainsAvailable(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"fixture","inputSchema":{"type":"object","properties":{"n":{"enum":[123456789012345678901234567890,1e400,true,false,null]}}}}]}}`)
	for _, credential := range []string{"", "unrelated-fake-secret"} {
		d := &discovery{credential: credential}
		result, notification, err := d.message(raw, 2)
		if err != nil || notification || result == nil {
			t.Fatalf("normal primitive schema rejected: %v", err)
		}
	}
}
