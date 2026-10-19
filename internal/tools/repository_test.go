package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"foundry/internal/domain"
)

const testCommit = "0123456789012345678901234567890123456789"

func writeFixture(t *testing.T, root, name, content string) domain.FileMeta {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return domain.FileMeta{Path: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(content))}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("invalid JSON body %q: %v", body, err)
	}
	return value
}

func TestRepositoryListIsSortedAndManifestConfined(t *testing.T) {
	root := t.TempDir()
	b := writeFixture(t, root, "src/b.ts", "second\n")
	a := writeFixture(t, root, "src/a.ts", "first\n")
	writeFixture(t, root, "private/secret.txt", "must not be named\n")
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{b, a}}

	result, err := repository.Execute(context.Background(), "repo.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 0 {
		t.Fatalf("list metadata substantiated source citations: %v", result.Paths)
	}
	if result.Truncated {
		t.Fatal("small list reported truncation")
	}
	body := decodeBody(t, result.Body)
	if body["tool"] != "repo.list" || body["base_commit"] != testCommit {
		t.Fatalf("missing authority metadata: %s", result.Body)
	}
	files, ok := body["files"].([]any)
	if !ok || len(files) != 2 {
		t.Fatalf("unexpected files: %#v", body["files"])
	}
	if files[0].(map[string]any)["path"] != "src/a.ts" || files[1].(map[string]any)["path"] != "src/b.ts" {
		t.Fatalf("manifest is not sorted: %#v", files)
	}
	if strings.Contains(string(result.Body), "secret") {
		t.Fatalf("outside-manifest name disclosed: %s", result.Body)
	}
}

func TestRepositoryReadValidatesWholeSourceBeforeSlicing(t *testing.T) {
	root := t.TempDir()
	meta := writeFixture(t, root, "src/a.ts", "one\ntwo\nthree\n")
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}

	result, err := repository.Execute(context.Background(), "repo.read", json.RawMessage(`{"path":"src/a.ts","start_line":2,"max_lines":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != "src/a.ts" || result.Truncated != true {
		t.Fatalf("unexpected evidence metadata: %#v", result)
	}
	body := decodeBody(t, result.Body)
	if body["content"] != "two\n" || body["returned_bytes"] != float64(4) {
		t.Fatalf("unexpected read body: %s", result.Body)
	}
	source := body["source"].(map[string]any)
	if source["path"] != "src/a.ts" || source["sha256"] != meta.SHA256 || source["start_line"] != float64(2) || source["end_line"] != float64(2) {
		t.Fatalf("unexpected source metadata: %#v", source)
	}

	if err := os.WriteFile(filepath.Join(root, "src/a.ts"), []byte("one\nbad\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Execute(context.Background(), "repo.read", json.RawMessage(`{"path":"src/a.ts","start_line":1,"max_lines":1}`)); err == nil {
		t.Fatal("served a slice before validating the complete source hash and size")
	}
}

func TestRepositorySearchIsLiteralBoundedAndSubstantiatesReturnedPaths(t *testing.T) {
	root := t.TempDir()
	a := writeFixture(t, root, "src/a.ts", "plain\na.* literal\nneedle one\n")
	b := writeFixture(t, root, "src/b.ts", "needle two\n")
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{b, a}}

	result, err := repository.Execute(context.Background(), "repo.search", json.RawMessage(`{"query":"a.*"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != "src/a.ts" {
		t.Fatalf("unexpected observed paths: %v", result.Paths)
	}
	body := decodeBody(t, result.Body)
	matches := body["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("literal query behaved like a regexp: %s", result.Body)
	}
	match := matches[0].(map[string]any)
	if match["path"] != "src/a.ts" || match["line"] != float64(2) || match["text"] != "a.* literal" || match["sha256"] != a.SHA256 {
		t.Fatalf("unexpected match metadata: %#v", match)
	}

	result, err = repository.Execute(context.Background(), "repo.search", json.RawMessage(`{"query":"needle","path":"src/b.ts"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != "src/b.ts" || strings.Contains(string(result.Body), "src/a.ts") {
		t.Fatalf("optional path was not exact: %#v %s", result.Paths, result.Body)
	}
}

func TestRepositoryRejectsInvalidToolArgumentsAndPaths(t *testing.T) {
	root := t.TempDir()
	meta := writeFixture(t, root, "src/a.ts", "text\n")
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}
	cases := []struct {
		name string
		tool string
		args string
	}{
		{"unknown tool", "repo.exec", `{}`},
		{"list field", "repo.list", `{"path":"src/a.ts"}`},
		{"unknown read field", "repo.read", `{"path":"src/a.ts","start_line":1,"max_lines":1,"command":"cat"}`},
		{"duplicate read field", "repo.read", `{"path":"src/a.ts","path":"src/a.ts","start_line":1,"max_lines":1}`},
		{"traversal", "repo.read", `{"path":"../src/a.ts","start_line":1,"max_lines":1}`},
		{"wrong case", "repo.read", `{"path":"SRC/a.ts","start_line":1,"max_lines":1}`},
		{"zero start", "repo.read", `{"path":"src/a.ts","start_line":0,"max_lines":1}`},
		{"too many lines", "repo.read", `{"path":"src/a.ts","start_line":1,"max_lines":201}`},
		{"empty query", "repo.search", `{"query":""}`},
		{"long query", "repo.search", `{"query":"` + strings.Repeat("x", 257) + `"}`},
		{"duplicate search field", "repo.search", `{"query":"x","query":"y"}`},
		{"non-object", "repo.list", `[]`},
		{"oversized arguments", "repo.search", `{"query":"x","padding":"` + strings.Repeat("x", 8192) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repository.Execute(context.Background(), tc.tool, json.RawMessage(tc.args)); err == nil {
				t.Fatalf("accepted %s %s", tc.tool, tc.args)
			}
		})
	}
}

func TestRepositoryRejectsLossyOrAmbiguousJSONArguments(t *testing.T) {
	root := t.TempDir()
	meta := writeFixture(t, root, "src/a.ts", "text\n")
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}
	reject := []struct {
		name string
		tool string
		args []byte
	}{
		{"case variant unknown key", "repo.search", []byte(`{"QUERY":"text"}`)},
		{"case variant semantic duplicate", "repo.search", []byte(`{"query":"text","QUERY":"other"}`)},
		{"read case variant", "repo.read", []byte(`{"Path":"src/a.ts","start_line":1,"max_lines":1}`)},
		{"malformed UTF-8", "repo.search", append([]byte(`{"query":"`), append([]byte{0xff}, []byte(`"}`)...)...)},
		{"lone high surrogate", "repo.search", []byte(`{"query":"\ud800"}`)},
		{"lone low surrogate", "repo.search", []byte(`{"query":"\udc00"}`)},
		{"null optional path", "repo.search", []byte(`{"query":"text","path":null}`)},
		{"null required query", "repo.search", []byte(`{"query":null}`)},
		{"null required read path", "repo.read", []byte(`{"path":null,"start_line":1,"max_lines":1}`)},
		{"null required start", "repo.read", []byte(`{"path":"src/a.ts","start_line":null,"max_lines":1}`)},
		{"null required max", "repo.read", []byte(`{"path":"src/a.ts","start_line":1,"max_lines":null}`)},
		{"wrong query type", "repo.search", []byte(`{"query":1}`)},
		{"wrong optional path type", "repo.search", []byte(`{"query":"text","path":1}`)},
		{"wrong read path type", "repo.read", []byte(`{"path":[],"start_line":1,"max_lines":1}`)},
		{"wrong line type", "repo.read", []byte(`{"path":"src/a.ts","start_line":"1","max_lines":1}`)},
		{"missing required query", "repo.search", []byte(`{}`)},
		{"missing required read path", "repo.read", []byte(`{"start_line":1,"max_lines":1}`)},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repository.Execute(context.Background(), tc.tool, json.RawMessage(tc.args)); err == nil {
				t.Fatalf("accepted lossy or ambiguous arguments: %q", tc.args)
			}
		})
	}

	for _, args := range []json.RawMessage{
		json.RawMessage(`{"query":"\ud83d\ude00"}`),
		json.RawMessage("{\"query\":\"\ufffd\"}"),
	} {
		if _, err := repository.Execute(context.Background(), "repo.search", args); err != nil {
			t.Fatalf("rejected lossless Unicode %q: %v", args, err)
		}
	}
}

func TestRepositoryRejectsSymlinkSpecialBinaryAndInvalidUTF8(t *testing.T) {
	root := t.TempDir()
	target := writeFixture(t, root, "target.txt", "target\n")
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	link := target
	link.Path = "link.txt"
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	pipe := domain.FileMeta{Path: "pipe", SHA256: strings.Repeat("0", 64), Bytes: 0}
	for name, content := range map[string][]byte{"binary.txt": {'a', 0, 'b'}, "utf8.txt": {0xff}} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		meta := domain.FileMeta{Path: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(content))}
		if _, err := (Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}).Execute(context.Background(), "repo.read", json.RawMessage(`{"path":"`+name+`","start_line":1,"max_lines":1}`)); err == nil {
			t.Fatalf("served unsafe content %s", name)
		}
	}
	for _, meta := range []domain.FileMeta{link, pipe} {
		if _, err := (Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}).Execute(context.Background(), "repo.read", json.RawMessage(`{"path":"`+meta.Path+`","start_line":1,"max_lines":1}`)); err == nil {
			t.Fatalf("served non-regular source %s", meta.Path)
		}
	}
}

func TestRepositoryResultBoundTruncationAndLargeLineFailure(t *testing.T) {
	root := t.TempDir()
	bounded := strings.Repeat(strings.Repeat("x", 400)+"\n", 200)
	meta := writeFixture(t, root, "bounded.txt", bounded)
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}
	result, err := repository.Execute(context.Background(), "repo.read", json.RawMessage(`{"path":"bounded.txt","start_line":1,"max_lines":200}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Body) > 64<<10 || !result.Truncated {
		t.Fatalf("result bound not enforced: bytes=%d truncated=%v", len(result.Body), result.Truncated)
	}
	decodeBody(t, result.Body)

	large := strings.Repeat("y", 70<<10) + "\n"
	largeMeta := writeFixture(t, root, "large.txt", large)
	largeRepository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{largeMeta}}
	if _, err := largeRepository.Execute(context.Background(), "repo.read", json.RawMessage(`{"path":"large.txt","start_line":1,"max_lines":1}`)); err == nil {
		t.Fatal("oversized single line was silently presented as complete")
	}
	if _, err := largeRepository.Execute(context.Background(), "repo.search", json.RawMessage(`{"query":"yyy"}`)); err == nil {
		t.Fatal("oversized matching line was silently presented as complete")
	}
}

func TestRepositoryHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	meta := writeFixture(t, root, "src/a.ts", "needle\n")
	repository := Repository{Root: root, BaseCommit: testCommit, Manifest: []domain.FileMeta{meta}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct{ tool, args string }{
		{"repo.list", `{}`},
		{"repo.read", `{"path":"src/a.ts","start_line":1,"max_lines":1}`},
		{"repo.search", `{"query":"needle"}`},
	} {
		if _, err := repository.Execute(ctx, tc.tool, json.RawMessage(tc.args)); err == nil {
			t.Fatalf("%s ignored cancellation", tc.tool)
		}
	}
}
