// Package dependencyadmission validates frozen ecosystem inputs before publishing
// a generic dependency bundle. Admission grants no execution authority.
package dependencyadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"foundry/internal/dependencybundle"
)

const (
	maxRootJSON    = 1 << 20
	maxPackageJSON = 256 << 10
	maxPackages    = 512
	maxEdges       = 4096
	nodePolicy     = "node-lock-v3-ranges-v1"
)

var errInvalid = errors.New("invalid_dependency_input")

type SourceBytes struct {
	Path string
	Data []byte
}
type NodeSource struct{ PackageJSON, PackageLock SourceBytes }
type SourceDigest struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type PackageIdentity struct {
	Location       string `json:"location"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	MetadataSHA256 string `json:"metadata_sha256"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ArtifactSize   int64  `json:"artifact_size"`
	ArtifactClaim  string `json:"artifact_claim"`
}
type DependencyEdge struct {
	From        string `json:"from"`
	Name        string `json:"name"`
	To          string `json:"to"`
	Requirement string `json:"requirement"`
	Status      string `json:"status"`
}
type AdmissionReceipt struct {
	SchemaVersion int                      `json:"schema_version"`
	Policy        string                   `json:"policy"`
	Bundle        dependencybundle.Binding `json:"bundle"`
	SourceFiles   []SourceDigest           `json:"source_files"`
	Packages      []PackageIdentity        `json:"packages"`
	Edges         []DependencyEdge         `json:"edges"`
	Provenance    string                   `json:"provenance"`
	Digest        string                   `json:"digest,omitempty"`
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func edgeLess(a, b DependencyEdge) bool {
	for _, p := range [][2]string{{a.From, b.From}, {a.Name, b.Name}, {a.To, b.To}, {a.Requirement, b.Requirement}, {a.Status, b.Status}} {
		if p[0] != p[1] {
			return p[0] < p[1]
		}
	}
	return false
}
func packageLess(a, b PackageIdentity) bool {
	for _, p := range [][2]string{{a.Location, b.Location}, {a.Name, b.Name}, {a.Version, b.Version}} {
		if p[0] != p[1] {
			return p[0] < p[1]
		}
	}
	return false
}
func (r AdmissionReceipt) computeDigest() string {
	r.Digest = ""
	raw, _ := json.Marshal(r)
	return sum(append([]byte("foundry:dependency-admission:v1\n"), raw...))
}

// Validate checks canonical retained facts and their digest. It neither reads a
// bundle nor authorizes execution; callers must check the binding independently.
func (r AdmissionReceipt) Validate() error {
	if r.SchemaVersion != 1 || r.Policy != nodePolicy || r.Provenance != "user_supplied_tree" || r.SourceFiles == nil || len(r.SourceFiles) != 2 || r.Packages == nil || len(r.Packages) > maxPackages || r.Edges == nil || len(r.Edges) > maxEdges {
		return errInvalid
	}
	b := r.Bundle
	if b.SchemaVersion != 1 || b.Ecosystem != "node" || !validHash(b.BundleDigest) || !validHash(b.ManifestDigest) || b.FileCount < 0 || b.FileCount > dependencybundle.MaxFiles || b.TotalBytes < 0 || b.TotalBytes > dependencybundle.MaxPayloadBytes {
		return errInvalid
	}
	for i, s := range r.SourceFiles {
		if s.Path != []string{"package-lock.json", "package.json"}[i] || s.Bytes <= 0 || s.Bytes > maxRootJSON || !validHash(s.SHA256) {
			return errInvalid
		}
	}
	locations := map[string]bool{"": true}
	packages := map[string]*nodePackage{}
	for i, p := range r.Packages {
		n, e := locationName(p.Location)
		if e != nil || n != p.Name || !stableVersion(p.Version) || !validHash(p.MetadataSHA256) || p.ArtifactSHA256 != "" || p.ArtifactSize != 0 || p.ArtifactClaim != "unverified_lock_claim" || i > 0 && !packageLess(r.Packages[i-1], p) || locations[p.Location] {
			return errInvalid
		}
		locations[p.Location] = true
		packages[p.Location] = &nodePackage{name: p.Name, version: p.Version}
	}
	for i, e := range r.Edges {
		if !locations[e.From] || !locations[e.To] || e.To == "" || !validName(e.Name) || e.Status != "active" || i > 0 && edgeLess(e, r.Edges[i-1]) {
			return errInvalid
		}
		target := packages[e.To]
		if target == nil || target.name != e.Name || nearest(e.From, e.Name, packages) != e.To {
			return errInvalid
		}
		if yes, er := satisfies(e.Requirement, target.version); er != nil || !yes {
			return errInvalid
		}
	}
	seen := map[string]bool{"": true}
	byFrom := map[string][]string{}
	for _, e := range r.Edges {
		byFrom[e.From] = append(byFrom[e.From], e.To)
	}
	queue := []string{""}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range byFrom[from] {
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
			}
		}
	}
	if len(seen) != len(locations) {
		return errInvalid
	}
	if !validHash(r.Digest) || r.Digest != r.computeDigest() {
		return errInvalid
	}
	return nil
}
func finishReceipt(r AdmissionReceipt, b dependencybundle.Binding, e error) (AdmissionReceipt, error) {
	if e != nil {
		if errors.Is(e, dependencybundle.ErrPublicationUnknown) {
			r.Bundle = b
			r.Digest = ""
			return r, e
		}
		return AdmissionReceipt{}, e
	}
	r.Bundle = b
	r.Digest = r.computeDigest()
	if e = r.Validate(); e != nil {
		return AdmissionReceipt{}, e
	}
	return r, nil
}
func canonicalReceipt(r *AdmissionReceipt) {
	sort.Slice(r.SourceFiles, func(i, j int) bool { return r.SourceFiles[i].Path < r.SourceFiles[j].Path })
	sort.Slice(r.Packages, func(i, j int) bool { return packageLess(r.Packages[i], r.Packages[j]) })
	sort.SliceStable(r.Edges, func(i, j int) bool { return edgeLess(r.Edges[i], r.Edges[j]) })
}
func ownedSource(s SourceBytes, path string) (SourceBytes, error) {
	if s.Path != path || len(s.Data) == 0 || len(s.Data) > maxRootJSON {
		return SourceBytes{}, errInvalid
	}
	return SourceBytes{Path: path, Data: append([]byte(nil), s.Data...)}, nil
}
func sourceDigest(s SourceBytes) SourceDigest {
	return SourceDigest{Path: s.Path, Bytes: int64(len(s.Data)), SHA256: sum(s.Data)}
}
