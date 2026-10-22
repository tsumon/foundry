package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func controlledC4Fixture() Mission {
	m := c4Fixture()
	m.C4Operations = nil
	m.Spec.C4.ControlVersion = 1
	c := C4Control{SchemaVersion: 1, ID: "ready-1", Kind: "readiness", MissionID: m.ID, Epoch: m.Epoch, BaseCommit: m.BaseCommit, BaseDigest: strings.Repeat("c", 64), ProfileDigest: m.Spec.C4.ProfileDigest(), ActivationDigest: strings.Repeat("d", 64), ActivationExpiresAt: "2026-10-21T01:00:00Z", SourceKind: "synthetic_c4_control", PreparedRevision: 1, PreparedAt: c4Time, ExpiresAt: "2026-10-21T00:10:00Z", State: "prepared"}
	c.MaterialDigest = C4ControlDigest(c)
	m.C4Controls = []C4Control{c}
	return m
}
func readyC4Fixture() Mission {
	m := controlledC4Fixture()
	c := &m.C4Controls[0]
	c.Approval = &C4Approval{Digest: c.MaterialDigest, Revision: 2, At: c4Time}
	c.Dispatch = &C4Fact{Revision: 3, At: c4Time}
	c.Result = &C4ControlResult{Status: "ready", Revision: 4, At: c4Time, ValidUntil: c.ExpiresAt, Fingerprint: strings.Repeat("f", 64)}
	c.State = "completed"
	return m
}
func TestC4ControlValidationAndImmutableLifecycle(t *testing.T) {
	before := controlledC4Fixture()
	if err := ValidateC4Mission(before); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*C4Control){
		"material": func(c *C4Control) { c.MaterialDigest = strings.Repeat("a", 64) },
		"source":   func(c *C4Control) { c.SourceKind = "docker_c4_control"; c.MaterialDigest = C4ControlDigest(*c) },
		"approval digest": func(c *C4Control) {
			c.State = "approved"
			c.Approval = &C4Approval{Digest: strings.Repeat("a", 64), Revision: 10, At: c4Time}
		},
		"approval revision": func(c *C4Control) {
			c.State = "approved"
			c.Approval = &C4Approval{Digest: c.MaterialDigest, Revision: 9, At: c4Time}
		},
		"dispatch skip": func(c *C4Control) { c.State = "dispatched"; c.Dispatch = &C4Fact{Revision: 10, At: c4Time} },
		"expiry":        func(c *C4Control) { c.ExpiresAt = "2026-10-21T00:11:00Z"; c.MaterialDigest = C4ControlDigest(*c) },
		"target":        func(c *C4Control) { c.ContainerID = strings.Repeat("a", 64); c.MaterialDigest = C4ControlDigest(*c) },
	} {
		t.Run(name, func(t *testing.T) {
			after := cloneC4(before)
			change(&after.C4Controls[0])
			if ValidateC4ControlTransition(before, after) == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
	after := cloneC4(before)
	c := &after.C4Controls[0]
	c.State = "approved"
	c.Approval = &C4Approval{Digest: c.MaterialDigest, Revision: before.Revision, At: c4Time}
	if err := ValidateC4Transition(before, after); err != nil {
		t.Fatal(err)
	}
	ready := readyC4Fixture()
	if err := ValidateC4Mission(ready); err != nil {
		t.Fatal(err)
	}
	changed := cloneC4(ready)
	changed.C4Controls[0].Result.Fingerprint = strings.Repeat("a", 64)
	if ValidateC4Transition(ready, changed) == nil {
		t.Fatal("result substitution accepted")
	}
	changed = cloneC4(ready)
	changed.Epoch++
	changed.Plan = &Plan{Revision: 1}
	if err := ValidateC4Transition(ready, changed); err != nil {
		t.Fatal("historical receipt invalid after replan", err)
	}
	changed.C4Controls = append(changed.C4Controls, changed.C4Controls[0])
	if ValidateC4Mission(changed) == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestC4ControlOperationReceiptBinding(t *testing.T) {
	m := readyC4Fixture()
	o := c4Fixture().C4Operations[0]
	o.ProfileDigest = m.Spec.C4.ProfileDigest()
	o.ReadinessID = m.C4Controls[0].ID
	o.ReadinessDigest = m.C4Controls[0].MaterialDigest
	o.MaterialDigest = C4MaterialDigest(o)
	m.C4Operations = []C4Operation{o}
	if err := ValidateC4Mission(m); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "digest", "expired", "base", "source"} {
		t.Run(kind, func(t *testing.T) {
			n := cloneC4(m)
			o := &n.C4Operations[0]
			switch kind {
			case "missing":
				o.ReadinessID = "other"
			case "digest":
				o.ReadinessDigest = strings.Repeat("a", 64)
			case "expired":
				o.PreparedAt = "2026-10-21T00:10:00Z"
			case "base":
				o.BaseDigest = strings.Repeat("a", 64)
			case "source":
				n.C4Controls[0].SourceKind = "docker_c4_control"
			}
			o.MaterialDigest = C4MaterialDigest(*o)
			if ValidateC4Mission(n) == nil {
				t.Fatal("unbound operation accepted")
			}
		})
	}
	if err := ValidateC4Operation(*m.Spec.C4, o); err != nil {
		t.Fatal("standalone structure must remain usable", err)
	}
	m.C4Controls = nil
	if ValidateC4Mission(m) == nil {
		t.Fatal("standalone bypass leaked to full mission")
	}
}
func TestC4ControlLegacyDigestPreserved(t *testing.T) {
	m := c4Fixture()
	p := *m.Spec.C4
	o := m.C4Operations[0]
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "control_version") {
		t.Fatal("legacy version serialized")
	}
	raw, _ = json.Marshal(o)
	if strings.Contains(string(raw), "readiness") {
		t.Fatal("legacy receipt serialized")
	}
	// Adding omitted zero fields cannot affect the existing canonical digest.
	type oldProfile struct {
		SchemaVersion int    `json:"schema_version"`
		ID            string `json:"id"`
		Mode          string `json:"mode"`
		ImageID       string `json:"image_id"`
		Platform      string `json:"platform"`
		DockerPath    string `json:"docker_path"`
		Endpoint      string `json:"endpoint"`
		Verifier      string `json:"verifier"`
	}
	old := oldProfile{p.SchemaVersion, p.ID, p.Mode, p.ImageID, p.Platform, p.DockerPath, p.Endpoint, p.Verifier}
	want := c4Digest("profile", struct {
		Profile oldProfile `json:"profile"`
		Policy  C4Policy   `json:"policy"`
	}{old, FixedC4Policy()})
	if p.ProfileDigest() != want {
		t.Fatal("historical profile digest changed")
	}
}

func TestC4ControlCannotApproveStaleMaterialOrExceedReceiptTTL(t *testing.T) {
	m := controlledC4Fixture()
	m.Epoch++
	m.Plan = &Plan{Revision: 1}
	n := cloneC4(m)
	c := &n.C4Controls[0]
	c.State = "approved"
	c.Approval = &C4Approval{Digest: c.MaterialDigest, Revision: m.Revision, At: c.PreparedAt}
	if ValidateC4Transition(m, n) == nil {
		t.Fatal("stale material approved")
	}
	for _, kind := range []string{"expiry", "activation", "status", "code", "time"} {
		t.Run(kind, func(t *testing.T) {
			m := readyC4Fixture()
			c := &m.C4Controls[0]
			switch kind {
			case "expiry":
				c.Result.ValidUntil = "2026-10-21T00:10:01Z"
			case "activation":
				c.ActivationExpiresAt = "2026-10-21T00:05:00Z"
				c.ExpiresAt = c.ActivationExpiresAt
				c.MaterialDigest = C4ControlDigest(*c)
				c.Approval.Digest = c.MaterialDigest
			case "status":
				c.Result.Status = "cleaned"
			case "code":
				c.Result.FailureCode = "raw private output"
			case "time":
				c.Result.At = "2026-10-20T23:59:59Z"
			}
			if ValidateC4Mission(m) == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}

func TestC4ControlOperationRequiresAlreadyDurableReceipt(t *testing.T) {
	before := readyC4Fixture()
	c := &before.C4Controls[0]
	c.State = "dispatched"
	c.Result = nil
	after := readyC4Fixture()
	after.C4Controls[0].Result.Revision = before.Revision
	o := c4Fixture().C4Operations[0]
	o.ProfileDigest = after.Spec.C4.ProfileDigest()
	o.ReadinessID = c.ID
	o.ReadinessDigest = c.MaterialDigest
	o.MaterialDigest = C4MaterialDigest(o)
	after.C4Operations = []C4Operation{o}
	if ValidateC4Transition(before, after) == nil {
		t.Fatal("operation inherited uncommitted receipt")
	}
}

func cleanupC4Fixture() (Mission, C4Control) {
	m := readyC4Fixture()
	o := c4Fixture().C4Operations[0]
	o.ProfileDigest = m.Spec.C4.ProfileDigest()
	o.ReadinessID = m.C4Controls[0].ID
	o.ReadinessDigest = m.C4Controls[0].MaterialDigest
	o.MaterialDigest = C4MaterialDigest(o)
	o.Approval = &C4Approval{Digest: o.MaterialDigest, Revision: 5, At: c4Time}
	o.Dispatch = &C4Fact{Revision: 6, At: c4Time}
	o.Created = &C4Fact{Revision: 7, At: c4Time}
	o.ContainerID = strings.Repeat("a", 64)
	o.Result = &C4Result{Class: "outcome_unknown", Revision: 8, At: c4Time}
	o.State = "finished"
	m.C4Operations = []C4Operation{o}
	m.Epoch++
	m.Plan = &Plan{Revision: 1}
	c := m.C4Controls[0]
	c.ID = "cleanup-1"
	c.Kind = "cleanup"
	c.Epoch = m.Epoch
	c.PlanRevision = 1
	c.PreparedRevision = m.Revision
	c.OperationID = o.ID
	c.OperationDigest = o.MaterialDigest
	c.ContainerID = o.ContainerID
	c.State = "prepared"
	c.Approval = nil
	c.Dispatch = nil
	c.Result = nil
	c.MaterialDigest = C4ControlDigest(c)
	return m, c
}
func TestC4CleanupControlTargetsOlderOperationWithFreshAuthority(t *testing.T) {
	before, c := cleanupC4Fixture()
	after := cloneC4(before)
	after.C4Controls = append(after.C4Controls, c)
	if err := ValidateC4Transition(before, after); err != nil {
		t.Fatal("current cleanup for old operation rejected", err)
	}
	for _, kind := range []string{"id", "digest", "container", "epoch", "confirmed", "activation"} {
		t.Run(kind, func(t *testing.T) {
			b := cloneC4(before)
			a := cloneC4(after)
			c := &a.C4Controls[1]
			switch kind {
			case "id":
				c.OperationID = "missing"
			case "digest":
				c.OperationDigest = strings.Repeat("b", 64)
			case "container":
				c.ContainerID = "short"
			case "epoch":
				c.Epoch--
			case "activation":
				c.ActivationDigest = strings.Repeat("b", 64)
			case "confirmed":
				for _, m := range []*Mission{&a, &b} {
					o := &m.C4Operations[0]
					o.Cleanup = []C4Cleanup{{State: "confirmed", ContainerID: o.ContainerID, Owner: o.Owner, Revision: 9, At: c4Time}}
				}
			}
			c.MaterialDigest = C4ControlDigest(*c)
			if ValidateC4Transition(b, a) == nil {
				t.Fatal("invalid cleanup authority accepted")
			}
		})
	}
}
func TestC4CleanupRequiresPreviouslyDurableTerminalTarget(t *testing.T) {
	before, c := cleanupC4Fixture()
	before.C4Operations[0].Result = nil
	before.C4Operations[0].State = "created"
	after := cloneC4(before)
	after.C4Operations[0].State = "finished"
	after.C4Operations[0].Result = &C4Result{Class: "outcome_unknown", Revision: before.Revision, At: c4Time}
	after.C4Controls = append(after.C4Controls, c)
	if ValidateC4Transition(before, after) == nil {
		t.Fatal("cleanup material accepted before target terminal was durable")
	}
}
func TestC4ControlsRejectMalformedProfileDirectly(t *testing.T) {
	m := controlledC4Fixture()
	m.Spec.C4.Mode = "unknown"
	m.C4Controls[0].ProfileDigest = m.Spec.C4.ProfileDigest()
	m.C4Controls[0].MaterialDigest = C4ControlDigest(m.C4Controls[0])
	if ValidateC4Controls(m) == nil {
		t.Fatal("malformed profile accepted")
	}
}
