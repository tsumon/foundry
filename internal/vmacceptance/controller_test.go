package vmacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCancelledAdmissionHasNoEffects(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native admission hosts")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(root, "bundle")
	if e = os.Mkdir(bundle, 0700); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "private")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, e := Run(ctx, Config{NativeVMAcceptance: true, Bundle: bundle, DataDir: data})
	if e == nil || summary.Passed {
		t.Fatal("cancelled acceptance passed")
	}
	if _, e = os.Stat(data); !os.IsNotExist(e) {
		t.Fatal("cancelled acceptance had effects")
	}
}

func TestRunWithoutExplicitPermissionHasNoEffects(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "new-private-data")
	s, err := Run(context.Background(), Config{Bundle: root, DataDir: data})
	if err == nil || s.Passed || s.Stage != "admission" || s.FailureCode != "explicit_native_vm_acceptance_required" {
		t.Fatal(s, err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("created data without explicit permission")
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > 8192 {
		t.Fatal("invalid bounded summary")
	}
}

func TestMalformedBundleProducesRetainedSanitizedFailureWithoutNativeLaunch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native admission hosts")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(root, "bundle")
	if e = os.Mkdir(bundle, 0700); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "private")
	summary, e := Run(context.Background(), Config{NativeVMAcceptance: true, Bundle: bundle, DataDir: data})
	if e == nil || summary.Passed || summary.Stage != "bundle" || summary.FailureCode != "bundle_failed" {
		t.Fatal(summary, e)
	}
	raw, e := os.ReadFile(filepath.Join(data, "summary.json"))
	if e != nil {
		t.Fatal(e)
	}
	var persisted Summary
	if e = json.Unmarshal(raw, &persisted); e != nil || persisted.Passed || persisted.FailureCode != summary.FailureCode {
		t.Fatal("failure report missing", e)
	}
	entries, e := os.ReadDir(filepath.Join(data, "instances"))
	if e != nil || len(entries) != 0 {
		t.Fatal("invalid bundle launched instance", e)
	}
}

func TestReserveDataRootIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := reserveDataRoot(path); err != nil {
		t.Fatal(err)
	}
	if err := reserveDataRoot(path); err == nil {
		t.Fatal("existing root claimed")
	}
}

func TestSourceDigestIncludesCommittedMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "source")
	if err := createRepository(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before, err := sourceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := sourceTreeDigest(root)
	if err != nil || before == after {
		t.Fatal("git mutation was invisible", err)
	}
}

func TestExpectedV2BaselineExecutionErrorStillRequiresOwnedNativeEvidence(t *testing.T) {
	for _, caseID := range []string{"typecheck", "setup", "discovery", "memory", "pids", "timeout", "signal", "held_stream"} {
		t.Run(caseID, func(t *testing.T) {
			profile := "c4_python_model_v2"
			if caseID == "typecheck" {
				profile = "c4_typescript_model_v2"
			}
			f, e := selectFixture(profile, caseID)
			if e != nil {
				t.Fatal(e)
			}
			o := operationFixture()[0]
			o.Result.Class, o.Result.Tests = f.expectedClass, 0
			m := domain.Mission{ID: "mission", C4Operations: []domain.C4Operation{o}}
			m.Spec.C4 = &domain.C4Profile{EmbeddedVM: &domain.C4VMIdentity{}, V2: &domain.C4ProfileV2{}}
			s := Summary{V2: &EvidenceV2{Inputs: &domain.C4InputContractV2{}}}
			root := t.TempDir()
			// This is the real post-Execute boundary used by Run. An empty runtime
			// proves routing to ownership validation, never synthetic native success.
			finished, e := finishBaseline(context.Background(), &s, m, &vmruntime.Runtime{}, f, root, root, root, &localFixture{}, &fixtureLease{}, sandbox.ErrC4Execution)
			if !finished || e == nil || e.Error() != "native_v2_session_not_closed_owned" || s.Passed || s.Stage == "complete" || len(s.Operations) != 0 {
				t.Fatalf("expected execution error skipped native validation: finished=%v summary=%+v error=%v", finished, s, e)
			}
			storageErr := errors.New("storage write failed")
			for _, executionErr := range []error{storageErr, context.DeadlineExceeded, context.Canceled, errors.New("unrelated launch failure"), errors.New(sandbox.ErrC4Execution.Error()), fmt.Errorf("wrapped: %w", sandbox.ErrC4Execution), errors.Join(sandbox.ErrC4Execution, storageErr)} {
				finished, e = finishBaseline(context.Background(), &s, m, nil, f, root, root, root, nil, nil, executionErr)
				if !finished || e != executionErr || s.Passed {
					t.Fatalf("unexpected execution error was consumed: %v -> %v", executionErr, e)
				}
			}
		})
	}
}

func TestBaselineBoundaryPreservesPositiveAndV1ErrorPolicy(t *testing.T) {
	legacy := fixtureSpec{expectedTests: 2}
	node, _ := selectFixture("c4_node_model_v2", "workflow")
	typescript, _ := selectFixture("c4_typescript_model_v2", "workflow")
	python, _ := selectFixture("c4_python_model_v2", "workflow")
	descendant, _ := selectFixture("c4_python_model_v2", "descendant")
	unknown := node
	unknown.caseID, unknown.expectedClass = "unknown", "infrastructure_failed"
	wrongClass := descendant
	wrongClass.expectedClass = "infrastructure_failed"
	wrongTests, _ := selectFixture("c4_typescript_model_v2", "typecheck")
	wrongTests.expectedTests = 1
	for _, f := range []fixtureSpec{legacy, node, typescript, python, descendant, unknown, wrongClass, wrongTests} {
		o := operationFixture()[0]
		o.Result.Tests = f.expectedTests
		s, m := Summary{}, domain.Mission{C4Operations: []domain.C4Operation{o}}
		finished, e := finishBaseline(context.Background(), &s, m, nil, f, "", "", "", nil, nil, sandbox.ErrC4Execution)
		if !finished || e != sandbox.ErrC4Execution || s.Passed {
			t.Fatal("non-negative or unrecognized fixture consumed execution failure", f.profile, f.caseID, e)
		}
		if f.expectedClass == "" {
			finished, e = finishBaseline(context.Background(), &s, m, nil, f, "", "", "", nil, nil, nil)
			if finished || e != nil || s.Passed {
				t.Fatal("positive baseline did not continue unchanged", f.profile, e)
			}
			m.C4Operations[0].Result.Tests = 0
			finished, e = finishBaseline(context.Background(), &s, m, nil, f, "", "", "", nil, nil, nil)
			if !finished || e == nil || s.Passed {
				t.Fatal("positive baseline accepted zero genuine tests", f.profile)
			}
		}
	}
}
