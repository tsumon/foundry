package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"foundry/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadDelivery returns the same bounded bytes whose hash was checked. Stored
// absolute paths never grant access outside the configured managed root.
func (e *Engine) ReadDelivery(id, kind string) ([]byte, error) {
	m, err := e.s.GetMission(id)
	if err != nil {
		return nil, err
	}
	if err = supportedMode(m.WorkspaceMode); err != nil {
		return nil, err
	}
	if m.Delivery == nil {
		return nil, errors.New("delivery artifact not found")
	}
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return nil, errors.New("invalid mission identity")
	}
	name, expected, limit := "changes.patch", m.Delivery.PatchSHA256, int64(24<<20)
	if kind == "report" {
		name, expected, limit = "report.md", m.Delivery.ReportSHA256, 1<<20
	} else if kind != "patch" {
		return nil, errors.New("unknown artifact")
	}
	relative := filepath.Join("missions", id, "delivery", name)
	if domain.UsesGenerations(m.WorkspaceMode) {
		if err = e.verifyDelivery(m); err != nil {
			return nil, err
		}
		relative = filepath.Join("missions", id, "exports", m.Delivery.OperationID, name)
	}
	dir := filepath.Dir(filepath.Join(e.options.DataDir, relative))
	if m.Delivery.PatchPath != filepath.Join(dir, "changes.patch") || m.Delivery.ReportPath != filepath.Join(dir, "report.md") {
		return nil, errors.New("delivery outside managed root")
	}
	root, err := os.OpenRoot(e.options.DataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	current := ""
	var info os.FileInfo
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err = root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("symlinked delivery")
		}
		if current != relative && !info.IsDir() {
			return nil, errors.New("invalid delivery directory")
		}
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid or oversized artifact")
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("artifact replaced during read")
	}
	b, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("artifact exceeds bounds")
	}
	if domain.UsesGenerations(m.WorkspaceMode) {
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != expected {
			return nil, errors.New("artifact digest changed")
		}
	}
	return b, nil
}
