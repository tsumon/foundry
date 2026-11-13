package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"

	"foundry/internal/domain"
)

func Schema() Ontology {
	o := Ontology{Version: SchemaVersion, Types: []TypeDefinition{}, Relations: []RelationDefinition{}, Rules: []string{"业务记录是权威来源；关系不授予执行权限或审批。", "悬空引用明确记录问题，不补造节点或证据。", "归档节点标记为历史，不证明当前任务验收通过。", "证据来源沿用 EffectiveSource；模拟与未知来源不等同于隔离执行。", "模型上下文仅包含当前任务已有授权输入字段的结构关系。", "planned 文件仅表示计划涉及该路径，不证明仓库中存在该文件。"}}
	labels := map[string]string{"project": "项目", "target": "交付目标", "environment": "环境", "resource": "资源", "resource_binding": "资源绑定", "task": "任务", "repository": "仓库", "file": "文件", "plan": "计划", "step": "步骤", "candidate": "候选", "evidence": "验证证据", "review": "评审", "approval": "审批记录", "delivery": "交付记录"}
	descriptions := map[string]string{"project": "目录中登记的项目", "target": "项目的交付平台、格式与架构", "environment": "项目登记的运行环境", "resource": "目录资源，不含账户与外部标识", "resource_binding": "项目、环境与资源的真实绑定", "task": "任务业务记录", "repository": "任务选定的仓库与基线提交，不含绝对路径", "file": "已记录的上下文或变更路径；planned 表示计划路径", "plan": "带修订号的当前或历史计划", "step": "计划中的步骤与依赖", "candidate": "实际记录的候选摘要", "evidence": "保留真实来源类别的验证记录，不含输出", "review": "候选评审记录，不推导额外通过结论", "approval": "已有审批记录，关联不替代审批校验", "delivery": "已有交付记录与产物摘要，不含文件路径"}
	for _, k := range []string{"project", "target", "environment", "resource", "resource_binding", "task", "repository", "file", "plan", "step", "candidate", "evidence", "review", "approval", "delivery"} {
		o.Types = append(o.Types, TypeDefinition{k, labels[k], descriptions[k]})
	}
	add := func(k string, f, t []string) {
		o.Relations = append(o.Relations, RelationDefinition{Kind: k, Label: map[string]string{"contains": "包含", "binds": "绑定", "uses": "使用", "touches": "涉及文件", "enables": "前置步骤", "evolves_to": "候选演进", "verified_by": "验证记录", "reviewed_by": "评审记录", "delivered_as": "交付记录", "approved_by": "审批关联"}[k], From: f, To: t})
	}
	add("contains", []string{"project", "task", "repository", "plan"}, []string{"target", "environment", "resource", "resource_binding", "task", "repository", "file", "plan", "step", "candidate", "evidence", "review", "approval", "delivery"})
	add("binds", []string{"environment"}, []string{"resource_binding"})
	add("uses", []string{"resource_binding"}, []string{"resource"})
	add("touches", []string{"step", "candidate"}, []string{"file"})
	add("enables", []string{"step"}, []string{"step"})
	add("evolves_to", []string{"candidate"}, []string{"candidate"})
	add("verified_by", []string{"candidate"}, []string{"evidence"})
	add("reviewed_by", []string{"candidate", "evidence"}, []string{"review"})
	add("delivered_as", []string{"candidate"}, []string{"delivery"})
	add("approved_by", []string{"plan", "candidate"}, []string{"approval"})
	for i := range o.Relations {
		r := &o.Relations[i]
		for _, f := range r.From {
			for _, t := range r.To {
				if r.Kind != "contains" || legalContains(f, t) {
					r.Pairs = append(r.Pairs, [2]string{f, t})
				}
			}
		}
	}
	return o
}

func Validate(g Graph) error {
	if g.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported knowledge schema %d", g.SchemaVersion)
	}
	kinds := map[string]bool{}
	relations := map[string]RelationDefinition{}
	for _, t := range Schema().Types {
		kinds[t.Kind] = true
	}
	for _, r := range Schema().Relations {
		relations[r.Kind] = r
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		if n.ID == "" || !kinds[n.Kind] || n.Source.ID == "" {
			return fmt.Errorf("invalid knowledge node %q", n.ID)
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("duplicate node %s", n.ID)
		}
		nodes[n.ID] = n
	}
	contains := func(a []string, s string) bool {
		for _, v := range a {
			if v == s {
				return true
			}
		}
		return false
	}
	ids := map[string]bool{}
	for _, e := range g.Edges {
		r, ok := relations[e.Kind]
		f, fok := nodes[e.From]
		t, tok := nodes[e.To]
		pairOK := false
		for _, pair := range r.Pairs {
			if pair[0] == f.Kind && pair[1] == t.Kind {
				pairOK = true
				break
			}
		}
		if !pairOK {
			return fmt.Errorf("illegal relation pair %s -> %s", f.Kind, t.Kind)
		}
		if e.ID == "" || ids[e.ID] || !ok || !fok || !tok || !contains(r.From, f.Kind) || !contains(r.To, t.Kind) || e.Source.ID == "" {
			return fmt.Errorf("invalid knowledge edge %q", e.ID)
		}
		ids[e.ID] = true
	}
	return nil
}

func legalContains(from, to string) bool {
	switch from {
	case "project":
		return to == "target" || to == "environment" || to == "resource" || to == "resource_binding" || to == "task"
	case "task":
		return to == "repository" || to == "file" || to == "plan" || to == "candidate" || to == "evidence" || to == "review" || to == "approval" || to == "delivery"
	case "repository":
		return to == "file"
	case "plan":
		return to == "step"
	}
	return false
}

type projector struct {
	g     Graph
	nodes map[string]bool
	edges map[string]bool
}

func key(kind, id string) string { return kind + ":" + url.PathEscape(id) }
func (p *projector) issue(id, msg string) {
	p.g.Issues = append(p.g.Issues, Issue{"invalid_reference", id, msg})
}
func (p *projector) node(kind, id, label, project, mission, state string, hist bool, rev int64, props map[string]string) string {
	if id == "" {
		p.issue(kind, "record has no source ID")
		return ""
	}
	n := key(kind, id)
	if p.nodes[n] {
		p.issue(id, "duplicate record ID")
		return n
	}
	p.nodes[n] = true
	p.g.Nodes = append(p.g.Nodes, Node{ID: n, Kind: kind, Label: label, ProjectID: project, MissionID: mission, State: state, Historical: hist, Source: Provenance{kind, id, rev}, Properties: props})
	return n
}
func (p *projector) edge(kind, from, to string, src Provenance) {
	if !p.nodes[from] || !p.nodes[to] {
		p.issue(src.ID, "missing endpoint for "+kind)
		return
	}
	id := key(kind, from+"|"+to)
	if p.edges[id] {
		return
	}
	p.edges[id] = true
	p.g.Edges = append(p.g.Edges, Edge{id, from, to, kind, src})
}
func Project(s Source) (Graph, error) {
	p := projector{g: Graph{SchemaVersion: SchemaVersion, Backend: Backend, Nodes: []Node{}, Edges: []Edge{}, Issues: []Issue{}}, nodes: map[string]bool{}, edges: map[string]bool{}}
	cat := s.Catalog
	for _, v := range cat.Projects {
		p.node("project", v.ID, v.Name, v.ID, "", "", false, v.Revision, nil)
	}
	for _, v := range cat.Targets {
		n := p.node("target", v.ID, v.Name, v.ProjectID, "", "", false, v.Revision, map[string]string{"platform": v.Platform, "format": v.Format, "architecture": v.Architecture, "distribution": v.Distribution})
		p.edge("contains", key("project", v.ProjectID), n, Provenance{"target", v.ID, v.Revision})
	}
	for _, v := range cat.Environments {
		n := p.node("environment", v.ID, v.Name, v.ProjectID, "", "", false, v.Revision, map[string]string{"tier": v.Tier})
		p.edge("contains", key("project", v.ProjectID), n, Provenance{"environment", v.ID, v.Revision})
	}
	for _, v := range cat.Resources {
		pr := ""
		if v.OwnerProjectID != nil {
			pr = *v.OwnerProjectID
		}
		n := p.node("resource", v.ID, v.Name, pr, "", "", false, v.Revision, map[string]string{"kind": v.Kind, "protected": strconv.FormatBool(v.Protected)})
		if pr != "" {
			p.edge("contains", key("project", pr), n, Provenance{"resource", v.ID, v.Revision})
		}
	}
	for _, v := range cat.Bindings {
		n := p.node("resource_binding", v.ID, "Resource binding", v.ProjectID, "", "", false, v.Revision, nil)
		src := Provenance{"resource_binding", v.ID, v.Revision}
		p.edge("contains", key("project", v.ProjectID), n, src)
		p.edge("binds", key("environment", v.EnvironmentID), n, src)
		p.edge("uses", n, key("resource", v.ResourceID), src)
	}
	for _, m := range s.Missions {
		p.mission(m)
	}
	for _, r := range s.PlanRevisions {
		if !p.nodes[key("task", r.MissionID)] {
			p.issue(r.MissionID, "plan revision references missing task")
			continue
		}
		duplicate := false
		for _, m := range s.Missions {
			if m.ID != r.MissionID {
				continue
			}
			same := func(plan *domain.Plan) bool {
				if plan == nil {
					return false
				}
				a, _ := json.Marshal(plan)
				b, _ := json.Marshal(r.Plan)
				return string(a) == string(b)
			}
			if same(m.Plan) {
				duplicate = true
			}
			for _, a := range m.RunArchives {
				if same(a.Plan) {
					duplicate = true
				}
			}
		}
		if !duplicate {
			p.run(r.MissionID, fmt.Sprintf("plan-history/%d", r.PlanRevision), r.MissionRevision, true, &r.Plan, "", nil, nil, nil, nil, nil, nil)
		}
	}
	for _, n := range p.g.Nodes {
		if n.Kind != "approval" || n.Label != "execute" || n.Properties["plan_revision"] == "0" {
			continue
		}
		matched := false
		for _, plan := range p.g.Nodes {
			if plan.Kind == "plan" && plan.MissionID == n.MissionID && plan.Properties["revision"] == n.Properties["plan_revision"] {
				p.edge("approved_by", plan.ID, n.ID, n.Source)
				matched = true
			}
		}
		if !matched {
			p.issue(n.Source.ID, "approval references unavailable plan revision")
		}
	}
	for _, v := range cat.MissionLinks {
		if p.nodes[key("project", v.ProjectID)] && p.nodes[key("task", v.MissionID)] {
			for i := range p.g.Nodes {
				if p.g.Nodes[i].MissionID == v.MissionID {
					p.g.Nodes[i].ProjectID = v.ProjectID
				}
			}
		}
		p.edge("contains", key("project", v.ProjectID), key("task", v.MissionID), Provenance{"mission_link", v.MissionID + "/" + v.ProjectID, cat.Revision})
	}
	sort.Slice(p.g.Nodes, func(i, j int) bool { return p.g.Nodes[i].ID < p.g.Nodes[j].ID })
	sort.Slice(p.g.Edges, func(i, j int) bool { return p.g.Edges[i].ID < p.g.Edges[j].ID })
	p.g.TotalNodes = len(p.g.Nodes)
	p.g.TotalEdges = len(p.g.Edges)
	raw, _ := json.Marshal(p.g)
	sum := sha256.Sum256(raw)
	p.g.SourceDigest = hex.EncodeToString(sum[:])
	return p.g, Validate(p.g)
}
func (p *projector) mission(m domain.Mission) {
	task := p.node("task", m.ID, m.Title, "", m.ID, m.State, false, m.Revision, nil)
	src := Provenance{"task", m.ID, m.Revision}
	if m.RepoPath != "" || m.BaseCommit != "" {
		repo := p.node("repository", m.ID, "Repository", "", m.ID, "", false, m.Revision, map[string]string{"base_commit": m.BaseCommit})
		p.edge("contains", task, repo, src)
		for _, f := range m.ContextManifest {
			n := p.node("file", m.ID+"/"+f.Path, f.Path, "", m.ID, "", false, m.Revision, map[string]string{"sha256": f.SHA256})
			p.edge("contains", repo, n, src)
		}
	}
	p.run(m.ID, "current", m.Revision, false, m.Plan, m.CandidateDigest, m.Changes, m.Evidence, m.Reviews, m.Delivery, m.Approvals, m.CodingAttempts)
	for i, a := range m.RunArchives {
		p.run(m.ID, fmt.Sprintf("archive/%d/%d", a.Epoch, i), a.Revision, true, a.Plan, a.CandidateDigest, a.Changes, a.Evidence, a.Reviews, a.Delivery, nil, a.CodingAttempts)
	}
}
func (p *projector) run(mid, run string, rev int64, hist bool, plan *domain.Plan, digest string, changes []domain.Change, evidence []domain.Verification, reviews []domain.Review, delivery *domain.Delivery, approvals []domain.Approval, attempts []domain.CodingAttempt) {
	prefix := mid + "/" + run
	task := key("task", mid)
	src := Provenance{"task_run", prefix, rev}
	add := func(kind, id, label, state string, props map[string]string) string {
		n := p.node(kind, prefix+"/"+id, label, "", mid, state, hist, rev, props)
		p.edge("contains", task, n, src)
		return n
	}
	candidate := ""
	if digest != "" {
		candidate = add("candidate", "candidate/"+digest, "Candidate", "", map[string]string{"digest": digest})
	}
	candidates := map[string]string{}
	if candidate != "" {
		candidates[digest] = candidate
	}
	ensureCandidate := func(d string, recorded bool) string {
		if d == "" {
			return ""
		}
		if n := candidates[d]; n != "" {
			return n
		}
		state := "previous"
		props := map[string]string{"digest": d}
		if !recorded {
			state = "referenced"
			props["snapshot_availability"] = "not_recorded"
		}
		n := add("candidate", "candidate/"+d, "Candidate", state, props)
		candidates[d] = n
		for i := range p.g.Nodes {
			if p.g.Nodes[i].ID == n {
				p.g.Nodes[i].Historical = true
				break
			}
		}
		return n
	}
	for _, a := range attempts {
		for _, ref := range []*domain.GenerationRef{a.Before, a.After} {
			if ref != nil {
				ensureCandidate(ref.Digest, true)
			}
		}
	}
	for _, a := range attempts {
		if a.Before != nil && a.After != nil && a.Before.Digest != "" && a.After.Digest != "" && a.Before.Digest != a.After.Digest {
			p.edge("evolves_to", candidates[a.Before.Digest], candidates[a.After.Digest], Provenance{"coding_attempt", a.ID, rev})
		}
	}
	files := map[string]string{}
	for _, c := range changes {
		if files[c.Path] != "" {
			continue
		}
		files[c.Path] = add("file", "file/"+c.Path, c.Path, "", nil)
		if candidate != "" {
			p.edge("touches", candidate, files[c.Path], src)
		}
	}
	planID := ""
	if plan != nil {
		planID = add("plan", fmt.Sprint(plan.Revision), "Plan", "", map[string]string{"revision": fmt.Sprint(plan.Revision)})
		steps := map[string]string{}
		for _, s := range plan.Steps {
			if s.ID == "" {
				p.issue(prefix, "step has no ID")
				continue
			}
			steps[s.ID] = p.node("step", prefix+"/step/"+s.ID, s.Title, "", mid, s.Status, hist, rev, nil)
			p.edge("contains", planID, steps[s.ID], src)
		}
		for _, s := range plan.Steps {
			for _, dep := range s.DependsOn {
				p.edge("enables", steps[dep], steps[s.ID], src)
			}
			for _, path := range s.Files {
				f := files[path]
				if f == "" && !hist {
					recorded := key("file", mid+"/"+path)
					if p.nodes[recorded] {
						f = recorded
					}
				}
				if f == "" {
					f = add("file", "planned-file/"+path, path, "planned", nil)
					files[path] = f
					for i := range p.g.Nodes {
						if p.g.Nodes[i].ID == f {
							p.g.Nodes[i].Source = Provenance{"plan", prefix + "/" + fmt.Sprint(plan.Revision), plan.Revision}
							break
						}
					}
				}
				p.edge("touches", steps[s.ID], f, src)
			}
		}
	}
	evIDs := []string{}
	for i, v := range evidence {
		id := v.ID
		if id == "" {
			id = fmt.Sprintf("legacy/%d", i)
			p.issue(prefix, "legacy verification has no ID; positional identity only")
		}
		n := add("evidence", "evidence/"+id, "Verification", strconv.FormatBool(v.Passed), map[string]string{"candidate_digest": v.CandidateDigest})
		for j := range p.g.Nodes {
			if p.g.Nodes[j].ID == n {
				p.g.Nodes[j].EvidenceSource = v.EffectiveSource()
				if v.CandidateDigest != "" && digest != "" && v.CandidateDigest != digest {
					p.g.Nodes[j].Historical = true
				}
				break
			}
		}
		evIDs = append(evIDs, n)
		if v.CandidateDigest != "" {
			target := ensureCandidate(v.CandidateDigest, false)
			for j := range p.g.Nodes {
				if p.g.Nodes[j].ID == target && p.g.Nodes[j].State == "referenced" {
					p.g.Nodes[j].Source = Provenance{"evidence", prefix + "/evidence/" + id, rev}
					break
				}
			}
			p.edge("verified_by", target, n, Provenance{"evidence", prefix + "/evidence/" + id, rev})
		}
	}
	for i, v := range reviews {
		n := add("review", fmt.Sprintf("review/%d", i), string(v.Role), strconv.FormatBool(v.Passed), nil)
		if v.CandidateDigest != "" && digest != "" && v.CandidateDigest != digest {
			for j := range p.g.Nodes {
				if p.g.Nodes[j].ID == n {
					p.g.Nodes[j].Historical = true
					break
				}
			}
		}
		if v.CandidateDigest != "" {
			if target := candidates[v.CandidateDigest]; target != "" {
				p.edge("reviewed_by", target, n, src)
			} else {
				p.issue(prefix, "review references unavailable candidate")
			}
		}
		if v.EvidenceDigest != "" {
			if v.EvidenceDigest == domain.VerificationDigest(evidence) {
				for _, e := range evIDs {
					p.edge("reviewed_by", e, n, src)
				}
			} else {
				p.issue(prefix, "review evidence digest does not match recorded evidence")
			}
		}
	}
	for i, v := range approvals {
		n := add("approval", fmt.Sprintf("approval/%d", i), v.Kind, "recorded", map[string]string{"revision": fmt.Sprint(v.Revision), "plan_revision": fmt.Sprint(v.PlanRevision), "run": run})
		if v.Kind == "execute" && v.PlanRevision != 0 {
			if plan != nil && plan.Revision == v.PlanRevision {
				p.edge("approved_by", planID, n, src)
			} else {
				// Historical plan relations are resolved after all run and revision records are projected.
			}
		}
	}
	if delivery != nil {
		n := add("delivery", "delivery", "Delivery", "recorded", map[string]string{"patch_sha256": delivery.PatchSHA256, "report_sha256": delivery.ReportSHA256})
		if delivery.CandidateDigest != "" && delivery.CandidateDigest == digest && candidate != "" {
			p.edge("delivered_as", candidate, n, src)
		} else {
			p.issue(prefix, "delivery references unavailable candidate")
		}
	}
}

// TaskContext projects only fields already carried by the existing model payload.
// It excludes title, repository location, catalogs, approvals, reviews and archives.
func TaskContext(m domain.Mission) Context {
	safe := domain.Mission{ID: m.ID, Revision: m.Revision, ContextManifest: m.ContextManifest, Plan: m.Plan, Changes: m.Changes, Evidence: m.Evidence, CandidateDigest: m.CandidateDigest}
	// Manifest paths are approved context; no repository identity or source metadata is needed.
	safe.RepoPath = "approved-context"
	g, _ := Project(Source{Missions: []domain.Mission{safe}})
	c := Context{SchemaVersion: SchemaVersion, MissionID: m.ID, Revision: m.Revision, Nodes: []Node{}, Edges: []Edge{}}
	allowedCandidates := map[string]bool{}
	for _, v := range m.Evidence {
		allowedCandidates[v.CandidateDigest] = true
	}
	keep := map[string]bool{}
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].Kind == "task" && g.Nodes[j].Kind != "task" })
	for _, n := range g.Nodes {
		if n.Kind == "candidate" && !allowedCandidates[n.Properties["digest"]] {
			continue
		}
		if n.Kind == "repository" {
			continue
		}
		if len(c.Nodes) >= 60 {
			c.Truncated = true
			break
		}
		n.Source = Provenance{}
		n.State = ""
		n.Properties = nil
		c.Nodes = append(c.Nodes, n)
		keep[n.ID] = true
	}
	for _, e := range g.Edges {
		if e.From == key("repository", m.ID) {
			e.From = key("task", m.ID)
			e.ID = key("contains", e.From+"|"+e.To)
		}
		if keep[e.From] && keep[e.To] {
			if len(c.Edges) >= 100 {
				c.Truncated = true
				break
			}
			e.Source = Provenance{}
			c.Edges = append(c.Edges, e)
		}
	}
	return c
}
