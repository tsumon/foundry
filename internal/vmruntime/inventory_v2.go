package vmruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

const MaxRuntimeInventoryBytesV2 = 64 << 10
const RuntimeInventoryPathV2 = "runtime-inventory-v2.json"

var errInventoryV2 = errors.New("invalid VM runtime inventory v2")

type RuntimeFileV2 struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type RuntimeProjectionV2 struct {
	SHA256       string `json:"sha256"`
	Entries      int    `json:"entries"`
	RegularBytes int64  `json:"regular_bytes"`
}
type RuntimeNodeV2 struct {
	Version    string        `json:"version"`
	Executable RuntimeFileV2 `json:"executable"`
}
type RuntimePythonV2 struct {
	Version        string        `json:"version"`
	Distribution   string        `json:"distribution"`
	PackageVersion string        `json:"package_version"`
	Executable     RuntimeFileV2 `json:"executable"`
	Stdlib         string        `json:"stdlib"`
	Options        []string      `json:"options"`
}
type RuntimeTypeScriptV2 struct {
	Version  string        `json:"version"`
	Compiler RuntimeFileV2 `json:"compiler"`
}
type RuntimeRunnerV2 struct {
	ProfileID string        `json:"profile_id"`
	RunnerID  string        `json:"runner_id"`
	File      RuntimeFileV2 `json:"file"`
}
type RuntimeFrameworkReportV2 struct {
	SchemaVersion int    `json:"schema_version"`
	NodeRunner    string `json:"node_runner"`
	PythonRunner  string `json:"python_runner"`
	MaxBytes      int    `json:"max_bytes"`
	MaxCases      int    `json:"max_cases"`
}
type RuntimeCompilerOptionsV2 struct {
	Target           string `json:"target"`
	Module           string `json:"module"`
	ModuleResolution string `json:"module_resolution"`
	Strict           bool   `json:"strict"`
	NoEmitOnError    bool   `json:"no_emit_on_error"`
	AllowJS          bool   `json:"allow_js"`
	SkipLibCheck     bool   `json:"skip_lib_check"`
}
type RuntimeCompilerContractV2 struct {
	SchemaVersion   int                      `json:"schema_version"`
	ConfigPath      string                   `json:"config_path"`
	RootDir         string                   `json:"root_dir"`
	OutDir          string                   `json:"out_dir"`
	CompilerOptions RuntimeCompilerOptionsV2 `json:"compiler_options"`
	MaxFiles        int                      `json:"max_files"`
	MaxBytes        int64                    `json:"max_bytes"`
	MaxFileBytes    int64                    `json:"max_file_bytes"`
}
type RuntimeInventoryV2 struct {
	SchemaVersion        int                       `json:"schema_version"`
	GuestPlatform        string                    `json:"guest_platform"`
	ProjectionPolicy     string                    `json:"projection_policy"`
	ProjectedRuntime     RuntimeProjectionV2       `json:"projected_runtime"`
	Supervisor           RuntimeFileV2             `json:"supervisor"`
	Node                 RuntimeNodeV2             `json:"node"`
	Python               RuntimePythonV2           `json:"python"`
	TypeScript           RuntimeTypeScriptV2       `json:"typescript"`
	Runners              []RuntimeRunnerV2         `json:"runners"`
	FrameworkReport      RuntimeFrameworkReportV2  `json:"framework_report"`
	ChildContractVersion int                       `json:"child_contract_version"`
	CompilerContract     RuntimeCompilerContractV2 `json:"compiler_contract"`
}

func (d RuntimeInventoryV2) Validate() error {
	if d.SchemaVersion != 2 || (d.GuestPlatform != "linux/arm64" && d.GuestPlatform != "linux/amd64") || d.ProjectionPolicy != "foundry-runtime-projection-v2" || !hexDigest(d.ProjectedRuntime.SHA256) || d.ProjectedRuntime.Entries <= 0 || d.ProjectedRuntime.Entries > 20000 || d.ProjectedRuntime.RegularBytes <= 0 || d.ProjectedRuntime.RegularBytes > 512<<20 || d.ChildContractVersion != 1 {
		return errInventoryV2
	}
	validFile := func(f RuntimeFileV2, p string, max int64) bool {
		return f.Path == p && f.Size > 0 && f.Size <= max && hexDigest(f.SHA256)
	}
	if !validFile(d.Supervisor, "/init", 64<<20) || d.Node.Version != "24.22.1" || !validFile(d.Node.Executable, "/opt/foundry/node/bin/node", 128<<20) || d.Python.Version != "3.13.5" || d.Python.Distribution != "Debian trixie" || d.Python.PackageVersion != "3.13.5-2+deb13u5" || d.Python.Stdlib != "/usr/lib/python3.13" || !slices.Equal(d.Python.Options, []string{"-I", "-S", "-B"}) || !validFile(d.Python.Executable, "/usr/bin/python3.13", 128<<20) || d.TypeScript.Version != "5.9.3" || !validFile(d.TypeScript.Compiler, "/opt/foundry/typescript/lib/typescript.js", 128<<20) {
		return errInventoryV2
	}
	want := []RuntimeRunnerV2{{ProfileID: "c4_node_model_v2", RunnerID: "node-test-v2", File: RuntimeFileV2{Path: "/opt/foundry/runners/node.cjs"}}, {ProfileID: "c4_python_model_v2", RunnerID: "python-unittest-v2", File: RuntimeFileV2{Path: "/opt/foundry/runners/python.py"}}, {ProfileID: "c4_typescript_model_v2", RunnerID: "typescript-node-test-v2", File: RuntimeFileV2{Path: "/opt/foundry/runners/typescript.cjs"}}}
	if len(d.Runners) != len(want) {
		return errInventoryV2
	}
	for i, r := range d.Runners {
		w := want[i]
		if r.ProfileID != w.ProfileID || r.RunnerID != w.RunnerID || !validFile(r.File, w.File.Path, 1<<20) {
			return errInventoryV2
		}
	}
	if d.FrameworkReport != (RuntimeFrameworkReportV2{1, "node-test-v2", "python-unittest-v2", 2097152, 4096}) || d.CompilerContract != (RuntimeCompilerContractV2{1, "tsconfig.json", "/workspace", "/foundry-build/compiled", RuntimeCompilerOptionsV2{"ES2022", "NodeNext", "NodeNext", true, true, false, false}, 1000, 67108864, 8388608}) {
		return errInventoryV2
	}
	return nil
}

// CanonicalBytes is specific to the installed descriptor. Existing wire/domain
// encodings keep their own byte contracts. All descriptor strings are ASCII.
func (d RuntimeInventoryV2) CanonicalBytes() ([]byte, error) {
	if d.Validate() != nil {
		return nil, errInventoryV2
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return nil, errInventoryV2
	}
	var obj any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&obj) != nil {
		return nil, errInventoryV2
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(obj) != nil {
		return nil, errInventoryV2
	}
	b := bytes.TrimSuffix(out.Bytes(), []byte{'\n'})
	if len(b) > MaxRuntimeInventoryBytesV2 {
		return nil, errInventoryV2
	}
	return b, nil
}
func (d RuntimeInventoryV2) Digest() (string, error) {
	b, e := d.CanonicalBytes()
	if e != nil {
		return "", e
	}
	h := sha256.New()
	h.Write([]byte("foundry:runtime-inventory:v2\n"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func ParseRuntimeInventoryV2(raw []byte) (RuntimeInventoryV2, error) {
	var d RuntimeInventoryV2
	if len(raw) == 0 || len(raw) > MaxRuntimeInventoryBytesV2 || strictJSONV2(raw, &d) != nil {
		return RuntimeInventoryV2{}, errInventoryV2
	}
	b, e := d.CanonicalBytes()
	if e != nil || !bytes.Equal(raw, b) {
		return RuntimeInventoryV2{}, errInventoryV2
	}
	return d, nil
}
