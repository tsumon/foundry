package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/knowledge"
	"sort"
	"strings"
)

var ErrKnowledge = errors.New("knowledge graph unavailable or invalid; rebuild derived graph")
var ErrKnowledgeQuery = errors.New("invalid knowledge query")

func (s *Store) initializeKnowledge() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_meta(id INTEGER PRIMARY KEY CHECK(id=1),data BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS knowledge_nodes(id TEXT PRIMARY KEY,kind TEXT NOT NULL,project_id TEXT NOT NULL,mission_id TEXT NOT NULL,label TEXT NOT NULL,data BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS knowledge_edges(id TEXT PRIMARY KEY,source TEXT NOT NULL,target TEXT NOT NULL,kind TEXT NOT NULL,data BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS knowledge_nodes_kind ON knowledge_nodes(kind);
 CREATE INDEX IF NOT EXISTS knowledge_nodes_project ON knowledge_nodes(project_id);
 CREATE INDEX IF NOT EXISTS knowledge_nodes_mission ON knowledge_nodes(mission_id);
 CREATE INDEX IF NOT EXISTS knowledge_edges_source ON knowledge_edges(source);
 CREATE INDEX IF NOT EXISTS knowledge_edges_target ON knowledge_edges(target);`)
	return err
}

// SyncKnowledge reads one business snapshot and replaces only derived records atomically.
func (s *Store) SyncKnowledge(ctx context.Context, force bool) (err error) {
	defer func() {
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = fmt.Errorf("%w", ErrKnowledge)
			}
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	catalog, _, err := readCatalog(tx)
	if err != nil {
		return err
	}
	source := knowledge.Source{Catalog: catalog, Missions: []domain.Mission{}}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM missions ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var m domain.Mission
		m, err = decodeMission(raw)
		if err != nil {
			rows.Close()
			return err
		}
		source.Missions = append(source.Missions, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT data FROM plan_revisions ORDER BY mission_id,plan_revision`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var revision domain.PlanRevision
		if err = json.Unmarshal(raw, &revision); err != nil {
			rows.Close()
			return err
		}
		source.PlanRevisions = append(source.PlanRevisions, revision)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	graph, err := knowledge.Project(source)
	if err != nil {
		return err
	}
	if err = knowledge.Validate(graph); err != nil {
		return err
	}
	// Hash the allowlisted projection, never credentials, prompts, or whole database bytes.
	raw, err := json.Marshal(struct {
		Version int
		Nodes   []knowledge.Node
		Edges   []knowledge.Edge
		Issues  []knowledge.Issue
	}{knowledge.SchemaVersion, graph.Nodes, graph.Edges, graph.Issues})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if !force {
		var previous []byte
		e := tx.QueryRowContext(ctx, `SELECT data FROM knowledge_meta WHERE id=1`).Scan(&previous)
		if e == nil {
			old, e := readKnowledgeGraph(ctx, tx)
			if e != nil {
				return e
			}
			if old.SourceDigest == digest {
				return tx.Commit()
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		} else {
			var count int
			if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM knowledge_nodes)+(SELECT count(*) FROM knowledge_edges)`).Scan(&count); e != nil {
				return e
			}
			if count != 0 {
				return ErrKnowledge
			}
		}
	}
	graph.SchemaVersion = knowledge.SchemaVersion
	graph.Backend = knowledge.Backend
	graph.SourceDigest = digest
	graph.SyncedAt = timestamp()
	graph.TotalNodes = len(graph.Nodes)
	graph.TotalEdges = len(graph.Edges)
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_edges`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM knowledge_nodes`); err != nil {
		return err
	}
	for _, n := range graph.Nodes {
		raw, _ := json.Marshal(n)
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_nodes VALUES(?,?,?,?,?,?)`, n.ID, n.Kind, n.ProjectID, n.MissionID, n.Label, raw); err != nil {
			return err
		}
	}
	for _, e := range graph.Edges {
		raw, _ := json.Marshal(e)
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_edges VALUES(?,?,?,?,?)`, e.ID, e.From, e.To, e.Kind, raw); err != nil {
			return err
		}
	}
	graph.Nodes = []knowledge.Node{}
	graph.Edges = []knowledge.Edge{}
	raw, err = json.Marshal(graph)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_meta VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func readKnowledgeGraph(ctx context.Context, tx *sql.Tx) (knowledge.Graph, error) {
	var g knowledge.Graph
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM knowledge_meta WHERE id=1`).Scan(&raw); err != nil {
		return g, ErrKnowledge
	}
	if json.Unmarshal(raw, &g) != nil || g.SchemaVersion != knowledge.SchemaVersion || g.Backend != knowledge.Backend {
		return g, ErrKnowledge
	}
	for _, table := range []string{"knowledge_nodes", "knowledge_edges"} {
		columns := "id,kind,project_id,mission_id,label,data"
		if table == "knowledge_edges" {
			columns = "id,kind,source,target,data"
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM `+table+` ORDER BY id`)
		if err != nil {
			return g, ErrKnowledge
		}
		for rows.Next() {
			var id, kind, first, second, label string
			if table == "knowledge_nodes" {
				err = rows.Scan(&id, &kind, &first, &second, &label, &raw)
			} else {
				err = rows.Scan(&id, &kind, &first, &second, &raw)
			}
			if err != nil {
				rows.Close()
				return g, ErrKnowledge
			}
			if table == "knowledge_nodes" {
				var n knowledge.Node
				if json.Unmarshal(raw, &n) != nil || n.ID != id || n.Kind != kind || n.ProjectID != first || n.MissionID != second || n.Label != label {
					rows.Close()
					return g, ErrKnowledge
				}
				g.Nodes = append(g.Nodes, n)
			} else {
				var e knowledge.Edge
				if json.Unmarshal(raw, &e) != nil || e.ID != id || e.Kind != kind || e.From != first || e.To != second {
					rows.Close()
					return g, ErrKnowledge
				}
				g.Edges = append(g.Edges, e)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return g, ErrKnowledge
		}
	}
	raw, _ = json.Marshal(struct {
		Version int
		Nodes   []knowledge.Node
		Edges   []knowledge.Edge
		Issues  []knowledge.Issue
	}{knowledge.SchemaVersion, g.Nodes, g.Edges, g.Issues})
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != g.SourceDigest {
		return g, ErrKnowledge
	}
	if len(g.Nodes) != g.TotalNodes || len(g.Edges) != g.TotalEdges || knowledge.Validate(g) != nil {
		return g, ErrKnowledge
	}
	return g, nil
}

func (s *Store) QueryKnowledge(ctx context.Context, q knowledge.Query) (out knowledge.Graph, err error) {
	if q.Depth == 0 {
		q.Depth = 1
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Mode == "" {
		q.Mode = "neighbors"
	}
	if q.Depth < 1 || q.Depth > 4 || q.Limit < 1 || q.Limit > 200 || (q.Mode != "neighbors" && q.Mode != "impact" && q.Mode != "lineage") {
		return out, ErrKnowledgeQuery
	}
	defer func() {
		if err != nil && !errors.Is(err, ErrKnowledgeQuery) {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = ErrKnowledge
			}
		}
	}()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	full, err := readKnowledgeGraph(ctx, tx)
	if err != nil {
		return out, err
	}
	out = full
	out.Nodes = []knowledge.Node{}
	out.Edges = []knowledge.Edge{}
	reachable := map[string]bool{}
	if q.Root != "" {
		direction := `e.source=w.id`
		next := `e.target`
		if q.Mode == "lineage" {
			direction = `e.target=w.id`
			next = `e.source`
		} else if q.Mode == "neighbors" {
			direction = `(e.source=w.id OR e.target=w.id)`
			next = `CASE WHEN e.source=w.id THEN e.target ELSE e.source END`
		}
		// UNION bounds cycles to one visit per node/depth; hard work budget also bounds wide graphs.
		sqlText := `WITH RECURSIVE walk(id,depth) AS (SELECT id,0 FROM knowledge_nodes WHERE id=? AND (?='' OR project_id=? OR id IN (SELECT e.target FROM knowledge_edges e JOIN knowledge_nodes b ON b.id=e.source WHERE b.kind='resource_binding' AND b.project_id=?)) AND (?='' OR mission_id=?) UNION SELECT ` + next + `,w.depth+1 FROM walk w JOIN knowledge_edges e ON ` + direction + ` JOIN knowledge_nodes n ON n.id=` + next + ` WHERE w.depth<? AND (?='' OR n.project_id=? OR n.id IN (SELECT e.target FROM knowledge_edges e JOIN knowledge_nodes b ON b.id=e.source WHERE b.kind='resource_binding' AND b.project_id=?)) AND (?='' OR n.mission_id=?) LIMIT 10001) SELECT id FROM walk`
		rows, e := tx.QueryContext(ctx, sqlText, q.Root, q.ProjectID, q.ProjectID, q.ProjectID, q.MissionID, q.MissionID, q.Depth, q.ProjectID, q.ProjectID, q.ProjectID, q.MissionID, q.MissionID)
		if e != nil {
			return out, e
		}
		count := 0
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return out, e
			}
			reachable[id] = true
			count++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if count >= 10001 {
			out.Truncated = true
		}
	}
	projectVisible := map[string]bool{}
	for _, n := range full.Nodes {
		if n.ProjectID == q.ProjectID {
			projectVisible[n.ID] = true
		}
	}
	// Shared resources participate through their project-owned binding, not an invented owner.
	bindings := map[string]bool{}
	for _, n := range full.Nodes {
		if n.Kind == "resource_binding" && n.ProjectID == q.ProjectID {
			bindings[n.ID] = true
		}
	}
	for _, e := range full.Edges {
		if bindings[e.From] {
			projectVisible[e.To] = true
		}
	}
	if q.Root != "" {
		sort.SliceStable(full.Nodes, func(i, j int) bool { return full.Nodes[i].ID == q.Root && full.Nodes[j].ID != q.Root })
	}
	selected := map[string]bool{}
	search := strings.ToLower(q.Search)
	for _, n := range full.Nodes {
		if q.Root != "" && !reachable[n.ID] || q.ProjectID != "" && !projectVisible[n.ID] || q.MissionID != "" && n.MissionID != q.MissionID || q.Kind != "" && n.Kind != q.Kind || search != "" && !strings.Contains(strings.ToLower(n.Label+" "+n.ID), search) {
			continue
		}
		if len(out.Nodes) >= q.Limit {
			out.Truncated = true
			continue
		}
		out.Nodes = append(out.Nodes, n)
		selected[n.ID] = true
	}
	for _, e := range full.Edges {
		if selected[e.From] && selected[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	if q.ProjectID != "" || q.MissionID != "" || q.Root != "" || q.Kind != "" || q.Search != "" {
		// Issues use the same exact provenance IDs as graph facts. Never infer
		// ownership from slash-delimited IDs, which may themselves contain slashes.
		visibleSources := map[string]bool{}
		for _, n := range out.Nodes {
			visibleSources[n.Source.ID] = true
		}
		for _, e := range out.Edges {
			visibleSources[e.Source.ID] = true
		}
		out.Issues = []knowledge.Issue{}
		for _, issue := range full.Issues {
			if visibleSources[issue.SourceID] {
				out.Issues = append(out.Issues, issue)
			}
		}
	}
	return out, tx.Commit()
}
