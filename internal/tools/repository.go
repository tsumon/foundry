package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/platformfs"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
	"foundry/internal/jsontext"
)

const (
	maxRepositoryArguments = 8 << 10
	maxRepositoryResult    = 64 << 10
	maxRepositoryLine      = 60 << 10
	maxRepositoryFiles     = 30
	maxRepositoryFileBytes = 10 << 20
	maxReadLines           = 200
	maxSearchMatches       = 50
	maxSearchQueryBytes    = 256
)

type Repository struct {
	Root       string
	BaseCommit string
	Manifest   []domain.FileMeta
}

type Result struct {
	Body      []byte
	Paths     []string
	Truncated bool
}

type listedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type listBody struct {
	Tool          string       `json:"tool"`
	BaseCommit    string       `json:"base_commit"`
	Files         []listedFile `json:"files"`
	ReturnedBytes int          `json:"returned_bytes"`
	Truncated     bool         `json:"truncated"`
}

type sourceRange struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type readBody struct {
	Tool          string      `json:"tool"`
	BaseCommit    string      `json:"base_commit"`
	Source        sourceRange `json:"source"`
	Content       string      `json:"content"`
	ReturnedBytes int         `json:"returned_bytes"`
	Truncated     bool        `json:"truncated"`
}

type searchMatch struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
}

type searchBody struct {
	Tool          string        `json:"tool"`
	BaseCommit    string        `json:"base_commit"`
	Matches       []searchMatch `json:"matches"`
	ReturnedBytes int           `json:"returned_bytes"`
	Truncated     bool          `json:"truncated"`
}

type readArguments struct {
	Path      *string `json:"path"`
	StartLine *int    `json:"start_line"`
	MaxLines  *int    `json:"max_lines"`
}

type searchArguments struct {
	Query *string `json:"query"`
	Path  *string `json:"path"`
}

type argumentKind uint8

const (
	argumentString argumentKind = iota
	argumentInteger
)

type argumentField struct {
	kind     argumentKind
	required bool
}

var (
	listArgumentFields = map[string]argumentField{}
	readArgumentFields = map[string]argumentField{
		"path":       {kind: argumentString, required: true},
		"start_line": {kind: argumentInteger, required: true},
		"max_lines":  {kind: argumentInteger, required: true},
	}
	searchArgumentFields = map[string]argumentField{
		"query": {kind: argumentString, required: true},
		"path":  {kind: argumentString},
	}
)

func (r Repository) Execute(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	if err := contextError(ctx); err != nil {
		return Result{}, err
	}
	if len(arguments) == 0 || len(arguments) > maxRepositoryArguments {
		return Result{}, errors.New("repository tool arguments are invalid or exceed 8 KiB")
	}
	manifest, err := r.validatedManifest(ctx)
	if err != nil {
		return Result{}, err
	}
	switch name {
	case "repo.list":
		var args struct{}
		if err := decodeStrictObject(arguments, listArgumentFields, &args); err != nil {
			return Result{}, err
		}
		return r.executeList(ctx, manifest)
	case "repo.read":
		var args readArguments
		if err := decodeStrictObject(arguments, readArgumentFields, &args); err != nil {
			return Result{}, err
		}
		if args.Path == nil || args.StartLine == nil || args.MaxLines == nil || *args.StartLine < 1 || *args.MaxLines < 1 || *args.MaxLines > maxReadLines {
			return Result{}, errors.New("repo.read requires path, positive start_line, and max_lines from 1 to 200")
		}
		meta, ok := manifest[*args.Path]
		if !ok || !safeRepositoryPath(*args.Path) {
			return Result{}, errors.New("repo.read path is not in the approved manifest")
		}
		return r.executeRead(ctx, meta, *args.StartLine, *args.MaxLines)
	case "repo.search":
		var args searchArguments
		if err := decodeStrictObject(arguments, searchArgumentFields, &args); err != nil {
			return Result{}, err
		}
		if args.Query == nil || len(*args.Query) == 0 || len(*args.Query) > maxSearchQueryBytes || !utf8.ValidString(*args.Query) {
			return Result{}, errors.New("repo.search requires a nonempty UTF-8 query of at most 256 bytes")
		}
		if args.Path != nil {
			if _, ok := manifest[*args.Path]; !ok || !safeRepositoryPath(*args.Path) {
				return Result{}, errors.New("repo.search path is not in the approved manifest")
			}
		}
		return r.executeSearch(ctx, manifest, *args.Query, args.Path)
	default:
		return Result{}, errors.New("unknown repository tool")
	}
}

func (r Repository) validatedManifest(ctx context.Context) (map[string]domain.FileMeta, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(r.Root) || filepath.Clean(r.Root) != r.Root {
		return nil, errors.New("repository root must be an absolute clean path")
	}
	rootInfo, err := os.Lstat(r.Root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("repository root is unavailable or unsafe")
	}
	if !validObjectID(r.BaseCommit) {
		return nil, errors.New("repository base commit is invalid")
	}
	if len(r.Manifest) == 0 || len(r.Manifest) > maxRepositoryFiles {
		return nil, errors.New("repository manifest must contain 1 to 30 files")
	}
	manifest := make(map[string]domain.FileMeta, len(r.Manifest))
	casePaths := make(map[string]string, len(r.Manifest))
	for _, meta := range r.Manifest {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if !safeRepositoryPath(meta.Path) || meta.Bytes < 0 || meta.Bytes > maxRepositoryFileBytes || !validSHA256(meta.SHA256) {
			return nil, errors.New("repository manifest contains invalid metadata")
		}
		if _, exists := manifest[meta.Path]; exists {
			return nil, errors.New("repository manifest contains a duplicate path")
		}
		folded := strings.ToLower(meta.Path)
		if previous, exists := casePaths[folded]; exists && previous != meta.Path {
			return nil, errors.New("repository manifest contains case-colliding paths")
		}
		casePaths[folded] = meta.Path
		manifest[meta.Path] = meta
	}
	return manifest, nil
}

func (r Repository) executeList(ctx context.Context, manifest map[string]domain.FileMeta) (Result, error) {
	paths := make([]string, 0, len(manifest))
	for name := range manifest {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	files := make([]listedFile, 0, len(paths))
	for _, name := range paths {
		if err := contextError(ctx); err != nil {
			return Result{}, err
		}
		meta := manifest[name]
		files = append(files, listedFile{Path: meta.Path, SHA256: meta.SHA256, Bytes: meta.Bytes})
	}
	body, err := marshalRepositoryBody(listBody{Tool: "repo.list", BaseCommit: r.BaseCommit, Files: files, ReturnedBytes: 0, Truncated: false})
	if err != nil {
		return Result{}, err
	}
	return Result{Body: body, Paths: []string{}, Truncated: false}, nil
}

func (r Repository) executeRead(ctx context.Context, meta domain.FileMeta, startLine, maxLines int) (Result, error) {
	content, err := r.readValidated(ctx, meta)
	if err != nil {
		return Result{}, err
	}
	lines := splitLines(content)
	start := startLine - 1
	if start > len(lines) {
		start = len(lines)
	}
	limit := start + maxLines
	if limit > len(lines) {
		limit = len(lines)
	}
	var selected []byte
	endLine := 0
	boundTruncated := false
	for index := start; index < limit; index++ {
		if err := contextError(ctx); err != nil {
			return Result{}, err
		}
		line := lines[index]
		if len(line) > maxRepositoryLine {
			if len(selected) == 0 {
				return Result{}, errors.New("repo.read line exceeds the bounded result allowance")
			}
			boundTruncated = true
			break
		}
		candidate := append(append([]byte(nil), selected...), line...)
		candidateBody := readBody{
			Tool:       "repo.read",
			BaseCommit: r.BaseCommit,
			Source: sourceRange{Path: meta.Path, SHA256: meta.SHA256, StartLine: startLine,
				EndLine: index + 1},
			Content: string(candidate), ReturnedBytes: len(candidate), Truncated: true,
		}
		if _, err := marshalRepositoryBody(candidateBody); err != nil {
			if len(selected) == 0 {
				return Result{}, errors.New("repo.read line exceeds the bounded result allowance")
			}
			boundTruncated = true
			break
		}
		selected = candidate
		endLine = index + 1
	}
	truncated := boundTruncated
	// endLine is one-based. Any source line after it means the returned slice is
	// incomplete, whether due to max_lines or the serialized-result ceiling.
	if endLine > 0 {
		truncated = truncated || endLine < len(lines)
	} else {
		truncated = start < len(lines)
	}
	bodyValue := readBody{
		Tool:       "repo.read",
		BaseCommit: r.BaseCommit,
		Source: sourceRange{Path: meta.Path, SHA256: meta.SHA256, StartLine: startLine,
			EndLine: endLine},
		Content: string(selected), ReturnedBytes: len(selected), Truncated: truncated,
	}
	body, err := marshalRepositoryBody(bodyValue)
	if err != nil {
		return Result{}, err
	}
	paths := []string{}
	if len(selected) > 0 {
		paths = append(paths, meta.Path)
	}
	return Result{Body: body, Paths: paths, Truncated: truncated}, nil
}

func (r Repository) executeSearch(ctx context.Context, manifest map[string]domain.FileMeta, query string, only *string) (Result, error) {
	paths := make([]string, 0, len(manifest))
	if only != nil {
		paths = append(paths, *only)
	} else {
		for name := range manifest {
			paths = append(paths, name)
		}
		sort.Strings(paths)
	}
	matches := make([]searchMatch, 0)
	observed := make([]string, 0)
	observedSet := map[string]bool{}
	returnedBytes := 0
	truncated := false
	stop := false
	for _, name := range paths {
		if err := contextError(ctx); err != nil {
			return Result{}, err
		}
		meta := manifest[name]
		content, err := r.readValidated(ctx, meta)
		if err != nil {
			return Result{}, err
		}
		for index, rawLine := range splitLines(content) {
			if err := contextError(ctx); err != nil {
				return Result{}, err
			}
			line := strings.TrimSuffix(strings.TrimSuffix(string(rawLine), "\n"), "\r")
			if !strings.Contains(line, query) {
				continue
			}
			if len(matches) == maxSearchMatches {
				truncated = true
				stop = true
				break
			}
			if len(line) > maxRepositoryLine {
				if len(matches) == 0 {
					return Result{}, errors.New("repo.search matching line exceeds the bounded result allowance")
				}
				truncated = true
				stop = true
				break
			}
			match := searchMatch{Path: meta.Path, SHA256: meta.SHA256, Line: index + 1, Text: line}
			candidate := append(append([]searchMatch(nil), matches...), match)
			candidateBody := searchBody{Tool: "repo.search", BaseCommit: r.BaseCommit, Matches: candidate, ReturnedBytes: returnedBytes + len(line), Truncated: true}
			if _, err := marshalRepositoryBody(candidateBody); err != nil {
				if len(matches) == 0 {
					return Result{}, errors.New("repo.search matching line exceeds the bounded result allowance")
				}
				truncated = true
				stop = true
				break
			}
			matches = candidate
			returnedBytes += len(line)
			if !observedSet[meta.Path] {
				observedSet[meta.Path] = true
				observed = append(observed, meta.Path)
			}
		}
		if stop {
			break
		}
	}
	body, err := marshalRepositoryBody(searchBody{Tool: "repo.search", BaseCommit: r.BaseCommit, Matches: matches, ReturnedBytes: returnedBytes, Truncated: truncated})
	if err != nil {
		return Result{}, err
	}
	return Result{Body: body, Paths: observed, Truncated: truncated}, nil
}

func (r Repository) readValidated(ctx context.Context, meta domain.FileMeta) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(r.Root)
	if err != nil {
		return nil, errors.New("repository root cannot be opened")
	}
	defer root.Close()
	if err := validatePathComponents(root, meta.Path); err != nil {
		return nil, err
	}
	file, err := platformfs.OpenRootRegular(root, meta.Path)
	if err != nil {
		return nil, errors.New("repository source cannot be opened safely")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Bytes {
		return nil, errors.New("repository source metadata does not match the manifest")
	}
	content := make([]byte, 0, int(meta.Bytes))
	buffer := make([]byte, 32<<10)
	for {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			if int64(len(content)+count) > meta.Bytes {
				return nil, errors.New("repository source size changed while reading")
			}
			content = append(content, buffer[:count]...)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, errors.New("repository source read failed")
		}
	}
	if int64(len(content)) != meta.Bytes {
		return nil, errors.New("repository source size does not match the manifest")
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, errors.New("repository source is binary or invalid UTF-8")
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != meta.SHA256 {
		return nil, errors.New("repository source hash does not match the manifest")
	}
	return content, nil
}

func validatePathComponents(root *os.Root, name string) error {
	components := strings.Split(name, "/")
	for index := range components {
		prefix := strings.Join(components[:index+1], "/")
		info, err := root.Lstat(prefix)
		if err != nil {
			return errors.New("repository source is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("repository source symlinks are not allowed")
		}
		if index < len(components)-1 && !info.IsDir() {
			return errors.New("repository source path is not a directory tree")
		}
		if index == len(components)-1 && !info.Mode().IsRegular() {
			return errors.New("repository source must be a regular file")
		}
	}
	return nil
}

func splitLines(content []byte) [][]byte {
	if len(content) == 0 {
		return nil
	}
	lines := make([][]byte, 0, bytes.Count(content, []byte{'\n'})+1)
	for len(content) > 0 {
		index := bytes.IndexByte(content, '\n')
		if index < 0 {
			lines = append(lines, content)
			break
		}
		lines = append(lines, content[:index+1])
		content = content[index+1:]
	}
	return lines
}

func marshalRepositoryBody(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("repository result cannot be serialized")
	}
	if len(body) > maxRepositoryResult {
		return nil, errors.New("repository result exceeds 64 KiB")
	}
	return body, nil
}

func decodeStrictObject(arguments json.RawMessage, fields map[string]argumentField, destination any) error {
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("repository tool arguments must be a JSON object")
	}
	if !jsontext.Lossless(arguments) {
		return errors.New("repository tool arguments must have lossless JSON text encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return errors.New("repository tool arguments contain invalid or duplicate JSON")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("repository tool arguments contain trailing JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &object); err != nil {
		return errors.New("repository tool arguments do not match the tool schema")
	}
	for key, raw := range object {
		field, ok := fields[key]
		if !ok || !argumentHasKind(raw, field.kind) {
			return errors.New("repository tool arguments do not match the tool schema")
		}
	}
	for key, field := range fields {
		if _, ok := object[key]; field.required && !ok {
			return errors.New("repository tool arguments do not match the tool schema")
		}
	}
	if err := json.Unmarshal(arguments, destination); err != nil {
		return errors.New("repository tool arguments do not match the tool schema")
	}
	return nil
}

func argumentHasKind(raw json.RawMessage, kind argumentKind) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	switch kind {
	case argumentString:
		if trimmed[0] != '"' {
			return false
		}
		var value string
		return json.Unmarshal(trimmed, &value) == nil
	case argumentInteger:
		var value int
		return json.Unmarshal(trimmed, &value) == nil
	default:
		return false
	}
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || keys[key] {
				return errors.New("duplicate or invalid object key")
			}
			keys[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unterminated object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unterminated array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func safeRepositoryPath(name string) bool {
	return name != "" && len(name) <= 4096 && utf8.ValidString(name) && fs.ValidPath(name) && path.Clean(name) == name &&
		!strings.Contains(name, "\\") && !strings.ContainsFunc(name, unicode.IsControl)
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("repository tool canceled: %w", err)
	}
	return nil
}
