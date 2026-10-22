package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/engine"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const c4ConfigLimit = 16 << 10

var errC4Config = errors.New("invalid C4 control configuration")

// Only an explicitly supplied regular local file is read. No Docker path,
// socket, environment, context or provider discovery occurs in this parser.
func readC4Config(path string, at time.Time) (engine.C4Activation, error) {
	var c engine.C4Activation
	if !filepath.IsAbs(path) {
		return c, errC4Config
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return c, errC4Config
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > c4ConfigLimit {
		return c, errC4Config
	}
	raw, err := io.ReadAll(io.LimitReader(f, c4ConfigLimit+1))
	if err != nil || len(raw) > c4ConfigLimit {
		return c, errC4Config
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if strictC4Value(d, 0) != nil {
		return c, errC4Config
	}
	if _, err = d.Token(); err != io.EOF {
		return c, errC4Config
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || !exactC4Keys(top, []string{"schema_version", "enabled", "profile", "source_root", "base_commit", "base_digest", "expires_at"}) {
		return c, errC4Config
	}
	var profile map[string]json.RawMessage
	if json.Unmarshal(top["profile"], &profile) != nil || !exactC4Keys(profile, []string{"schema_version", "control_version", "id", "mode", "image_id", "platform", "docker_path", "endpoint", "verifier"}) {
		return c, errC4Config
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || engine.ValidateC4Activation(c, at) != nil || c.Profile.Mode != "docker" {
		return engine.C4Activation{}, errC4Config
	}
	return c, nil
}
func exactC4Keys(m map[string]json.RawMessage, keys []string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := m[key]; !ok {
			return false
		}
	}
	return true
}
func strictC4Value(d *json.Decoder, depth int) error {
	if depth > 8 {
		return errC4Config
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return errC4Config
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			s, ok := key.(string)
			if err != nil || !ok || seen[s] {
				return errC4Config
			}
			seen[s] = true
			if strictC4Value(d, depth+1) != nil {
				return errC4Config
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errC4Config
		}
		return nil
	}
	return errC4Config // Configuration contains no arrays.
}
