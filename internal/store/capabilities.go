package store

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

// These helpers only access SQLite; capability registration never inspects files
// or contacts endpoints. Validation of protocol responses/imports is separate.
type capabilityReader interface{ QueryRow(string, ...any) *sql.Row }

func capabilityRead[T any](q capabilityReader, query string, args ...any) (T, error) {
	var value T
	var raw []byte
	err := q.QueryRow(query, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrNotFound
	}
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(raw, &value)
	return value, err
}

func capabilityList[T any](tx *sql.Tx, query string) ([]T, error) {
	result := make([]T, 0)
	rows, err := tx.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var value T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func capabilityWriteError(err error) error {
	if err != nil && isConstraint(err) {
		return ErrConflict
	}
	return err
}

func validCapabilityText(value string, max int, required bool) bool {
	return utf8.ValidString(value) && len(value) <= max && (!required || strings.TrimSpace(value) != "") && !strings.ContainsRune(value, 0)
}

func validCapabilityDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}

func validMCPEndpoint(endpoint string) bool {
	if !validCapabilityText(endpoint, 4096, true) {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") || u.Opaque != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func (s *Store) GetMCPConnection(id string) (domain.MCPConnection, error) {
	return capabilityRead[domain.MCPConnection](s.db, `SELECT data FROM mcp_connections WHERE id=?`, id)
}

func (s *Store) SaveMCPConnection(c domain.MCPConnection, expectedRevision int64) (domain.MCPConnection, error) {
	zero := domain.MCPConnection{}
	if !catalogID(c.ID) || expectedRevision < 0 || !validCapabilityText(c.Name, 200, true) || !validMCPEndpoint(c.Endpoint) || c.Protocol != domain.MCPProtocol || c.CredentialRevision < 0 || (c.CredentialRef != "" && !catalogID(c.CredentialRef)) || (c.CredentialRef != "" && c.CredentialRevision == 0) {
		return zero, domain.ErrInvalidCapability
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	old, err := capabilityRead[domain.MCPConnection](tx, `SELECT data FROM mcp_connections WHERE id=?`, c.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return zero, err
	}
	if old.Revision != expectedRevision {
		return zero, ErrConflict
	}
	if old.Revision > 0 {
		var active int
		if err = tx.QueryRow(`SELECT count(*) FROM discovery_operations WHERE connection_id=? AND status IN ('authorized_pending','dispatching')`, c.ID).Scan(&active); err != nil {
			return zero, err
		}
		if active > 0 {
			return zero, ErrConflict
		}
		// An endpoint change must never silently forward the old credential.
		if c.Endpoint != old.Endpoint && c.CredentialRef != "" && c.CredentialRef == old.CredentialRef {
			return zero, domain.ErrInvalidCapability
		}
		if c.CredentialRevision < old.CredentialRevision || (c.CredentialRef != old.CredentialRef && c.CredentialRevision <= old.CredentialRevision) {
			return zero, domain.ErrInvalidCapability
		}
	}
	c.Revision = expectedRevision + 1
	c.UpdatedAt = timestamp()
	c.CreatedAt = old.CreatedAt
	if c.CreatedAt == "" {
		c.CreatedAt = c.UpdatedAt
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return zero, err
	}
	if expectedRevision == 0 {
		_, err = tx.Exec(`INSERT INTO mcp_connections(id,revision,data) VALUES(?,?,?)`, c.ID, c.Revision, raw)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE mcp_connections SET revision=?,data=? WHERE id=? AND revision=?`, c.Revision, raw, c.ID, expectedRevision)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				err = ErrConflict
			}
		}
	}
	if err != nil {
		return zero, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return c, nil
}

func (s *Store) GetSkillRevision(id string, revision int64) (domain.SkillRevision, error) {
	return capabilityRead[domain.SkillRevision](s.db, `SELECT data FROM skill_revisions WHERE id=? AND revision=?`, id, revision)
}

func validSkillFiles(files []domain.SkillFile) bool {
	if len(files) < 1 || len(files) > 16 {
		return false
	}
	total := 0
	root := false
	seen := map[string]bool{}
	for _, file := range files {
		p := file.Path
		lower := strings.ToLower(p)
		if !utf8.ValidString(p) || p == "" || len(p) > 512 || path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, "\\") || strings.ContainsFunc(p, unicode.IsControl) || seen[lower] || !utf8.ValidString(file.Content) || strings.ContainsRune(file.Content, 0) || len(file.Content) > 64<<10 {
			return false
		}
		parts := strings.Split(p, "/")
		if len(parts) > 4 {
			return false
		}
		for _, part := range parts {
			if part == ".." || part == "." || part == "" {
				return false
			}
		}
		if len(parts) > 1 && parts[0] != "references" && parts[0] != "assets" {
			return false
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".md", ".txt", ".json", ".yaml", ".yml":
		default:
			return false
		}
		if p == "SKILL.md" {
			root = true
		}
		seen[lower] = true
		total += len(file.Content)
	}
	return root && total <= 256<<10
}

func (s *Store) SaveSkillRevision(v domain.SkillRevision, expectedRevision int64) (domain.SkillRevision, error) {
	zero := domain.SkillRevision{}
	if !catalogID(v.ID) || expectedRevision < 0 || !validCapabilityText(v.Name, 64, true) || !validCapabilityText(v.Description, 1024, true) || !validCapabilityText(v.Source, 2048, false) || !validCapabilityDigest(v.Digest) || !validSkillFiles(v.Files) {
		return zero, domain.ErrInvalidCapability
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	var current int64
	err = tx.QueryRow(`SELECT revision FROM skills WHERE id=?`, v.ID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	if current != expectedRevision {
		return zero, ErrConflict
	}
	v.Revision = current + 1
	v.CreatedAt = timestamp()
	raw, err := json.Marshal(v)
	if err != nil {
		return zero, err
	}
	if current == 0 {
		_, err = tx.Exec(`INSERT INTO skills(id,revision,name) VALUES(?,?,?)`, v.ID, v.Revision, v.Name)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE skills SET revision=?,name=? WHERE id=? AND revision=?`, v.Revision, v.Name, v.ID, current)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				err = ErrConflict
			}
		}
	}
	if err != nil {
		return zero, capabilityWriteError(err)
	}
	if _, err = tx.Exec(`INSERT INTO skill_revisions(id,revision,data) VALUES(?,?,?)`, v.ID, v.Revision, raw); err != nil {
		return zero, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return v, nil
}

func (s *Store) GetGitRepository(id string) (domain.GitRepository, error) {
	return capabilityRead[domain.GitRepository](s.db, `SELECT data FROM git_repositories WHERE id=?`, id)
}

func (s *Store) SaveGitRepository(v domain.GitRepository, expectedRevision int64) (domain.GitRepository, error) {
	zero := domain.GitRepository{}
	if !catalogID(v.ID) || expectedRevision < 0 || !validCapabilityText(v.Name, 200, true) || !validCapabilityText(v.Path, 4096, true) || !filepath.IsAbs(v.Path) || (v.ProjectID != "" && !catalogID(v.ProjectID)) {
		return zero, domain.ErrInvalidCapability
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	old, err := capabilityRead[domain.GitRepository](tx, `SELECT data FROM git_repositories WHERE id=?`, v.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return zero, err
	}
	if old.Revision != expectedRevision {
		return zero, ErrConflict
	}
	if v.ProjectID != "" {
		c, _, err := readCatalog(tx)
		if err != nil {
			return zero, err
		}
		found := false
		for _, p := range c.Projects {
			if p.ID == v.ProjectID {
				found = true
				break
			}
		}
		if !found {
			return zero, ErrNotFound
		}
	}
	v.Revision = expectedRevision + 1
	v.UpdatedAt = timestamp()
	v.CreatedAt = old.CreatedAt
	if v.CreatedAt == "" {
		v.CreatedAt = v.UpdatedAt
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return zero, err
	}
	if expectedRevision == 0 {
		_, err = tx.Exec(`INSERT INTO git_repositories(id,revision,data) VALUES(?,?,?)`, v.ID, v.Revision, raw)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE git_repositories SET revision=?,data=? WHERE id=? AND revision=?`, v.Revision, raw, v.ID, expectedRevision)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				err = ErrConflict
			}
		}
	}
	if err != nil {
		return zero, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return v, nil
}

func validGitError(code string) bool {
	switch code {
	case "invalid_repository", "unsupported_layout", "repository_unavailable", "inspection_failed", "timeout", "canceled", "git_unavailable":
		return true
	}
	return false
}

func (s *Store) SaveGitObservation(v domain.GitObservation) (domain.GitObservation, error) {
	zero := domain.GitObservation{}
	if !catalogID(v.ID) || !catalogID(v.RepositoryID) || v.RepositoryRevision < 1 || v.WorkingTreeStatus != "not_checked" || (v.Status != "succeeded" && v.Status != "failed") || (v.Status == "succeeded" && (v.ErrorCode != "" || !filepath.IsAbs(v.Root))) || (v.Status == "failed" && !validGitError(v.ErrorCode)) {
		return zero, domain.ErrInvalidCapability
	}
	if v.CheckedAt == "" {
		v.CheckedAt = timestamp()
	}
	if !validCatalogTime(v.CheckedAt) {
		return zero, domain.ErrInvalidCapability
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	repo, err := capabilityRead[domain.GitRepository](tx, `SELECT data FROM git_repositories WHERE id=?`, v.RepositoryID)
	if err != nil {
		return zero, err
	}
	if repo.Revision != v.RepositoryRevision {
		return zero, ErrConflict
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return zero, err
	}
	_, err = tx.Exec(`INSERT INTO git_observations(id,repository_id,repository_revision,data) VALUES(?,?,?,?)`, v.ID, v.RepositoryID, v.RepositoryRevision, raw)
	if err != nil {
		return zero, capabilityWriteError(err)
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return v, nil
}

func (s *Store) GetGitObservation(id string) (domain.GitObservation, error) {
	return capabilityRead[domain.GitObservation](s.db, `SELECT data FROM git_observations WHERE id=?`, id)
}

// Snapshots are last-known successes (which may belong to an old revision).
// Discovery attempts are independently ordered and bounded, never inferred from
// success snapshots. Consumers must compare config revisions before health labels.
func (s *Store) Capabilities() (domain.CapabilitySnapshot, error) {
	zero := domain.CapabilitySnapshot{}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	c := domain.CapabilitySnapshot{}
	if c.Connections, err = capabilityList[domain.MCPConnection](tx, `SELECT data FROM mcp_connections ORDER BY id`); err != nil {
		return zero, err
	}
	if c.Snapshots, err = capabilityList[domain.MCPToolSnapshot](tx, `SELECT s.data FROM mcp_tool_snapshots s WHERE s.rowid=(SELECT max(x.rowid) FROM mcp_tool_snapshots x JOIN discovery_operations o ON o.id=x.operation_id WHERE x.connection_id=s.connection_id AND o.status='succeeded') ORDER BY s.connection_id`); err != nil {
		return zero, err
	}
	c.Discoveries = make([]domain.DiscoveryOperation, 0)
	rows, err := tx.Query(discoverySelect + ` ORDER BY rowid DESC LIMIT 100`)
	if err != nil {
		return zero, err
	}
	for rows.Next() {
		op, scanErr := scanDiscovery(rows)
		if scanErr != nil {
			rows.Close()
			return zero, scanErr
		}
		c.Discoveries = append(c.Discoveries, op)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return zero, err
	}
	if c.Skills, err = capabilityList[domain.SkillRevision](tx, `SELECT data FROM skill_revisions ORDER BY id,revision DESC`); err != nil {
		return zero, err
	}
	if c.Repositories, err = capabilityList[domain.GitRepository](tx, `SELECT data FROM git_repositories ORDER BY id`); err != nil {
		return zero, err
	}
	if c.GitObservations, err = capabilityList[domain.GitObservation](tx, `SELECT data FROM git_observations ORDER BY rowid DESC LIMIT 100`); err != nil {
		return zero, err
	}
	c.BuiltinCapabilities = []domain.BuiltinCapability{
		{ID: "snapshot", Name: "Committed repository snapshot", Description: "Existing controller flow; explicit context approval required."},
		{ID: "candidate_patch", Name: "Candidate modification", Description: "Existing controller flow; approved plan and scoped candidate only."},
		{ID: "docker_verification", Name: "Docker verification", Description: "Existing controller flow; approved commands and immutable local image only."},
		{ID: "local_export", Name: "Local export", Description: "Existing controller flow; explicit delivery approval required."},
		{ID: "repo.list", Name: "Controlled repository list", Description: "Researcher only in an explicitly consented controlled-read task; lists the frozen approved manifest.", AgentCallable: true},
		{ID: "repo.read", Name: "Controlled repository read", Description: "Researcher only in an explicitly consented controlled-read task; reads the frozen approved manifest.", AgentCallable: true},
		{ID: "repo.search", Name: "Controlled repository search", Description: "Researcher only in an explicitly consented controlled-read task; searches the frozen approved manifest.", AgentCallable: true},
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return c, nil
}
