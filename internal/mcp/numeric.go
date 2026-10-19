package mcp

import (
	"encoding/json"
	"errors"
)

const (
	maxExecutionNumberBytes = 256
	maxExecutionExponent    = 1024
)

var errUnsupportedExecutionNumber = errors.New("unsupported execution number")

// validateExecutionNumbers bounds the exact JSON number lexemes accepted by
// execution validation. Discovery canonicalization intentionally remains
// lossless and does not apply this smaller execution subset.
func validateExecutionNumbers(value any) error {
	stack := []any{value}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		switch node := current.(type) {
		case nil, bool, string:
		case json.Number:
			if !validExecutionNumberLexeme(node.String()) {
				return errUnsupportedExecutionNumber
			}
		case []any:
			stack = append(stack, node...)
		case map[string]any:
			for _, child := range node {
				stack = append(stack, child)
			}
		default:
			return errUnsupportedExecutionNumber
		}
	}
	return nil
}

func validExecutionNumberLexeme(literal string) bool {
	if len(literal) == 0 || len(literal) > maxExecutionNumberBytes {
		return false
	}
	i := 0
	if literal[i] == '-' {
		i++
		if i == len(literal) {
			return false
		}
	}
	if literal[i] == '0' {
		i++
		if i < len(literal) && literal[i] >= '0' && literal[i] <= '9' {
			return false
		}
	} else {
		if literal[i] < '1' || literal[i] > '9' {
			return false
		}
		for i < len(literal) && literal[i] >= '0' && literal[i] <= '9' {
			i++
		}
	}
	if i < len(literal) && literal[i] == '.' {
		i++
		start := i
		for i < len(literal) && literal[i] >= '0' && literal[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(literal) && (literal[i] == 'e' || literal[i] == 'E') {
		i++
		if i < len(literal) && (literal[i] == '+' || literal[i] == '-') {
			i++
		}
		start := i
		exponent := 0
		for i < len(literal) && literal[i] >= '0' && literal[i] <= '9' {
			exponent = exponent*10 + int(literal[i]-'0')
			if exponent > maxExecutionExponent {
				return false
			}
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(literal)
}
