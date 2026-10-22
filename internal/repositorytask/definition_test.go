package repositorytask

import (
	"encoding/json"
	"strings"
	"testing"
)

const example = `{"schema_version":1,"goal":"Normalize labels","summary":"Scripted label normalization","scope":["internal/label"],"protected":["foundry.task.json","go.mod","go.sum"],"steps":[{"id":"label","title":"Normalize","description":"Change label formatting","depends_on":[],"edits":[{"path":"internal/label/label.go","content":"broken"},{"path":"internal/label/display.go","content":"display"}]}],"repairs":[[{"path":"internal/label/label.go","content":"normalized"}]],"checks":[{"path":"internal/label/label.go","content":"normalized"},{"path":"internal/label/display.go","content":"display"}]}`

func TestParseNestedMultifile(t *testing.T) {
	d, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if d.Goal != "Normalize labels" || len(d.Steps[0].Edits) != 2 {
		t.Fatal(d)
	}
}

func TestParseFailsClosed(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown":            strings.Replace(example, `"schema_version":1`, `"schema_version":1,"command":"sh"`, 1),
		"version":            strings.Replace(example, `"schema_version":1`, `"schema_version":2`, 1),
		"duplicate":          strings.Replace(example, `"goal":`, `"goal":"other","goal":`, 1),
		"case-alias":         strings.Replace(example, `"goal":`, `"Goal":"other","goal":`, 1),
		"case-path-alias":    strings.Replace(example, `"path":"internal/label/display.go","content":"display"}]}`, `"path":"internal/label/Display.go","content":"display"}]}`, 1),
		"missing-content":    strings.Replace(example, `,"content":"broken"`, ``, 1),
		"null-content":       strings.Replace(example, `"content":"broken"`, `"content":null`, 1),
		"unsafe":             strings.ReplaceAll(example, "internal/label/label.go", "../label.go"),
		"protected":          strings.ReplaceAll(example, "internal/label/label.go", "internal/label/label_test.go"),
		"descriptor-edit":    strings.ReplaceAll(example, "internal/label/label.go", "foundry.task.json"),
		"uncovered":          strings.Replace(example, `{"path":"internal/label/label.go","content":"normalized"},`, "", 1),
		"cycle":              strings.Replace(example, `"depends_on":[]`, `"depends_on":["label"]`, 1),
		"unknown-dependency": strings.Replace(example, `"depends_on":[]`, `"depends_on":["missing"]`, 1),
		"trailing":           example + `{}`,
		"oversize":           strings.Repeat(" ", 128*1024) + example,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatal("accepted invalid descriptor")
			}
		})
	}
	var obj map[string]any
	_ = json.Unmarshal([]byte(example), &obj)
	obj["repairs"] = []any{[]any{}, []any{}, []any{}}
	raw, _ := json.Marshal(obj)
	if _, err := Parse(raw); err == nil {
		t.Fatal("accepted three repair rounds")
	}
}
