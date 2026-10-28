package dependencyadmission

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// Validate every object before decoding selected fields. JSON's default decoder
// silently accepts duplicate keys and case aliases; neither is evidence here.
func strictJSON(raw []byte, limit int) error {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return errInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errInvalid
		}
		t, e := d.Token()
		if e != nil {
			return errInvalid
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				s, ok := k.(string)
				if e != nil || !ok {
					return errInvalid
				}
				key := strings.ToLower(s)
				if keys[key] {
					return errInvalid
				}
				keys[key] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			t, e = d.Token()
			if e != nil || t != json.Delim('}') {
				return errInvalid
			}
		case '[':
			for d.More() {
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			t, e = d.Token()
			if e != nil || t != json.Delim(']') {
				return errInvalid
			}
		default:
			return errInvalid
		}
		return nil
	}
	if e := value(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errInvalid
	}
	return nil
}
func object(raw []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' {
		return nil, errInvalid
	}
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, errInvalid
	}
	return m, nil
}
func fieldString(m map[string]json.RawMessage, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil || bytes.Equal(v, []byte("null")) {
		return "", errInvalid
	}
	return s, nil
}
func dependencyMap(m map[string]json.RawMessage, key string) (map[string]string, error) {
	result := map[string]string{}
	raw, ok := m[key]
	if !ok {
		return result, nil
	}
	obj, e := object(bytes.TrimSpace(raw))
	if e != nil || len(obj) > maxEdges {
		return nil, errInvalid
	}
	for n, v := range obj {
		var spec string
		if !validName(n) || json.Unmarshal(v, &spec) != nil || bytes.Equal(v, []byte("null")) {
			return nil, errInvalid
		}
		result[n] = spec
	}
	return result, nil
}
func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if other, ok := b[k]; !ok || other != v {
			return false
		}
	}
	return true
}
