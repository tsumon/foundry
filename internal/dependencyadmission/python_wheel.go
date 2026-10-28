package dependencyadmission

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"foundry/internal/dependencybundle"
	"foundry/internal/platformfs"
)

const pythonPolicy = "python-wheel-numeric-v1"
const pythonLockPath = "foundry.python.lock.json"

type admittedWheel struct {
	identity     PackageIdentity
	requirements []string
	extras       []string
	files        map[string][]byte
}

func pythonLocation(w pythonWheel) string {
	return strings.ReplaceAll(w.Name, "-", "_") + "-" + w.Version + ".dist-info"
}
func pythonPath(p string) bool {
	if p == "" || len(p) > 1024 || platformfs.WindowsRelative(p) != nil {
		return false
	}
	parts := strings.Split(p, "/")
	if len(parts) > 32 {
		return false
	}
	for _, part := range parts {
		if len(part) > 128 {
			return false
		}
	}
	for _, c := range []byte(p) {
		if c < 32 || c > 126 || c == '\\' || c == ':' {
			return false
		}
	}
	return true
}

// Bound the central directory before archive/zip allocates its File slice. ZIP64,
// multi-disk and prefixed archives are outside this bounded wheel subset.
func wheelZIPCount(b []byte) (int, error) {
	if len(b) < 22 {
		return 0, errInvalid
	}
	end := -1
	for i := len(b) - 22; i >= 0 && i >= len(b)-22-65535; i-- {
		if binary.LittleEndian.Uint32(b[i:]) == 0x06054b50 && i+22+int(binary.LittleEndian.Uint16(b[i+20:])) == len(b) {
			end = i
			break
		}
	}
	// archive/zip accepts a later EOCD whose comment merely fits before EOF.
	// Exclude later signatures, even incomplete ones, from this supported subset
	// so decoding cannot select a different directory or allocate from its count.
	// Ordinary archive comments remain supported and part of the original hash.
	if end < 0 || bytes.Contains(b[end+4:], []byte{'P', 'K', 5, 6}) {
		return 0, errInvalid
	}
	e := b[end:]
	n := int(binary.LittleEndian.Uint16(e[10:]))
	size := uint64(binary.LittleEndian.Uint32(e[12:]))
	offset := uint64(binary.LittleEndian.Uint32(e[16:]))
	if binary.LittleEndian.Uint16(e[4:]) != 0 || binary.LittleEndian.Uint16(e[6:]) != 0 || int(binary.LittleEndian.Uint16(e[8:])) != n || n == 0 || n > dependencybundle.MaxFiles || offset+size != uint64(end) || offset > uint64(len(b)) || binary.LittleEndian.Uint32(b) != 0x04034b50 {
		return 0, errInvalid
	}
	pos := int(offset)
	spans := make([][2]int, 0, n)
	for i := 0; i < n; i++ {
		if pos+46 > end || binary.LittleEndian.Uint32(b[pos:]) != 0x02014b50 {
			return 0, errInvalid
		}
		h := b[pos:]
		next := pos + 46 + int(binary.LittleEndian.Uint16(h[28:])) + int(binary.LittleEndian.Uint16(h[30:])) + int(binary.LittleEndian.Uint16(h[32:]))
		if next > end || binary.LittleEndian.Uint16(h[34:]) != 0 || binary.LittleEndian.Uint32(h[42:]) >= uint32(offset) {
			return 0, errInvalid
		}
		local := int(binary.LittleEndian.Uint32(h[42:]))
		nameLen, extraLen := int(binary.LittleEndian.Uint16(h[28:])), int(binary.LittleEndian.Uint16(h[30:]))
		if local+30 > int(offset) || binary.LittleEndian.Uint32(b[local:]) != 0x04034b50 {
			return 0, errInvalid
		}
		lh := b[local:]
		localName, localExtra := int(binary.LittleEndian.Uint16(lh[26:])), int(binary.LittleEndian.Uint16(lh[28:]))
		start := local + 30 + localName + localExtra
		compressed := binary.LittleEndian.Uint32(h[20:])
		expanded := binary.LittleEndian.Uint32(h[24:])
		crc := binary.LittleEndian.Uint32(h[16:])
		if start > int(offset) || compressed == 0xffffffff || expanded == 0xffffffff || uint64(start)+uint64(compressed) > offset || localName != nameLen || !bytes.Equal(lh[30:30+localName], h[46:46+nameLen]) || !bytes.Equal(lh[6:10], h[8:12]) || !wheelZIPExtras(h[46+nameLen:46+nameLen+extraLen]) || !wheelZIPExtras(lh[30+localName:30+localName+localExtra]) {
			return 0, errInvalid
		}
		finish := start + int(compressed)
		if binary.LittleEndian.Uint16(h[8:])&8 != 0 {
			if finish+12 > int(offset) {
				return 0, errInvalid
			}
			if binary.LittleEndian.Uint32(b[finish:]) == 0x08074b50 {
				finish += 4
			}
			if finish+12 > int(offset) || binary.LittleEndian.Uint32(b[finish:]) != crc || binary.LittleEndian.Uint32(b[finish+4:]) != compressed || binary.LittleEndian.Uint32(b[finish+8:]) != expanded {
				return 0, errInvalid
			}
			finish += 12
			if x := binary.LittleEndian.Uint32(lh[14:]); x != 0 && x != crc {
				return 0, errInvalid
			}
			if x := binary.LittleEndian.Uint32(lh[18:]); x != 0 && x != compressed {
				return 0, errInvalid
			}
			if x := binary.LittleEndian.Uint32(lh[22:]); x != 0 && x != expanded {
				return 0, errInvalid
			}
		} else if binary.LittleEndian.Uint32(lh[14:]) != crc || binary.LittleEndian.Uint32(lh[18:]) != compressed || binary.LittleEndian.Uint32(lh[22:]) != expanded {
			return 0, errInvalid
		}
		spans = append(spans, [2]int{local, finish})
		pos = next
	}
	if pos != end {
		return 0, errInvalid
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	endLocal := 0
	for _, span := range spans {
		if span[0] != endLocal {
			return 0, errInvalid
		}
		endLocal = span[1]
	}
	if endLocal != int(offset) {
		return 0, errInvalid
	}
	return n, nil
}
func wheelZIPExtras(b []byte) bool {
	for len(b) > 0 {
		if len(b) < 4 {
			return false
		}
		id := binary.LittleEndian.Uint16(b)
		n := int(binary.LittleEndian.Uint16(b[2:]))
		if id == 1 || n > len(b)-4 {
			return false
		}
		b = b[4+n:]
	}
	return true
}
func pythonForbidden(p string, b []byte) bool {
	if forbiddenPayload(p, b) {
		return true
	}
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		if strings.HasSuffix(part, ".data") || strings.HasSuffix(part, ".egg-info") || strings.HasSuffix(part, ".egg") || part == "__pycache__" || part == "sitecustomize" || part == "usercustomize" || part == "scripts" {
			return true
		}
	}
	base := strings.ToLower(path.Base(p))
	if base == "entry_points.txt" || base == "record.jws" || base == "record.p7s" || base == "sitecustomize.py" || base == "usercustomize.py" {
		return true
	}
	for _, suffix := range []string{".sh", ".bash", ".zsh", ".fish", ".cmd", ".bat", ".ps1", ".psm1", ".vbs", ".com", ".obj", ".rar", ".zst", ".zstd", ".lz4", ".cab", ".pth", ".egg-link", ".pyc", ".pyo", ".pyd", ".zip", ".whl", ".egg", ".tar", ".tgz", ".gz", ".bz2", ".xz", ".7z"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, magic := range [][]byte{[]byte("PK\x03\x04"), []byte("PK\x05\x06"), {0x1f, 0x8b}, []byte("BZh"), []byte("Rar!\x1a\x07"), {0x28, 0xb5, 0x2f, 0xfd}, {0x04, 0x22, 0x4d, 0x18}, {0xfd, '7', 'z', 'X', 'Z', 0}, {'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}} {
		if bytes.HasPrefix(b, magic) {
			return true
		}
	}
	return len(b) >= 262 && string(b[257:262]) == "ustar" || bytes.HasPrefix(b, []byte("#!")) || len(b) >= 4 && b[1] == 0x0d && b[2] == 0x0d && b[3] == 0x0a
}
func admitWheel(ctx context.Context, w pythonWheel, b []byte, total *int64, entries *int) (admittedWheel, error) {
	out := admittedWheel{files: map[string][]byte{}}
	if int64(len(b)) != w.Size || sum(b) != w.SHA256 {
		return out, errors.New("artifact_hash_mismatch")
	}
	n, e := wheelZIPCount(b)
	if e != nil || n > dependencybundle.MaxFiles-*entries {
		return out, errors.New("unsupported_wheel")
	}
	*entries += n
	zr, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil || len(zr.File) != n {
		return out, errors.New("unsupported_wheel")
	}
	aliases := map[string]string{}
	dirs := map[string]bool{}
	usedDirs := map[string]bool{}
	explicit := map[string]bool{}
	location := pythonLocation(w)
	for _, f := range zr.File {
		if e := ctx.Err(); e != nil {
			return out, e
		}
		p := f.Name
		isDir := strings.HasSuffix(p, "/")
		if isDir {
			p = strings.TrimSuffix(p, "/")
		}
		if !pythonPath(p) || f.Flags&^uint16(0x808) != 0 || f.Method != zip.Store && f.Method != zip.Deflate || f.Mode()&(os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeCharDevice|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || isDir != f.Mode().IsDir() || !isDir && (!f.Mode().IsRegular() || f.Mode().Perm()&0111 != 0) || f.UncompressedSize64 > uint64(dependencybundle.MaxFileBytes) {
			return out, errors.New("unsafe_dependency_layout")
		}
		if explicit[p] {
			return out, errors.New("unsafe_dependency_layout")
		}
		explicit[p] = true
		parts := strings.Split(p, "/")
		for i := 1; i <= len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			key := strings.ToLower(prefix)
			if old, ok := aliases[key]; ok && old != prefix {
				return out, errors.New("unsafe_dependency_layout")
			}
			aliases[key] = prefix
			if i < len(parts) || isDir {
				if _, ok := out.files[prefix]; ok {
					return out, errors.New("unsafe_dependency_layout")
				}
				dirs[prefix] = true
				if !isDir {
					usedDirs[prefix] = true
				}
			}
			if strings.HasSuffix(strings.ToLower(parts[i-1]), ".dist-info") && (i != 1 || parts[i-1] != location) {
				return out, errors.New("package_identity_mismatch")
			}
		}
		if !isDir && dirs[p] {
			return out, errors.New("unsafe_dependency_layout")
		}
		if pythonForbidden(p, nil) {
			return out, errors.New("unsupported_wheel")
		}
		if isDir && f.UncompressedSize64 != 0 {
			return out, errors.New("unsupported_wheel")
		}
		if f.UncompressedSize64 > uint64(dependencybundle.MaxPayloadBytes-*total) {
			return out, errors.New("unsupported_wheel")
		}
		if (p == location+"/METADATA" || p == location+"/WHEEL") && f.UncompressedSize64 > maxPackageJSON {
			return out, errors.New("unsupported_wheel")
		}
		reader, e := f.Open()
		if e != nil {
			return out, errors.New("unsupported_wheel")
		}
		data, re := io.ReadAll(io.LimitReader(reader, min(dependencybundle.MaxFileBytes, dependencybundle.MaxPayloadBytes-*total)+1))
		ce := reader.Close()
		if re != nil || ce != nil || int64(len(data)) > dependencybundle.MaxFileBytes || uint64(len(data)) != f.UncompressedSize64 {
			return out, errors.New("record_mismatch")
		}
		if isDir {
			continue
		}
		*total += int64(len(data))
		if *total > dependencybundle.MaxPayloadBytes {
			return out, errors.New("unsupported_wheel")
		}
		if pythonForbidden(p, data) {
			return out, errors.New("unsupported_wheel")
		}
		out.files[p] = data
	}
	for d := range dirs {
		if !usedDirs[d] {
			return out, errors.New("unsafe_dependency_layout")
		}
	}
	metadata, ok := out.files[location+"/METADATA"]
	if !ok {
		return out, errors.New("unsupported_wheel")
	}
	wheel, ok := out.files[location+"/WHEEL"]
	if !ok {
		return out, errors.New("unsupported_wheel")
	}
	record, ok := out.files[location+"/RECORD"]
	if !ok {
		return out, errors.New("record_mismatch")
	}
	wh, e := pythonHeaders(wheel)
	if e != nil || !oneHeader(wh, "wheel-version", "1.0") || !oneHeader(wh, "root-is-purelib", "true") || !oneHeader(wh, "tag", "py3-none-any") || len(wh["build"]) != 0 {
		return out, errors.New("unsupported_wheel")
	}
	mh, e := pythonHeaders(metadata)
	if e != nil {
		return out, e
	}
	if len(mh["metadata-version"]) != 1 || !supportedMetadataVersion(mh["metadata-version"][0]) || len(mh["name"]) != 1 || len(mh["version"]) != 1 || normalizePythonName(mh["name"][0]) != w.Name || mh["version"][0] != w.Version {
		return out, errors.New("package_identity_mismatch")
	}
	// Validate syntax as well as normalization (e.g. no leading separator aliases).
	if m := pythonRequirementPattern.FindStringSubmatch(mh["name"][0]); m == nil || m[2] != "" {
		return out, errors.New("package_identity_mismatch")
	}
	for _, dynamic := range mh["dynamic"] {
		for _, v := range strings.Split(dynamic, ",") {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "name", "version", "requires-dist", "requires-python", "provides-extra", "requires-external":
				return out, errors.New("unsupported_wheel")
			}
		}
	}
	if len(mh["requires-python"]) > 1 {
		return out, errors.New("unsupported_python_constraint")
	}
	if len(mh["requires-python"]) == 1 {
		ok, e := pythonSatisfies(mh["requires-python"][0], "3.13.5")
		if e != nil {
			return out, errors.New("unsupported_python_constraint")
		}
		if !ok {
			return out, errors.New("runtime_incompatible")
		}
	}
	for _, external := range mh["requires-external"] {
		if external != "" {
			return out, errors.New("unsupported_wheel")
		}
	}
	extras := map[string]bool{}
	for _, extra := range mh["provides-extra"] {
		m := pythonRequirementPattern.FindStringSubmatch(extra)
		name := normalizePythonName(extra)
		if m == nil || m[2] != "" || !pythonName(name) || extras[name] {
			return out, errors.New("unsupported_extra")
		}
		extras[name] = true
		out.extras = append(out.extras, name)
	}
	sort.Strings(out.extras)
	out.requirements = mh["requires-dist"]
	if len(out.requirements) > maxEdges {
		return out, errInvalid
	}
	if e = checkRecord(record, location+"/RECORD", out.files); e != nil {
		return out, e
	}
	out.identity = PackageIdentity{Location: location, Name: w.Name, Version: w.Version, MetadataSHA256: sum(metadata), ArtifactSHA256: sum(b), ArtifactSize: int64(len(b)), ArtifactClaim: "user_supplied_hash_verified", RecordSHA256: sum(record)}
	return out, nil
}

// Email headers are case-insensitive and continuation lines are unfolded before
// interpretation. Raw complete METADATA bytes remain in the bundle and receipt hash.
func pythonHeaders(b []byte) (map[string][]string, error) {
	if len(b) == 0 || len(b) > maxPackageJSON || !utf8.Valid(b) {
		return nil, errors.New("unsupported_wheel")
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, c := range s {
		if c < 32 && c != '\n' && c != '\t' || c == 127 {
			return nil, errors.New("unsupported_wheel")
		}
	}
	h := map[string][]string{}
	last := ""
	var value strings.Builder
	flush := func() error {
		if last == "" {
			return nil
		}
		if (last == "requires-dist" || last == "provides-extra") && len(h[last]) >= maxEdges {
			return errInvalid
		}
		h[last] = append(h[last], value.String())
		value.Reset()
		return nil
	}
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if last == "" {
				return nil, errors.New("unsupported_wheel")
			}
			value.WriteByte(' ')
			value.WriteString(strings.TrimSpace(line))
			continue
		}
		key, text, ok := strings.Cut(line, ":")
		if !ok || key == "" {
			return nil, errors.New("unsupported_wheel")
		}
		for _, c := range key {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return nil, errors.New("unsupported_wheel")
			}
		}
		if e := flush(); e != nil {
			return nil, e
		}
		last = strings.ToLower(key)
		value.WriteString(strings.TrimSpace(text))
	}
	if e := flush(); e != nil {
		return nil, e
	}
	return h, nil
}
func supportedMetadataVersion(v string) bool {
	switch v {
	case "2.1", "2.2", "2.3", "2.4", "2.5", "2.6":
		return true
	}
	return false
}

func oneHeader(h map[string][]string, key, value string) bool {
	return len(h[key]) == 1 && h[key][0] == value
}
func checkRecord(b []byte, self string, files map[string][]byte) error {
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = 3
	r.ReuseRecord = true
	seen := map[string]bool{}
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.New("record_mismatch")
		}
		p := row[0]
		data, ok := files[p]
		if !ok || !pythonPath(p) || seen[p] {
			return errors.New("record_mismatch")
		}
		seen[p] = true
		if p == self {
			if row[1] != "" || row[2] != "" {
				return errors.New("record_mismatch")
			}
			continue
		}
		hash, e := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(row[1], "sha256="))
		if e != nil || len(hash) != 32 || row[1] != "sha256="+base64.RawURLEncoding.EncodeToString(hash) || row[2] != strconv.Itoa(len(data)) {
			return errors.New("record_mismatch")
		}
		if sum(data) != hex.EncodeToString(hash) {
			return errors.New("record_mismatch")
		}
	}
	if len(seen) != len(files) {
		return errors.New("record_mismatch")
	}
	return nil
}
