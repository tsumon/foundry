package engine

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/workspace"
)

type AppPreview struct {
	HTML            string `json:"html"`
	CandidateDigest string `json:"candidate_digest"`
}

func (e *Engine) ideaFiles(id string) (domain.Mission, map[string][]byte, error) {
	m, err := e.s.GetMission(id)
	if err != nil {
		return m, nil, err
	}
	if err = e.validateIdeaOrigin(m); err != nil {
		return m, nil, err
	}
	c, _, _, err := e.resolveV2MaterialMode(context.Background(), m, false)
	if err != nil {
		return m, nil, err
	}
	root, err := e.selectedCandidate(m)
	if err != nil {
		return m, nil, err
	}
	files, err := workspace.ReadAppFiles(root, m.CandidateDigest, c)
	if err != nil {
		return m, nil, err
	}
	return m, files, nil
}
func (e *Engine) AppPreview(id string) (AppPreview, error) {
	m, files, err := e.ideaFiles(id)
	if err != nil {
		return AppPreview{}, err
	}
	html, err := workspace.BundleBrowserApp(files)
	return AppPreview{html, m.CandidateDigest}, err
}
func (e *Engine) AppSource(id string) ([]byte, error) {
	m, files, err := e.ideaFiles(id)
	if err != nil {
		return nil, err
	}
	if err = e.verifyDelivery(m); err != nil {
		return nil, err
	}
	if _, err = workspace.BundleBrowserApp(files); err != nil {
		return nil, err
	}
	return workspace.ZipAppFiles(files)
}
