package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/jsontext"
	"io"
	"net/http"
	"reflect"
	"strings"
)

// Coding records add recursive exact spelling and duplicate/required-field
// checks before the existing strict typed decoder. No client record is authority.
func decodeCodingJSON(w http.ResponseWriter, r *http.Request, out any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if !jsontext.Lossless(raw) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = checkCodingJSON(d, reflect.TypeOf(out).Elem(), 0, false); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON(w, r, out)
}
func checkCodingJSON(d *json.Decoder, t reflect.Type, depth int, strict bool) error {
	bad := errors.New("invalid nested coding record")
	if depth > 64 {
		return bad
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	required := t == reflect.TypeOf(domain.C4ImportSelectionV2{}) || t == reflect.TypeOf(domain.C4DependencyBindingV2{}) || t == reflect.TypeOf(domain.C4VMIdentity{}) || t == reflect.TypeOf(engine.CodingDependencyImportRequest{})
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if required || strict {
			return bad
		}
		return nil
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		fields := map[string]reflect.StructField{}
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					fields[name] = f
				}
			}
		}
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			name, ok := k.(string)
			if e != nil || !ok || seen[name] {
				return bad
			}
			seen[name] = true
			child := reflect.TypeOf((*any)(nil)).Elem()
			if t.Kind() == reflect.Struct {
				f, ok := fields[name]
				if !ok {
					return bad
				}
				child = f.Type
			} else if t.Kind() == reflect.Map {
				child = t.Elem()
			}
			if err = checkCodingJSON(d, child, depth+1, strict || required); err != nil {
				return err
			}
		}
		if required {
			for name, f := range fields {
				if !strings.Contains(f.Tag.Get("json"), ",omitempty") && !seen[name] {
					return bad
				}
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return bad
		}
	case '[':
		child := reflect.TypeOf((*any)(nil)).Elem()
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			child = t.Elem()
		}
		for d.More() {
			if err = checkCodingJSON(d, child, depth+1, strict || required); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return bad
		}
	default:
		return bad
	}
	return nil
}
