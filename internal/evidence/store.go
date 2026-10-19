package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"foundry/internal/domain"
)

const (
	maxModelEvidenceBytes      = 2 << 20
	maxRepositoryEvidenceBytes = 64 << 10
)

// Store seals evidence beneath a controller-owned root. Envelope data never
// supplies a filesystem path; the lowercase SHA-256 digest is the only key.
type Store struct {
	root          string
	rootErr       error
	beforePublish func() error
	syncDir       func(string) error
}

func New(root string) *Store {
	abs, err := filepath.Abs(root)
	if err == nil && strings.TrimSpace(root) == "" {
		err = errors.New("evidence root is required")
	}
	return &Store{root: filepath.Clean(abs), rootErr: err, syncDir: syncDirectory}
}

func (s *Store) Put(envelope domain.EvidenceEnvelope, body []byte) (domain.EvidenceEnvelope, error) {
	if err := s.prepareRoot(); err != nil {
		return domain.EvidenceEnvelope{}, err
	}
	if !utf8.Valid(body) || containsNUL(body) {
		return domain.EvidenceEnvelope{}, errors.New("evidence body must be UTF-8 text without NUL")
	}
	if limit, err := evidenceLimit(envelope.Kind); err != nil {
		return domain.EvidenceEnvelope{}, err
	} else if len(body) > limit {
		return domain.EvidenceEnvelope{}, errors.New("evidence body exceeds kind limit")
	}
	if envelope.ContentType == "application/json" && !json.Valid(body) {
		return domain.EvidenceEnvelope{}, errors.New("JSON evidence body is invalid")
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if envelope.Digest != "" && envelope.Digest != digest {
		return domain.EvidenceEnvelope{}, errors.New("evidence digest does not match body")
	}
	if envelope.Bytes != 0 && envelope.Bytes != int64(len(body)) {
		return domain.EvidenceEnvelope{}, errors.New("evidence length does not match body")
	}
	envelope.Digest = digest
	envelope.Bytes = int64(len(body))
	if envelope.SchemaVersion == 0 {
		envelope.SchemaVersion = 1
	}
	if envelope.CapturedAt == "" {
		envelope.CapturedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := validateEnvelope(envelope); err != nil {
		return domain.EvidenceEnvelope{}, err
	}

	final := s.blobPath(envelope.Digest)
	staging, err := s.secureSubdirectory("staging")
	if err != nil {
		return domain.EvidenceEnvelope{}, err
	}
	finalDir, err := s.secureSubdirectory("blobs", envelope.Digest[:2])
	if err != nil {
		return domain.EvidenceEnvelope{}, err
	}
	if info, err := os.Lstat(final); err == nil {
		if !info.Mode().IsRegular() {
			return domain.EvidenceEnvelope{}, errors.New("evidence destination is not a regular file")
		}
		if err := s.synchronizeDirectory(finalDir); err != nil {
			return domain.EvidenceEnvelope{}, err
		}
		if _, err := s.Read(envelope); err != nil {
			return domain.EvidenceEnvelope{}, err
		}
		return envelope, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return domain.EvidenceEnvelope{}, fmt.Errorf("inspect evidence destination: %w", err)
	}

	temp, err := os.CreateTemp(staging, "evidence-*")
	if err != nil {
		return domain.EvidenceEnvelope{}, fmt.Errorf("create evidence staging file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(body)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return domain.EvidenceEnvelope{}, fmt.Errorf("stage evidence: %w", err)
	}
	if s.beforePublish != nil {
		if err := s.beforePublish(); err != nil {
			return domain.EvidenceEnvelope{}, err
		}
	}
	// Link is an atomic no-replace publication on the same controller-owned
	// filesystem. A racing existing digest must verify byte-for-byte.
	if err = os.Link(tempName, final); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return domain.EvidenceEnvelope{}, fmt.Errorf("publish evidence: %w", err)
		}
	}
	if err := s.synchronizeDirectory(finalDir); err != nil {
		return domain.EvidenceEnvelope{}, err
	}
	if _, err := s.Read(envelope); err != nil {
		return domain.EvidenceEnvelope{}, fmt.Errorf("verify published evidence: %w", err)
	}
	return envelope, nil
}

func (s *Store) Read(envelope domain.EvidenceEnvelope) ([]byte, error) {
	if err := s.prepareRoot(); err != nil {
		return nil, err
	}
	if err := validateEnvelope(envelope); err != nil {
		return nil, err
	}
	limit, _ := evidenceLimit(envelope.Kind)
	if envelope.Bytes > int64(limit) {
		return nil, errors.New("evidence envelope exceeds kind limit")
	}
	if err := s.checkSubdirectory("blobs", envelope.Digest[:2]); err != nil {
		return nil, err
	}
	path := s.blobPath(envelope.Digest)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect evidence blob: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("evidence blob is not a regular file")
	}
	if info.Size() != envelope.Bytes {
		return nil, errors.New("evidence blob length mismatch")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open evidence blob: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read evidence blob: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close evidence blob: %w", closeErr)
	}
	if len(body) > limit || int64(len(body)) != envelope.Bytes || !utf8.Valid(body) || containsNUL(body) {
		return nil, errors.New("evidence blob failed size or UTF-8 verification")
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != envelope.Digest {
		return nil, errors.New("evidence blob digest mismatch")
	}
	if envelope.ContentType == "application/json" && !json.Valid(body) {
		return nil, errors.New("evidence JSON failed verification")
	}
	return body, nil
}

func (s *Store) prepareRoot() error {
	if s == nil || s.rootErr != nil {
		if s == nil {
			return errors.New("evidence store is nil")
		}
		return s.rootErr
	}
	if err := secureMkdir(s.root); err != nil {
		return err
	}
	return s.synchronizeDirectory(filepath.Dir(s.root))
}

func (s *Store) blobPath(digest string) string {
	prefix := "invalid"
	if len(digest) >= 2 {
		prefix = digest[:2]
	}
	return filepath.Join(s.root, "blobs", prefix, digest)
}

func (s *Store) secureSubdirectory(parts ...string) (string, error) {
	current := s.root
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || filepath.Base(part) != part {
			return "", errors.New("invalid evidence directory component")
		}
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create evidence directory: %w", err)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("inspect evidence directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("evidence directory path contains a symlink or non-directory")
		}
		if err := s.synchronizeDirectory(filepath.Dir(current)); err != nil {
			return "", err
		}
	}
	return current, nil
}

func (s *Store) synchronizeDirectory(path string) error {
	if s.syncDir == nil {
		return syncDirectory(path)
	}
	return s.syncDir(path)
}

func (s *Store) checkSubdirectory(parts ...string) error {
	current := s.root
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect evidence directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("evidence directory path contains a symlink or non-directory")
		}
	}
	return nil
}

func validateEnvelope(envelope domain.EvidenceEnvelope) error {
	if envelope.Kind == "mcp_result" && (envelope.Truncated || envelope.ContentType != "application/json") {
		return errors.New("MCP result must be complete JSON")
	}
	if envelope.SchemaVersion != 1 || !safeText(envelope.InvocationID, 256) || !safeText(envelope.MissionID, 256) || envelope.Epoch < 1 || !safeText(envelope.Producer, 256) || !validTime(envelope.CapturedAt) || !lowerHex(envelope.Digest, 64) || envelope.Bytes < 0 || !lowerHex(envelope.BindingDigest, 64) {
		return errors.New("invalid evidence envelope")
	}
	if _, err := evidenceLimit(envelope.Kind); err != nil {
		return err
	}
	if envelope.ContentType != "application/json" && envelope.ContentType != "text/plain" && envelope.ContentType != "text/plain; charset=utf-8" {
		return errors.New("unsupported evidence content type")
	}
	return nil
}

func evidenceLimit(kind string) (int, error) {
	switch kind {
	case "model_input", "model_output":
		return maxModelEvidenceBytes, nil
	case "tool_observation", "mcp_result":
		return maxRepositoryEvidenceBytes, nil
	default:
		return 0, errors.New("unsupported evidence kind")
	}
}

func secureMkdir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect evidence directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("evidence directory is not a real directory")
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open evidence directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync evidence directory: %w", err)
	}
	return nil
}

func validTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func containsNUL(value []byte) bool {
	for _, b := range value {
		if b == 0 {
			return true
		}
	}
	return false
}
