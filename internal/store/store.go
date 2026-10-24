package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"foundry/internal/domain"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrConflict = errors.New("revision conflict")
	ErrNotFound = errors.New("not found")
)

// IsPersistenceError recognizes typed storage failures only. Validation,
// authority and quota errors are not classified by their prose.
func IsPersistenceError(err error) bool {
	var sqliteError sqlite3.Error
	return errors.As(err, &sqliteError) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone)
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize() error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = FULL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS missions (
			id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL,
			state TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			data BLOB NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			mission_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			created_at TEXT NOT NULL,
			revision INTEGER NOT NULL,
			FOREIGN KEY (mission_id) REFERENCES missions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS events_mission_seq ON events(mission_id, seq)`,
		`CREATE TABLE IF NOT EXISTS profiles (
			id TEXT PRIMARY KEY,
			data BLOB NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			data BLOB NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize store: %w", err)
		}
	}
	if err := s.initializeReliability(); err != nil {
		return err
	}
	if err := s.initializeCatalog(); err != nil {
		return err
	}
	if err := s.initializeCapabilities(); err != nil {
		return err
	}
	if err := s.initializeInvocations(); err != nil {
		return err
	}
	if err := s.initializeMCPExecution(); err != nil {
		return err
	}
	if err := s.initializeResearchFlow(); err != nil {
		return err
	}
	if err := s.initializeHooks(); err != nil {
		return err
	}
	if err := s.initializeModelAccounts(); err != nil {
		return err
	}
	if err := s.initializeModelRoutes(); err != nil {
		return err
	}
	if err := s.initializeModelAuthorizations(); err != nil {
		return err
	}
	if err := s.initializeModelAcceptanceOperations(); err != nil {
		return err
	}
	if err := s.initializeRepositoryInspections(); err != nil {
		return err
	}
	if err := s.initializeRepositoryImports(); err != nil {
		return err
	}
	return s.initializeRolePolicy()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateMission(m domain.Mission) (domain.Mission, error) {
	return s.createMission(m, nil, "")
}
func (s *Store) createMission(m domain.Mission, importOperation *domain.RepositoryImportOperation, sourcePath string) (domain.Mission, error) {
	if err := domain.ValidateC4Mission(m); err != nil {
		return domain.Mission{}, err
	}
	if importOperation == nil && (m.ImportVersion != 0 || m.RepositoryImport != nil) {
		return domain.Mission{}, domain.ImportInvalidBinding
	}
	if err := domain.ValidateCodingTrace(m); err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return domain.Mission{}, err
	}
	if strings.TrimSpace(m.ID) == "" {
		return domain.Mission{}, errors.New("mission ID is required")
	}
	now := timestamp()
	m.Revision = 1
	if m.CreatedAt == "" {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if err := domain.ValidateC4Mission(m); err != nil {
		return domain.Mission{}, err
	}
	// Creation may prepare baseline material, but cannot fabricate historical
	// approval/dispatch facts or an already authorized candidate operation.
	c4Initial := m
	c4Initial.C4Operations = nil
	c4Initial.C4Controls = nil
	c4Initial.Approvals = nil
	if err := domain.ValidateC4Transition(c4Initial, m); err != nil {
		return domain.Mission{}, err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("encode mission: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Mission{}, fmt.Errorf("begin create mission: %w", err)
	}
	defer tx.Rollback()
	if importOperation != nil {
		if err := commitRepositoryImportTx(tx, m, *importOperation, sourcePath); err != nil {
			return domain.Mission{}, err
		}
	} else if err := validateMissionImport(context.Background(), tx, m); err != nil {
		return domain.Mission{}, err
	}
	if err := validateNewModelSnapshotTx(tx, m); err != nil {
		return domain.Mission{}, err
	}
	_, err = tx.Exec(`INSERT INTO missions(id, revision, state, updated_at, data) VALUES(?,?,?,?,?)`,
		m.ID, m.Revision, m.State, m.UpdatedAt, data)
	if err != nil {
		if isConstraint(err) {
			return domain.Mission{}, ErrConflict
		}
		return domain.Mission{}, fmt.Errorf("insert mission: %w", err)
	}
	if err := insertEvent(tx, m.ID, "mission.created", m.Revision, now); err != nil {
		return domain.Mission{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Mission{}, fmt.Errorf("commit create mission: %w", err)
	}
	return m, nil
}

func (s *Store) GetMission(id string) (domain.Mission, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM missions WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Mission{}, ErrNotFound
	}
	if err != nil {
		return domain.Mission{}, fmt.Errorf("get mission: %w", err)
	}
	return decodeMission(data)
}

func (s *Store) ListMissions() ([]domain.Mission, error) {
	rows, err := s.db.Query(`SELECT data FROM missions ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list missions: %w", err)
	}
	defer rows.Close()
	missions := make([]domain.Mission, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan mission: %w", err)
		}
		mission, err := decodeMission(data)
		if err != nil {
			return nil, err
		}
		missions = append(missions, mission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list missions: %w", err)
	}
	return missions, nil
}

func (s *Store) UpdateMission(id string, expected int64, kind string, mutate func(*domain.Mission) error) (domain.Mission, error) {
	if mutate == nil {
		return domain.Mission{}, errors.New("mission mutation is required")
	}
	if strings.TrimSpace(kind) == "" {
		return domain.Mission{}, errors.New("event kind is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Mission{}, fmt.Errorf("begin update mission: %w", err)
	}
	defer tx.Rollback()
	m, err := updateMissionTx(tx, id, expected, kind, func(m *domain.Mission) error {
		usedCalls := m.Budget.UsedCalls
		state := m.State
		execute, _ := json.Marshal(executionApprovals(*m))
		ready := domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) && m.State == "planning" && m.Stage == "mcp_observation_ready"
		if err := mutate(m); err != nil {
			return err
		}
		if m.ModelSnapshot != nil && m.Budget.UsedCalls != usedCalls {
			return errors.New("account model budget requires role-aware reservation")
		}
		if m.ModelSnapshot != nil {
			nextExecute, _ := json.Marshal(executionApprovals(*m))
			if !bytes.Equal(execute, nextExecute) || state != "running" && m.State == "running" {
				return errors.New("account execution authority requires atomic approval")
			}
		}
		if ready && m.State == "planning" && m.Stage != "mcp_observation_ready" {
			return errors.New("observation continuation requires atomic consumer reservation")
		}
		return nil
	})
	if err != nil {
		return domain.Mission{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Mission{}, fmt.Errorf("commit mission update: %w", err)
	}
	return m, nil
}

// updateMissionTx is shared by operations and the revision ledger. Callbacks
// must be pure state mutations: filesystem work cannot participate in SQLite.
func updateMissionTx(tx *sql.Tx, id string, expected int64, kind string, mutate func(*domain.Mission) error) (domain.Mission, error) {
	if mutate == nil || strings.TrimSpace(kind) == "" {
		return domain.Mission{}, errors.New("mutation and event kind are required")
	}
	var revision int64
	var data []byte
	err := tx.QueryRow(`SELECT revision, data FROM missions WHERE id = ?`, id).Scan(&revision, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Mission{}, ErrNotFound
	}
	if err != nil {
		return domain.Mission{}, fmt.Errorf("read mission for update: %w", err)
	}
	if revision != expected {
		return domain.Mission{}, ErrConflict
	}
	m, err := decodeMission(data)
	if err != nil {
		return domain.Mission{}, err
	}
	// Decode a second, fully detached snapshot. A shallow copy would alias
	// profile, generation, stage, result and cleanup data inside the callback.
	c4Before, err := decodeMission(data)
	if err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateC4Mission(c4Before); err != nil {
		return domain.Mission{}, err
	}
	if err := validateMissionImport(context.Background(), tx, m); err != nil {
		return domain.Mission{}, err
	}
	importVersion := m.ImportVersion
	importBinding, _ := json.Marshal(m.RepositoryImport)
	mode := m.WorkspaceMode
	codingBefore := m
	codingBefore.CodingAttempts = nil
	traceBytes, err := json.Marshal(m.CodingAttempts)
	if err != nil {
		return domain.Mission{}, err
	}
	if err := json.Unmarshal(traceBytes, &codingBefore.CodingAttempts); err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateCodingTrace(codingBefore); err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateReplanContext(m); err != nil {
		return domain.Mission{}, err
	}
	if err := validateRecoveredSelections(tx, m); err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return domain.Mission{}, err
	}
	modelSnapshot, _ := json.Marshal(m.ModelSnapshot)
	modelPolicySnapshot, _ := json.Marshal(m.ModelPolicySnapshot)
	toolMode := m.ToolMode
	bindings, err := json.Marshal(m.TaskBindings)
	if err != nil {
		return domain.Mission{}, err
	}
	mcpBindings, err := json.Marshal(m.MCPBindings)
	if err != nil {
		return domain.Mission{}, err
	}
	archiveCount := len(m.RunArchives)
	archivePrefix, err := json.Marshal(m.RunArchives)
	if err != nil {
		return domain.Mission{}, err
	}
	if err := mutate(&m); err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateC4Transition(c4Before, m); err != nil {
		return domain.Mission{}, err
	}
	updatedImportBinding, _ := json.Marshal(m.RepositoryImport)
	if m.ImportVersion != importVersion || !bytes.Equal(importBinding, updatedImportBinding) {
		return domain.Mission{}, domain.ImportInvalidBinding
	}
	if err := validateMissionImport(context.Background(), tx, m); err != nil {
		return domain.Mission{}, err
	}
	if m.WorkspaceMode != mode {
		return domain.Mission{}, errors.New("workspace mode is immutable")
	}
	if err := domain.ValidateCodingTransition(codingBefore, m); err != nil {
		return domain.Mission{}, err
	}
	if err := domain.ValidateReplanContext(m); err != nil {
		return domain.Mission{}, err
	}
	if err := validateRecoveredSelections(tx, m); err != nil {
		return domain.Mission{}, err
	}
	updatedModelSnapshot, _ := json.Marshal(m.ModelSnapshot)
	if !bytes.Equal(modelSnapshot, updatedModelSnapshot) {
		return domain.Mission{}, errors.New("model snapshot is immutable")
	}
	updatedModelPolicySnapshot, _ := json.Marshal(m.ModelPolicySnapshot)
	if !bytes.Equal(modelPolicySnapshot, updatedModelPolicySnapshot) {
		return domain.Mission{}, errors.New("model policy snapshot is immutable")
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return domain.Mission{}, err
	}
	updatedBindings, err := json.Marshal(m.TaskBindings)
	if err != nil {
		return domain.Mission{}, err
	}
	if m.ToolMode != toolMode || !bytes.Equal(bindings, updatedBindings) {
		return domain.Mission{}, errors.New("tool mode and task bindings are immutable")
	}
	updatedMCPBindings, err := json.Marshal(m.MCPBindings)
	if err != nil || !bytes.Equal(mcpBindings, updatedMCPBindings) {
		return domain.Mission{}, errors.New("MCP bindings are immutable")
	}
	if len(m.RunArchives) < archiveCount {
		return domain.Mission{}, errors.New("run archives are append-only")
	}
	if archiveCount > 0 {
		prefix, err := json.Marshal(m.RunArchives[:archiveCount])
		if err != nil || !bytes.Equal(prefix, archivePrefix) {
			return domain.Mission{}, errors.New("run archives are immutable")
		}
	}
	seenArchives := map[[2]int64]bool{}
	for _, archive := range m.RunArchives {
		key := [2]int64{archive.Epoch, archive.PlanRevision}
		if seenArchives[key] {
			return domain.Mission{}, errors.New("duplicate run archive epoch/plan revision")
		}
		seenArchives[key] = true
	}
	if m.ID != id {
		return domain.Mission{}, errors.New("mission ID cannot be changed")
	}
	m.Revision = revision + 1
	m.UpdatedAt = timestamp()
	data, err = json.Marshal(m)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("encode mission: %w", err)
	}
	result, err := tx.Exec(`UPDATE missions SET revision = ?, state = ?, updated_at = ?, data = ? WHERE id = ? AND revision = ?`,
		m.Revision, m.State, m.UpdatedAt, data, id, expected)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("update mission: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return domain.Mission{}, fmt.Errorf("check mission update: %w", err)
	}
	if changed != 1 {
		return domain.Mission{}, ErrConflict
	}
	if err := insertEvent(tx, id, kind, m.Revision, m.UpdatedAt); err != nil {
		return domain.Mission{}, err
	}
	return m, nil
}

func (s *Store) Events(after int64, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT seq, mission_id, kind, created_at, revision FROM events WHERE seq > ? ORDER BY seq ASC LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.Event, 0)
	for rows.Next() {
		var event domain.Event
		if err := rows.Scan(&event.Seq, &event.MissionID, &event.Kind, &event.CreatedAt, &event.Revision); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

func (s *Store) PutProfile(p domain.Profile) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("profile ID is required")
	}
	if _, _, err := domain.ParseProviderBaseURL(p.BaseURL); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode profile: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.CatalogOperationID != "" {
		if _, err = catalogSelectionTx(tx, p, time.Now()); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO profiles(id, data) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, p.ID, data)
	if err != nil {
		return fmt.Errorf("put profile: %w", err)
	}
	return tx.Commit()
}

func (s *Store) GetProfile(id string) (domain.Profile, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM profiles WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Profile{}, ErrNotFound
	}
	if err != nil {
		return domain.Profile{}, fmt.Errorf("get profile: %w", err)
	}
	var profile domain.Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return domain.Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	return profile, nil
}

func (s *Store) ListProfiles() ([]domain.Profile, error) {
	rows, err := s.db.Query(`SELECT data FROM profiles ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]domain.Profile, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		var profile domain.Profile
		if err := json.Unmarshal(data, &profile); err != nil {
			return nil, fmt.Errorf("decode profile: %w", err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	return profiles, nil
}

func (s *Store) GetSettings() (domain.Settings, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM settings WHERE id = 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Settings{RoleBindings: map[domain.Role]string{}}, nil
	}
	if err != nil {
		return domain.Settings{}, fmt.Errorf("get settings: %w", err)
	}
	var settings domain.Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return domain.Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	if settings.RoleBindings == nil {
		settings.RoleBindings = map[domain.Role]string{}
	}
	return settings, nil
}

func (s *Store) PutSettings(settings domain.Settings) error {
	if settings.RoleBindings == nil {
		settings.RoleBindings = map[domain.Role]string{}
	}
	// Generic/manual settings writes cannot claim policy provenance. Only the
	// atomic confirmation transaction may set this marker.
	settings.ModelPolicyDecisionID = ""
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO settings(id, data) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, data)
	if err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	return nil
}

func decodeMission(data []byte) (domain.Mission, error) {
	var mission domain.Mission
	// Inspect the original envelope before a last-wins projection can erase an
	// earlier control declaration. Valid legacy records remain unchanged.
	mission.C4Invalid = invalidC4StoredEnvelope(data)
	// Isolate snapshot type errors without relaxing decoding of the legacy
	// mission body. Get/List remain inspectable; every mutation validates the
	// diagnostic and leaves the original stored bytes untouched.
	projection := struct {
		*domain.Mission
		Spec                json.RawMessage `json:"spec"`
		C4Operations        json.RawMessage `json:"c4_operations"`
		C4Controls          json.RawMessage `json:"c4_controls"`
		RepositoryImport    json.RawMessage `json:"repository_import"`
		ImportVersion       json.RawMessage `json:"import_version"`
		CodingAttempts      json.RawMessage `json:"coding_attempts"`
		ModelSnapshot       json.RawMessage `json:"model_snapshot"`
		ModelPolicySnapshot json.RawMessage `json:"model_policy_snapshot"`
	}{Mission: &mission}
	if err := json.Unmarshal(data, &projection); err != nil {
		return domain.Mission{}, fmt.Errorf("decode mission: %w", err)
	}
	if len(projection.Spec) > 0 {
		spec := struct {
			*domain.Spec
			C4 json.RawMessage `json:"c4"`
		}{Spec: &mission.Spec}
		if err := json.Unmarshal(projection.Spec, &spec); err != nil {
			return domain.Mission{}, fmt.Errorf("decode mission spec: %w", err)
		}
		if len(spec.C4) > 0 {
			var profile domain.C4Profile
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(spec.C4, &fields)
			version, hasVersion := fields["control_version"]
			if string(spec.C4) == "null" || (hasVersion && string(bytes.TrimSpace(version)) != "1") || decodeStoredJSON(spec.C4, &profile) != nil {
				mission.C4Invalid = true
			} else {
				mission.Spec.C4 = &profile
			}
		}
	}
	if len(projection.C4Operations) > 0 {
		if decodeStoredJSON(projection.C4Operations, &mission.C4Operations) != nil {
			mission.C4Invalid = true
		}
	}
	if len(projection.C4Controls) > 0 {
		if bytes.Equal(bytes.TrimSpace(projection.C4Controls), []byte("null")) || decodeStoredJSON(projection.C4Controls, &mission.C4Controls) != nil || mission.Spec.C4 == nil || mission.Spec.C4.ControlVersion != 1 {
			mission.C4Invalid = true
		}
	}
	if len(projection.ImportVersion) > 0 {
		if err := json.Unmarshal(projection.ImportVersion, &mission.ImportVersion); err != nil {
			mission.RepositoryImportInvalid = true
		}
	}
	if len(projection.RepositoryImport) > 0 && string(projection.RepositoryImport) != "null" {
		var binding domain.RepositoryImportBinding
		if decodeStoredJSON(projection.RepositoryImport, &binding) != nil {
			mission.RepositoryImportInvalid = true
		} else {
			mission.RepositoryImport = &binding
		}
	}
	if len(projection.CodingAttempts) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(projection.CodingAttempts))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&mission.CodingAttempts); err != nil {
			mission.CodingTraceInvalid = true
		}
	}
	if len(projection.ModelSnapshot) > 0 {
		if err := json.Unmarshal(projection.ModelSnapshot, &mission.ModelSnapshot); err != nil {
			mission.ModelSnapshot = nil
			mission.ModelSnapshotInvalid = true
		}
	}
	if len(projection.ModelPolicySnapshot) > 0 {
		if err := json.Unmarshal(projection.ModelPolicySnapshot, &mission.ModelPolicySnapshot); err != nil {
			mission.ModelPolicySnapshot = nil
			mission.ModelPolicySnapshotInvalid = true
		}
	}
	return mission, nil
}

func invalidC4StoredEnvelope(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := storedJSONValue(decoder)
	root, ok := value.(map[string]any)
	if err != nil || !ok {
		return true
	}
	for key := range root {
		for _, exact := range []string{"spec", "c4_controls", "c4_operations"} {
			if strings.EqualFold(key, exact) && key != exact {
				return true
			}
		}
	}
	if spec, ok := root["spec"].(map[string]any); ok {
		for key := range spec {
			if strings.EqualFold(key, "c4") && key != "c4" {
				return true
			}
		}
	}
	return false
}

func insertEvent(tx *sql.Tx, missionID, kind string, revision int64, createdAt string) error {
	_, err := tx.Exec(`INSERT INTO events(mission_id, kind, created_at, revision) VALUES(?,?,?,?)`, missionID, kind, createdAt, revision)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func isConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "constraint")
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
