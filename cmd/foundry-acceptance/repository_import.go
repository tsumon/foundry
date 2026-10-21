package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"foundry/internal/domain"
	"foundry/internal/engine"
)

// This fixture runs only in the disposable acceptance command. No production
// endpoint exposes corruption, journal reservation, or preapproval shortcuts.
func seedRepositoryImportScenarios(r *acceptanceRuntime) error {
	ctx := context.Background()
	catalog, err := r.store.Catalog()
	if err != nil {
		return err
	}
	projectID := "synthetic-c3-project"
	_, err = r.store.UpdateCatalog(catalog.Revision, "project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
		c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: projectID}, Name: "SYNTHETIC C3 local import", Description: "Disposable Git + SQLite + synthetic models/content checks. Docker and C4 not_checked."})
		return []string{projectID}, nil
	})
	if err != nil {
		return err
	}
	// Keep the historical controlled-mode registry/observation pair immutable.
	source, err := r.store.SaveGitRepository(domain.GitRepository{ID: "synthetic-c3-source", Name: "SYNTHETIC C3 disposable source", Path: r.Repo, ProjectID: projectID}, 0)
	if err != nil {
		return err
	}
	r.scenarios["repository_import_repository"] = source.ID
	r.scenarios["repository_import_project"] = projectID
	draft, err := createImportedFixture(r, "SYNTHETIC C3 imported draft — explicit C2 replan", codingReplanCreateRequest(r.Repo))
	if err != nil {
		return err
	}
	r.scenarios["repository_import_draft"] = draft.ID

	req := codingReplanCreateRequest(r.Repo)
	receipt, err := r.engine.InspectRepository(ctx, fixtureInspection(req))
	if err != nil {
		return err
	}
	operation, err := r.store.BeginRepositoryImport(ctx, domain.RepositoryImportOperation{ID: "synthetic-c3-interrupted", InspectionID: receipt.ID, MissionID: "synthetic-c3-no-mission", IntentDigest: strings.Repeat("d", 64)}, receipt, r.Repo)
	if err != nil {
		return err
	}
	r.scenarios["repository_import_interrupted_operation"] = operation.ID

	binaryPath := filepath.Join(r.Root, "synthetic-binary-repository")
	if err = seedRepository(binaryPath); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(binaryPath, "src/binary.bin"), []byte{0, 1, 2, 0}, 0600); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "src/binary.bin"}, {"-c", "user.name=Synthetic Acceptance", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "SYNTHETIC unsupported binary"}} {
		command := exec.Command("/usr/bin/git", args...)
		command.Dir = binaryPath
		command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
		if raw, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("seed synthetic binary: %w %s", err, raw)
		}
	}
	binary, err := r.store.SaveGitRepository(domain.GitRepository{ID: "synthetic-c3-binary", Name: "SYNTHETIC binary admission blocker (not inspected)", Path: binaryPath, ProjectID: projectID}, 0)
	if err != nil {
		return err
	}
	r.scenarios["repository_import_binary_repository"] = binary.ID

	// Real engine transitions create sealed evidence first. This isolated task is
	// then tampered for read-only diagnosis, never a reusable approval fixture.
	corrupt, err := createImportedFixture(r, "SYNTHETIC C3 isolated corrupt evidence — diagnostic only", codingReplanCreateRequest(r.Repo))
	if err != nil {
		return err
	}
	for round := 0; round < 2; round++ {
		if _, err = r.engine.Plan(ctx, corrupt.ID, corrupt.Revision); err != nil {
			return err
		}
		corrupt, err = waitSyntheticMission(r.store, corrupt.ID, "awaiting_approval", "blocked")
		if err != nil {
			return err
		}
		if corrupt.State != "awaiting_approval" {
			return fmt.Errorf("C3 fixture plan blocked: %s", corrupt.Error)
		}
		if _, err = r.engine.Approve(ctx, corrupt.ID, corrupt.Revision); err != nil {
			return err
		}
		corrupt, err = waitSyntheticMission(r.store, corrupt.ID, "blocked", "awaiting_delivery")
		if err != nil {
			return err
		}
	}
	if corrupt.State != "awaiting_delivery" || len(corrupt.Evidence) == 0 {
		return fmt.Errorf("C3 corrupt fixture requires actual synthetic evidence: %s", corrupt.Error)
	}
	_, err = r.store.UpdateMission(corrupt.ID, corrupt.Revision, "synthetic_fixture_evidence_corrupted", func(m *domain.Mission) error {
		m.Evidence[0].Results[0].Output = "SYNTHETIC deliberate isolated evidence corruption"
		return nil
	})
	if err != nil {
		return err
	}
	r.scenarios["repository_import_corrupt_evidence"] = corrupt.ID
	return nil
}

func fixtureInspection(req engine.CreateRequest) domain.RepositoryInspectionRequest {
	return domain.RepositoryInspectionRequest{RepoPath: req.RepoPath, Consent: true, Spec: domain.RepositoryImportSpec{Scope: req.Spec.Scope, Protected: []string{}, Acceptance: req.Spec.Acceptance, Image: req.Spec.Image}}
}
func createImportedFixture(r *acceptanceRuntime, title string, req engine.CreateRequest) (domain.Mission, error) {
	receipt, err := r.engine.InspectRepository(context.Background(), fixtureInspection(req))
	if err != nil {
		return domain.Mission{}, err
	}
	if receipt.Status != "ready" {
		return domain.Mission{}, fmt.Errorf("C3 fixture admission blocked: %s", receipt.Code)
	}
	req.Title, req.ImportVersion = title, 1
	req.ImportSelection = &domain.ImportSelection{InspectionID: receipt.ID, Digest: receipt.Digest}
	return r.engine.Create(context.Background(), req)
}
