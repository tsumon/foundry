package dependencyadmission

import (
	"bytes"
	"encoding/json"
	"strings"
)

type pythonRoot struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type pythonWheel struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type pythonLock struct {
	SchemaVersion  int           `json:"schema_version"`
	Python         string        `json:"python"`
	SelectedExtras []string      `json:"selected_extras"`
	Roots          []pythonRoot  `json:"roots"`
	Wheels         []pythonWheel `json:"wheels"`
}

func pythonName(s string) bool {
	if s == "" || len(s) > 256 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	previousDash := false
	for _, c := range s {
		if c == '-' {
			if previousDash {
				return false
			}
			previousDash = true
			continue
		}
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
		previousDash = false
	}
	return true
}
func parsePythonLock(raw []byte) (pythonLock, error) {
	var l pythonLock
	if strictJSON(raw, maxRootJSON) != nil {
		return l, errInvalid
	}
	m, e := object(bytes.TrimSpace(raw))
	if e != nil || !pythonExactKeys(m, "schema_version", "python", "selected_extras", "roots", "wheels") {
		return l, errInvalid
	}
	for _, key := range []string{"roots", "wheels"} {
		var entries []json.RawMessage
		if json.Unmarshal(m[key], &entries) != nil || entries == nil || len(entries) > maxPackages {
			return l, errInvalid
		}
		for _, entry := range entries {
			fields, e := object(bytes.TrimSpace(entry))
			if e != nil {
				return l, errInvalid
			}
			if key == "roots" && !pythonExactKeys(fields, "name", "version") || key == "wheels" && !pythonExactKeys(fields, "name", "version", "filename", "sha256", "size") {
				return l, errInvalid
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&l) != nil || l.SchemaVersion != 1 || l.Python != "3.13.5" || l.SelectedExtras == nil || len(l.SelectedExtras) != 0 || l.Roots == nil || l.Wheels == nil || len(l.Roots) > maxPackages || len(l.Wheels) > maxPackages {
		return pythonLock{}, errInvalid
	}
	for i, r := range l.Roots {
		if !pythonName(r.Name) || i > 0 && l.Roots[i-1].Name >= r.Name {
			return pythonLock{}, errInvalid
		}
		if _, e := pythonVersion(r.Version); e != nil {
			return pythonLock{}, errInvalid
		}
	}
	var total int64
	for i, w := range l.Wheels {
		if !pythonName(w.Name) || i > 0 && l.Wheels[i-1].Name >= w.Name || !validHash(w.SHA256) || w.Size <= 0 || w.Size > 64<<20 {
			return pythonLock{}, errInvalid
		}
		if _, e := pythonVersion(w.Version); e != nil {
			return pythonLock{}, errInvalid
		}
		if w.Filename != strings.ReplaceAll(w.Name, "-", "_")+"-"+w.Version+"-py3-none-any.whl" {
			return pythonLock{}, errInvalid
		}
		total += w.Size
		if total > 64<<20 {
			return pythonLock{}, errInvalid
		}
	}
	return l, nil
}

func pythonExactKeys(m map[string]json.RawMessage, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
