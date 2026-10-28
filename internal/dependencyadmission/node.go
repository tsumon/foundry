package dependencyadmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"

	"foundry/internal/dependencybundle"
)

type nodePackage struct {
	name, version  string
	deps, devDeps  map[string]string
	metadataSHA256 string
	materialized   bool
}

var knownPackageFields = []string{"name", "version", "dependencies", "devDependencies", "peerDependencies", "peerDependenciesMeta", "optionalDependencies", "workspaces", "overrides", "bundledDependencies", "bundleDependencies", "os", "cpu", "libc", "scripts", "engines", "bin", "type", "exports", "imports", "license", "resolved", "integrity", "dev", "peer", "optional", "devOptional", "link", "inBundle", "hasShrinkwrap", "hasInstallScript", "funding", "deprecated"}

func parsePackage(raw []byte, limit int, locked bool) (*nodePackage, error) {
	if e := strictJSON(raw, limit); e != nil {
		return nil, e
	}
	m, e := object(bytes.TrimSpace(raw))
	if e != nil {
		return nil, e
	}
	for k := range m {
		known := false
		for _, field := range knownPackageFields {
			if strings.EqualFold(k, field) {
				if k != field {
					return nil, errInvalid
				}
				known = true
				break
			}
		}
		if locked && !known {
			return nil, errors.New("unsupported_lock_field")
		}
	}
	for _, key := range []string{"peerDependencies", "peerDependenciesMeta", "optionalDependencies", "workspaces", "overrides", "bundledDependencies", "bundleDependencies"} {
		if v, ok := m[key]; ok {
			var x any
			if json.Unmarshal(v, &x) != nil {
				return nil, errInvalid
			}
			empty := false
			switch x := x.(type) {
			case map[string]any:
				empty = len(x) == 0
			case []any:
				empty = len(x) == 0
			}
			if !empty {
				return nil, errors.New("unsupported_package_field")
			}
		}
	}
	for _, key := range []string{"os", "cpu", "libc"} {
		if _, ok := m[key]; ok {
			return nil, errors.New("unsupported_package_field")
		}
	}
	for _, key := range []string{"peer", "optional", "devOptional", "link", "inBundle", "hasShrinkwrap", "hasInstallScript"} {
		if v, ok := m[key]; ok {
			var flag bool
			if json.Unmarshal(v, &flag) != nil || bytes.Equal(v, []byte("null")) {
				return nil, errInvalid
			}
			if flag {
				return nil, errors.New("unsupported_package_field")
			}
		}
	}
	if v, ok := m["scripts"]; ok {
		scripts, e := object(bytes.TrimSpace(v))
		if e != nil {
			return nil, e
		}
		for name, raw := range scripts {
			var s string
			if json.Unmarshal(raw, &s) != nil || bytes.Equal(raw, []byte("null")) {
				return nil, errInvalid
			}
			switch strings.ToLower(name) {
			case "preinstall", "install", "postinstall", "prepare", "preprepare", "postprepare":
				return nil, errors.New("unsupported_install_hook")
			}
		}
	}
	if v, ok := m["engines"]; ok {
		engines, e := object(bytes.TrimSpace(v))
		if e != nil {
			return nil, e
		}
		for k := range engines {
			if strings.EqualFold(k, "node") && k != "node" {
				return nil, errInvalid
			}
		}
		if spec, ok := engines["node"]; ok {
			var s string
			if json.Unmarshal(spec, &s) != nil {
				return nil, errors.New("unsupported_engine_spec")
			}
			yes, e := satisfies(s, "24.22.1")
			if e != nil {
				return nil, errors.New("unsupported_engine_spec")
			}
			if !yes {
				return nil, errors.New("runtime_incompatible")
			}
		}
	}
	p := &nodePackage{}
	p.name, e = fieldString(m, "name")
	if e != nil {
		return nil, e
	}
	p.version, e = fieldString(m, "version")
	if e != nil {
		return nil, e
	}
	if p.name != "" && !validName(p.name) || p.version != "" && !stableVersion(p.version) {
		return nil, errInvalid
	}
	p.deps, e = dependencyMap(m, "dependencies")
	if e != nil {
		return nil, e
	}
	p.devDeps, e = dependencyMap(m, "devDependencies")
	if e != nil {
		return nil, e
	}
	if locked {
		resolved, e := fieldString(m, "resolved")
		if e != nil {
			return nil, e
		}
		if resolved != "" {
			u, e := url.Parse(resolved)
			if e != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(resolved, "?#") {
				return nil, errors.New("unsupported_resolved_claim")
			}
		}
		integrity, e := fieldString(m, "integrity")
		if e != nil {
			return nil, e
		}
		if integrity != "" {
			if !validIntegrity(integrity) {
				return nil, errors.New("invalid_integrity_claim")
			}
		}
	}
	return p, nil
}
func validIntegrity(s string) bool {
	if len(s) > 1024 {
		return false
	}
	tokens := strings.Fields(s)
	if len(tokens) == 0 || len(tokens) > 8 {
		return false
	}
	for _, token := range tokens {
		algorithm, encoded, ok := strings.Cut(token, "-")
		if !ok {
			return false
		}
		size := 0
		switch algorithm {
		case "sha1":
			size = 20
		case "sha256":
			size = 32
		case "sha384":
			size = 48
		case "sha512":
			size = 64
		default:
			return false
		}
		b, e := base64.StdEncoding.Strict().DecodeString(encoded)
		if e != nil || len(b) != size || base64.StdEncoding.EncodeToString(b) != encoded {
			return false
		}
	}
	return true
}
func parseNodeLock(raw []byte) (*nodePackage, map[string]*nodePackage, error) {
	if e := strictJSON(raw, maxRootJSON); e != nil {
		return nil, nil, e
	}
	m, e := object(bytes.TrimSpace(raw))
	if e != nil {
		return nil, nil, e
	}
	for k := range m {
		switch k {
		case "name", "version", "lockfileVersion", "requires", "packages", "dependencies":
		default:
			return nil, nil, errors.New("unsupported_lock_field")
		}
	}
	if !bytes.Equal(bytes.TrimSpace(m["lockfileVersion"]), []byte("3")) {
		return nil, nil, errInvalid
	}
	for _, key := range []string{"name", "version"} {
		if _, e := fieldString(m, key); e != nil {
			return nil, nil, e
		}
	}
	if v, ok := m["requires"]; ok {
		var required bool
		if json.Unmarshal(v, &required) != nil || bytes.Equal(v, []byte("null")) {
			return nil, nil, errInvalid
		}
	}
	if v, ok := m["dependencies"]; ok {
		legacy, e := object(bytes.TrimSpace(v))
		if e != nil || len(legacy) != 0 {
			return nil, nil, errors.New("unsupported_lock_field")
		}
	}
	// Stream the location map so package count is checked before allocating the
	// next package record. The strict pass already rejected duplicate keys.
	d := json.NewDecoder(bytes.NewReader(m["packages"]))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, nil, errInvalid
	}
	packages := map[string]*nodePackage{}
	var root *nodePackage
	for d.More() {
		t, e = d.Token()
		loc, ok := t.(string)
		if e != nil || !ok {
			return nil, nil, errInvalid
		}
		if loc != "" && len(packages) >= maxPackages {
			return nil, nil, errInvalid
		}
		limit := maxPackageJSON
		if loc == "" {
			limit = maxRootJSON
		}
		var record json.RawMessage
		if d.Decode(&record) != nil || len(record) > limit {
			return nil, nil, errInvalid
		}
		p, e := parsePackage(record, limit, true)
		if e != nil {
			return nil, nil, e
		}
		if loc == "" {
			root = p
			continue
		}
		name, e := locationName(loc)
		if e != nil || p.name != "" && p.name != name || !stableVersion(p.version) {
			return nil, nil, errInvalid
		}
		p.name = name
		packages[loc] = p
	}
	if root == nil {
		return nil, nil, errInvalid
	}
	for _, key := range []string{"name", "version"} {
		if _, ok := m[key]; ok {
			v, e := fieldString(m, key)
			if e != nil || key == "name" && v != root.name || key == "version" && v != root.version {
				return nil, nil, errors.New("root_lock_mismatch")
			}
		}
	}
	return root, packages, nil
}

// payloadOwner derives every installed package boundary, including undeclared
// nested node_modules. A file under an unknown nested package cannot hide under
// its declared ancestor's prefix.
func payloadOwner(p string) (string, string, error) {
	parts := strings.Split(p, "/")
	i := 0
	loc := ""
	for {
		if i >= len(parts) {
			return "", "", errInvalid
		}
		name := parts[i]
		i++
		if strings.HasPrefix(name, "@") {
			if i >= len(parts) {
				return "", "", errInvalid
			}
			name += "/" + parts[i]
			i++
		}
		if !validName(name) {
			return "", "", errInvalid
		}
		if loc == "" {
			loc = "node_modules/" + name
		} else {
			loc += "/node_modules/" + name
		}
		for j := i; j < len(parts); j++ {
			if parts[j] == "node_modules" {
				if j != i {
					return "", "", errInvalid
				}
				i = j + 1
				break
			}
			if j == len(parts)-1 {
				return loc, strings.Join(parts[i:], "/"), nil
			}
		}
		if i >= len(parts) {
			return "", "", errInvalid
		}
	}
}
func forbiddenPayload(p string, b []byte) bool {
	for _, part := range strings.Split(p, "/") {
		if strings.EqualFold(part, ".bin") {
			return true
		}
	}
	base := strings.ToLower(path.Base(p))
	if base == "binding.gyp" || base == "npm-shrinkwrap.json" {
		return true
	}
	for _, suffix := range []string{".node", ".so", ".dylib", ".dll", ".exe", ".o", ".a", ".lib"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	if strings.Contains(base, ".so.") {
		return true
	}
	for _, magic := range [][]byte{{0x7f, 'E', 'L', 'F'}, {'M', 'Z'}, {0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe}, {0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}, {0xbe, 0xba, 0xfe, 0xca}, {0xca, 0xfe, 0xba, 0xbf}, {0xbf, 0xba, 0xfe, 0xca}, []byte("!<arch>\n")} {
		if bytes.HasPrefix(b, magic) {
			return true
		}
	}
	return false
}

// ImportNode checks exact committed root metadata against a separately supplied
// frozen tree. No package commands, resolver, network or dynamic code is used.
func ImportNode(ctx context.Context, storeRoot, nodeModulesDir string, source NodeSource) (AdmissionReceipt, error) {
	if e := ctx.Err(); e != nil {
		return AdmissionReceipt{}, e
	}
	packageJSON, e := ownedSource(source.PackageJSON, "package.json")
	if e != nil {
		return AdmissionReceipt{}, e
	}
	packageLock, e := ownedSource(source.PackageLock, "package-lock.json")
	if e != nil {
		return AdmissionReceipt{}, e
	}
	root, e := parsePackage(packageJSON.Data, maxRootJSON, false)
	if e != nil {
		return AdmissionReceipt{}, e
	}
	locked, packages, e := parseNodeLock(packageLock.Data)
	if e != nil {
		return AdmissionReceipt{}, e
	}
	if root.name != locked.name || root.version != locked.version || !equalMaps(root.deps, locked.deps) || !equalMaps(root.devDeps, locked.devDeps) {
		return AdmissionReceipt{}, errors.New("root_lock_mismatch")
	}
	receipt := AdmissionReceipt{SchemaVersion: 1, Policy: nodePolicy, SourceFiles: []SourceDigest{sourceDigest(packageJSON), sourceDigest(packageLock)}, Packages: []PackageIdentity{}, Edges: []DependencyEdge{}, Provenance: "user_supplied_tree"}
	manifest, e := dependencybundle.InspectFrozen(ctx, nodeModulesDir, "node", sum(packageLock.Data), func(f dependencybundle.File, b []byte) error {
		if forbiddenPayload(f.Path, b) {
			return errors.New("unsupported_dependency_payload")
		}
		if f.Path == ".package-lock.json" {
			return nil
		}
		loc, relative, e := payloadOwner(f.Path)
		if e != nil {
			return e
		}
		p, ok := packages[loc]
		if !ok {
			return errors.New("extraneous_package")
		}
		if relative == "package.json" {
			metadata, e := parsePackage(b, maxPackageJSON, false)
			if e != nil {
				return e
			}
			if p.materialized || metadata.name != p.name || metadata.version != p.version || !equalMaps(metadata.deps, p.deps) {
				return errors.New("package_lock_mismatch")
			}
			p.materialized = true
			p.metadataSHA256 = f.SHA256
		}
		return nil
	})
	if e != nil {
		return AdmissionReceipt{}, e
	}
	for loc, p := range packages {
		if !p.materialized {
			return AdmissionReceipt{}, errors.New("missing_package")
		}
		receipt.Packages = append(receipt.Packages, PackageIdentity{Location: loc, Name: p.name, Version: p.version, MetadataSHA256: p.metadataSHA256, ArtifactClaim: "unverified_lock_claim"})
	}
	receipt.Edges, e = nodeEdges(root, packages)
	if e != nil {
		return AdmissionReceipt{}, e
	}
	canonicalReceipt(&receipt)
	checked, e := manifest.Binding()
	if e != nil {
		return AdmissionReceipt{}, e
	}
	receipt, e = finishReceipt(receipt, checked, nil)
	if e != nil {
		return AdmissionReceipt{}, e
	}
	binding, e := dependencybundle.ImportFrozen(ctx, storeRoot, nodeModulesDir, manifest)
	return finishReceipt(receipt, binding, e)
}
