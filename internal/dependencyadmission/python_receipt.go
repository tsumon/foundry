package dependencyadmission

import (
	"strings"

	"foundry/internal/dependencybundle"
)

func (r AdmissionReceipt) validatePython() error {
	if r.SchemaVersion != 1 || r.Policy != pythonPolicy || r.Provenance != "user_supplied_wheels" || len(r.SourceFiles) != 1 || r.Packages == nil || len(r.Packages) > maxPackages || r.Edges == nil || len(r.Edges) > maxEdges {
		return errInvalid
	}
	b := r.Bundle
	if b.SchemaVersion != 1 || b.Ecosystem != "python" || !validHash(b.BundleDigest) || !validHash(b.ManifestDigest) || b.FileCount < 0 || b.FileCount > dependencybundle.MaxFiles || b.TotalBytes < 0 || b.TotalBytes > dependencybundle.MaxPayloadBytes {
		return errInvalid
	}
	s := r.SourceFiles[0]
	if s.Path != pythonLockPath || s.Bytes <= 0 || s.Bytes > maxRootJSON || !validHash(s.SHA256) {
		return errInvalid
	}
	byLocation := map[string]PackageIdentity{}
	byName := map[string]PackageIdentity{}
	var compressed int64
	for i, p := range r.Packages {
		if !pythonName(p.Name) || !pythonPath(p.Location) || !validHash(p.MetadataSHA256) || !validHash(p.RecordSHA256) || !validHash(p.ArtifactSHA256) || p.ArtifactSize <= 0 || p.ArtifactSize > dependencybundle.MaxPayloadBytes || p.ArtifactClaim != "user_supplied_hash_verified" || i > 0 && !packageLess(r.Packages[i-1], p) {
			return errInvalid
		}
		if _, e := pythonVersion(p.Version); e != nil {
			return errInvalid
		}
		if p.Location != pythonLocation(pythonWheel{Name: p.Name, Version: p.Version}) || byName[p.Name].Name != "" {
			return errInvalid
		}
		compressed += p.ArtifactSize
		if compressed > dependencybundle.MaxPayloadBytes {
			return errInvalid
		}
		byName[p.Name] = p
		byLocation[p.Location] = p
	}
	roots := map[string]bool{}
	for i, e := range r.Edges {
		if !pythonName(e.Name) || e.From != "" && byLocation[e.From].Name == "" || i > 0 && edgeLess(e, r.Edges[i-1]) {
			return errInvalid
		}
		spec := ""
		if e.From == "" {
			if e.Status != "active" || roots[e.Name] || !strings.HasPrefix(e.Requirement, "==") {
				return errInvalid
			}
			roots[e.Name] = true
			if e.Requirement != "=="+byName[e.Name].Version {
				return errInvalid
			}
			spec = e.Requirement
		} else {
			extras := []string{}
			if e.Status == "inactive_extra" {
				_, marker, ok := strings.Cut(e.Requirement, ";")
				if !ok {
					return errInvalid
				}
				marker = strings.TrimSpace(marker)
				if strings.HasPrefix(marker, "(") && strings.HasSuffix(marker, ")") {
					marker = strings.TrimSpace(marker[1 : len(marker)-1])
				}
				m := pythonExtraMarker.FindStringSubmatch(marker)
				if m == nil {
					return errInvalid
				}
				extras = append(extras, normalizePythonName(m[2]))
			}
			name, parsed, status, er := pythonRequirement(e.Requirement, extras)
			if er != nil || name != e.Name || status != e.Status {
				return errInvalid
			}
			spec = parsed
		}
		switch e.Status {
		case "active":
			target, ok := byLocation[e.To]
			if !ok || target.Name != e.Name {
				return errInvalid
			}
			if spec != "" {
				yes, er := pythonSatisfies(spec, target.Version)
				if er != nil || !yes {
					return errInvalid
				}
			}
		case "inactive_extra":
			if e.To != "" || e.From == "" {
				return errInvalid
			}
		default:
			return errInvalid
		}
	}
	seen := map[string]bool{"": true}
	for changed := true; changed; {
		changed = false
		for _, e := range r.Edges {
			if e.Status == "active" && seen[e.From] && !seen[e.To] {
				seen[e.To] = true
				changed = true
			}
		}
	}
	if len(seen) != len(r.Packages)+1 || !validHash(r.Digest) || r.Digest != r.computeDigest() {
		return errInvalid
	}
	return nil
}
