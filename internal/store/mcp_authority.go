package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
	"foundry/internal/mcp"
)

var ErrMCPBudget = errors.New("shared MCP/repository budget exhausted")
var ErrMCPFutureVersion = errors.New("unsupported MCP ledger version")

// CheckMCPLedgerVersion checks every SQL-owned MCP ledger, including decision
// and transition history. Recovery callers must leave the mission untouched
// when this returns ErrMCPFutureVersion; infrastructure errors propagate.
func (s *Store) CheckMCPLedgerVersion(missionID string) error {
	return supportedMCPLedger(s.db, missionID)
}

func mcpHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func isMCPMission(m domain.Mission) bool {
	return domain.IsControlledMCPRead(m.WorkspaceMode, m.ToolMode) && m.TaskBindings != nil && m.MCPBindings != nil
}

func supportedMCPLedger(q rowQuerier, missionID string) error {
	for _, table := range []string{"mcp_proposal_sources", "mcp_call_proposals", "mcp_call_grants", "mcp_call_decisions", "mcp_invocations", "mcp_invocation_transitions"} {
		var future bool
		if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE mission_id=? AND version!=1)`, missionID).Scan(&future); err != nil {
			return err
		}
		if future {
			return ErrMCPFutureVersion
		}
	}
	for _, table := range researchTables {
		var future bool
		if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE mission_id=? AND version!=2)`, missionID).Scan(&future); err != nil {
			return err
		}
		if future {
			return ErrMCPFutureVersion
		}
	}
	return nil
}

// SQL ownership/version and a checksum are independent of the untrusted JSON.
// Table/column identifiers here are internal constants, never user inputs.
func readMCPRecord[T any](q rowQuerier, table, idColumn, id, missionID string) (T, error) {
	var result T
	var trustedID, owner, session, digest, link string
	var version int
	var epoch int64
	var raw []byte
	linkColumn := "''"
	switch table {
	case "mcp_call_proposals":
		linkColumn = "source_invocation_id"
	case "mcp_call_grants":
		linkColumn = "proposal_id"
	}
	err := q.QueryRow(`SELECT `+idColumn+`,mission_id,version,epoch,session_id,data,digest,`+linkColumn+` FROM `+table+` WHERE `+idColumn+`=? AND mission_id=?`, id, missionID).Scan(&trustedID, &owner, &version, &epoch, &session, &raw, &digest, &link)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if version != 1 {
		return result, ErrMCPFutureVersion
	}
	if mcpHash(raw) != digest || decodeStoredJSON(raw, &result) != nil {
		return result, errors.New("corrupt MCP record")
	}
	var header map[string]json.RawMessage
	if json.Unmarshal(raw, &header) != nil {
		return result, errors.New("corrupt MCP record header")
	}
	var embeddedID, embeddedOwner, embeddedSession string
	var embeddedVersion int
	var embeddedEpoch int64
	if json.Unmarshal(header[idColumn], &embeddedID) != nil || json.Unmarshal(header["mission_id"], &embeddedOwner) != nil || json.Unmarshal(header["version"], &embeddedVersion) != nil || json.Unmarshal(header["epoch"], &embeddedEpoch) != nil || json.Unmarshal(header["session_id"], &embeddedSession) != nil || embeddedID != trustedID || embeddedOwner != owner || embeddedVersion != version || embeddedEpoch != epoch || embeddedSession != session {
		return result, errors.New("MCP JSON differs from SQL authority")
	}
	if linkColumn != "''" {
		var embeddedLink string
		if json.Unmarshal(header[linkColumn], &embeddedLink) != nil || embeddedLink != link {
			return result, errors.New("MCP linkage differs from SQL authority")
		}
	}
	return result, nil
}

func mcpMissionAuthority(m domain.Mission, binding, authority string) error {
	return mcpRoleAuthority(m, binding, authority, domain.Researcher)
}

func mcpRoleAuthority(m domain.Mission, binding, authority string, role domain.Role) error {
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	if !isMCPMission(m) {
		return ErrConflict
	}
	actual, err := domain.MCPBindingDigest(*m.TaskBindings, *m.MCPBindings)
	if err != nil {
		return err
	}
	if actual != binding {
		return ErrConflict
	}
	kind := "context"
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		switch role {
		case domain.Researcher, domain.Planner, domain.Main:
		case domain.Worker, domain.Debugger, domain.QA, domain.Auditor:
			kind = "execute"
		default:
			return ErrConflict
		}
	}
	for i := len(m.Approvals) - 1; i >= 0; i-- {
		approval := m.Approvals[i]
		if approval.Kind != kind {
			continue
		}
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) && kind == "execute" && (m.Plan == nil || approval.PlanRevision != m.Plan.Revision) {
			return ErrConflict
		}
		if approval.Digest == authority && lowerHex(authority, 64) {
			return nil
		}
		// V2 only accepts the latest authorization of the required kind.
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
			return ErrConflict
		}
	}
	return ErrConflict
}

// This checks immutable local records, not sealed file bytes or live server
// behavior. The Engine verifies seal bytes and recomputes context authority.
func currentMCPTool(q rowQuerier, m domain.Mission, name string) (domain.MCPTool, domain.MCPToolBinding, error) {
	zeroTool, zeroBinding := domain.MCPTool{}, domain.MCPToolBinding{}
	if !isMCPMission(m) {
		return zeroTool, zeroBinding, ErrConflict
	}
	b := *m.MCPBindings
	var connectionID string
	var revision int64
	var raw []byte
	err := q.QueryRow(`SELECT id,revision,data FROM mcp_connections WHERE id=?`, b.ConnectionID).Scan(&connectionID, &revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return zeroTool, zeroBinding, ErrNotFound
	}
	if err != nil {
		return zeroTool, zeroBinding, err
	}
	var c domain.MCPConnection
	if decodeStoredJSON(raw, &c) != nil || c.ID != connectionID || c.Revision != revision {
		return zeroTool, zeroBinding, errors.New("corrupt MCP connection")
	}
	if !c.Enabled || c.ID != b.ConnectionID || c.Revision != b.ConnectionRevision || c.Endpoint != b.Endpoint || c.Protocol != b.Protocol || c.CredentialRef != b.CredentialRef || c.CredentialRevision != b.CredentialRevision {
		return zeroTool, zeroBinding, ErrConflict
	}
	var snapshotID, snapshotOwner, operationID, opID, opOwner, status, opSnapshot string
	var snapshotRevision int64
	var snapshotRaw, opRaw []byte
	err = q.QueryRow(exactSnapshotSelect+` WHERE s.id=? AND s.connection_id=?`, b.SnapshotID, b.ConnectionID).Scan(&snapshotID, &snapshotOwner, &snapshotRevision, &operationID, &snapshotRaw, &opID, &opOwner, &status, &opSnapshot, &opRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return zeroTool, zeroBinding, ErrNotFound
	}
	if err != nil {
		return zeroTool, zeroBinding, err
	}
	var snapshot domain.MCPToolSnapshot
	var op discoveryIntentRecord
	if decodeStoredJSON(snapshotRaw, &snapshot) != nil || decodeStoredJSON(opRaw, &op) != nil || snapshot.ID != snapshotID || snapshot.ConnectionID != snapshotOwner || snapshot.ConnectionRevision != snapshotRevision || snapshotID != b.SnapshotID || snapshotOwner != b.ConnectionID || snapshotRevision != b.ConnectionRevision || snapshot.Digest != b.SnapshotDigest || snapshot.Protocol != b.Protocol || operationID != b.DiscoveryOperationID || operationID != opID || op.ID != opID || opOwner != b.ConnectionID || op.ConnectionID != opOwner || op.ConnectionRevision != b.ConnectionRevision || op.Endpoint != b.Endpoint || op.CredentialRevision != b.CredentialRevision || status != "succeeded" || opSnapshot != snapshotID {
		return zeroTool, zeroBinding, errors.New("invalid frozen MCP discovery authority")
	}
	if err := mcp.VerifyExecutionSnapshot(snapshot); err != nil {
		return zeroTool, zeroBinding, err
	}
	for _, selected := range b.Tools {
		if selected.Name != name {
			continue
		}
		for _, tool := range snapshot.Tools {
			if tool.Name != name {
				continue
			}
			output := ""
			if len(tool.OutputSchema) > 0 && string(tool.OutputSchema) != "null" {
				output = mcpHash(tool.OutputSchema)
			}
			if tool.DefinitionDigest != selected.DefinitionDigest || mcpHash(tool.InputSchema) != selected.InputSchemaDigest || output != selected.OutputSchemaDigest {
				return zeroTool, zeroBinding, ErrConflict
			}
			return tool, selected, nil
		}
	}
	return zeroTool, zeroBinding, ErrConflict
}

func validateMCPSource(q rowQuerier, m domain.Mission, source domain.MCPProposalSource, requireSuccess bool) (domain.Invocation, error) {
	if err := supportedMCPLedger(q, m.ID); err != nil {
		return domain.Invocation{}, err
	}
	v, err := readInvocation(q, source.ModelInvocationID)
	if err != nil {
		return v, err
	}
	if source.Version != 1 || source.Purpose != researchSourcePurpose(m) || source.MissionID != m.ID || source.Epoch != m.Epoch || v.MissionID != source.MissionID || v.Epoch != source.Epoch || v.SessionID != source.SessionID || v.GrantID != source.ReadGrantID || v.BindingDigest != source.BindingDigest || v.AuthorityDigest != source.AuthorityDigest || v.Kind != "model" || v.Role != domain.Researcher || m.State != "planning" {
		return v, ErrConflict
	}
	if err := mcpMissionAuthority(m, source.BindingDigest, source.AuthorityDigest); err != nil {
		return v, err
	}
	if _, err := invocationGrant(q, v, "planning"); err != nil {
		return v, err
	}
	if requireSuccess && (v.Status != "succeeded" || v.Result == nil) {
		return v, ErrConflict
	}
	var latest int64
	if err := q.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM invocations WHERE mission_id=? AND epoch=? AND session_id=? AND kind='model' AND role='researcher'`, m.ID, m.Epoch, source.SessionID).Scan(&latest); err != nil {
		return v, err
	}
	if latest != v.Ordinal {
		return v, ErrConflict
	}
	return v, nil
}

func validateMCPProposal(q rowQuerier, m domain.Mission, p domain.MCPProposal) error {
	source, err := readMCPRecord[domain.MCPProposalSource](q, "mcp_proposal_sources", "model_invocation_id", p.SourceInvocationID, m.ID)
	if err != nil {
		return err
	}
	v, err := validateMCPSource(q, m, source, true)
	if err != nil {
		return err
	}
	if p.Version != 1 || p.MissionID != m.ID || p.Epoch != source.Epoch || p.SessionID != source.SessionID || p.ReadGrantID != source.ReadGrantID || p.BindingDigest != source.BindingDigest || p.AuthorityDigest != source.AuthorityDigest || !sameJSON(p.SourceResult, v.Result) {
		return ErrConflict
	}
	tool, bound, err := currentMCPTool(q, m, p.ToolID)
	if err != nil {
		return err
	}
	canonical, err := mcp.ValidateArguments(tool, bound.Policy, p.Arguments)
	if err != nil {
		return err
	}
	if string(canonical) != string(p.Arguments) {
		return errors.New("noncanonical MCP arguments")
	}
	if p.Digest != proposalDigest(p) {
		return errors.New("invalid MCP proposal digest")
	}
	return nil
}

func proposalDigest(p domain.MCPProposal) string {
	p.Digest = ""
	p.CreatedAt = ""
	raw, _ := json.Marshal(p)
	return mcpHash(raw)
}
func mcpToolQuota(q rowQuerier, missionID string) error {
	var count int
	var reserved int64
	err := q.QueryRow(`SELECT (SELECT count(*) FROM invocations WHERE mission_id=? AND kind='repository')+(SELECT count(*) FROM mcp_invocations WHERE mission_id=?),(SELECT COALESCE(sum(reserved_result_bytes),0) FROM invocations WHERE mission_id=? AND kind='repository')+(SELECT COALESCE(sum(reserved_result_bytes),0) FROM mcp_invocations WHERE mission_id=?)`, missionID, missionID, missionID, missionID).Scan(&count, &reserved)
	if err != nil {
		return err
	}
	if count >= 3 || reserved+64<<10 > 192<<10 {
		return ErrMCPBudget
	}
	return nil
}

func mcpCheckpointBlocked(stage string) bool {
	switch stage {
	case "awaiting_tool_approval", "awaiting_mcp_materialization", "mcp_authorized_pending", "mcp_observation_ready":
		return true
	}
	return false
}
