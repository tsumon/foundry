package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"text/template"
)

// Models the trusted CLI's typed image data, whose ordinary JSON omits empty
// Cmd/Volumes. No installed Docker, socket, image, or host credential is used.
type c4CLIImage struct {
	Id           string
	Os           string
	Architecture string
	Config       struct {
		Cmd     []string       `json:",omitempty"`
		Volumes map[string]any `json:",omitempty"`
		Env     []string
	}
}

func TestC4DockerExplicitImageProjection(t *testing.T) {
	for _, route := range []string{"create", "readiness"} {
		for _, variant := range []string{"unset", "empty", "command", "volume", "environment", "quoted command"} {
			t.Run(route+"/"+variant, func(t *testing.T) {
				p := c4DriverPlan()
				image := c4CLIImage{Id: p.profile.ImageID, Os: "linux", Architecture: "arm64"}
				image.Config.Env = []string{"GOLANG_VERSION=1.27.3"}
				safe := variant == "unset" || variant == "empty"
				switch variant {
				case "empty":
					image.Config.Cmd = []string{}
					image.Config.Volumes = map[string]any{}
				case "command":
					image.Config.Cmd = []string{"sh"}
				case "volume":
					image.Config.Volumes = map[string]any{"/data": map[string]any{}}
				case "environment":
					image.Config.Env = []string{"LD_PRELOAD=/evil"}
				case "quoted command":
					image.Config.Cmd = []string{"x\"},\"Cmd\":null"}
				}
				calls := 0
				projected := false
				process := c4ReadinessProcessFunc(func(_ context.Context, c C4Command, out, _ io.Writer) (int, error) {
					calls++
					if c.Path != p.profile.DockerPath || len(c.Env) != 1 || c.Env[0] != "DOCKER_CONFIG=/dev/null" {
						t.Fatal("transport authority changed")
					}
					if len(c.Args) < 4 {
						t.Fatal("missing fixed command")
					}
					switch c.Args[2] {
					case "version":
						io.WriteString(out, `{"Server":{"Version":"29.9.2","Os":"linux","Arch":"arm64"}}`)
					case "info":
						io.WriteString(out, `{"ID":"owned-offline-fixture","OSType":"linux","Architecture":"aarch64"}`)
					case "image":
						if len(c.Args) != 7 || c.Args[3] != "inspect" || c.Args[4] != "--format" || c.Args[6] != p.profile.ImageID {
							t.Fatalf("image inspection must explicitly render security fields: %v", c.Args)
						}
						raw, err := json.Marshal(image)
						if err != nil {
							t.Fatal(err)
						}
						projectedBytes, err := c4RenderCLIImage(c.Args[5], image, raw)
						if err != nil {
							t.Fatal(err)
						}
						var rendered []map[string]json.RawMessage
						if err = json.Unmarshal(projectedBytes, &rendered); err != nil || len(rendered) != 1 {
							t.Fatal("invalid image projection", err)
						}
						var config map[string]json.RawMessage
						if err = json.Unmarshal(rendered[0]["Config"], &config); err != nil {
							t.Fatal(err)
						}
						for _, key := range []string{"Cmd", "Volumes", "Env"} {
							if _, ok := config[key]; !ok {
								t.Fatal("projection omitted", key)
							}
						}
						if got := string(config["Cmd"]); variant == "command" && got != `["sh"]` {
							t.Fatal("command erased")
						}
						projected = true
						out.Write(projectedBytes)
					case "create":
						if !safe || !projected {
							t.Fatal("unsafe image reached create")
						}
						io.WriteString(out, strings.Repeat("d", 64)+"\n")
					default:
						return 1, fmt.Errorf("unexpected command")
					}
					return 0, nil
				})
				if route == "create" {
					d := newC4DockerDriver(p, process, &c4CountingTrust{}, "")
					id, err := d.Create(context.Background(), p.Create())
					if safe && (err != nil || id != strings.Repeat("d", 64) || calls != 2) {
						t.Fatal("safe image rejected", err, calls)
					}
					if !safe && (err == nil || id != "" || calls != 1) {
						t.Fatal("unsafe image accepted", err, calls)
					}
				} else {
					p.profile.ControlVersion = 1
					c := C4DockerConfig{Enabled: true, Trust: C4Trust{DockerPath: p.profile.DockerPath, Endpoint: p.profile.Endpoint}}
					result, err := checkC4DockerReadiness(context.Background(), p.profile, c, process, &c4CountingTrust{})
					if safe && (err != nil || len(result.Fingerprint) != 64) {
						t.Fatal("safe readiness rejected", err)
					}
					if !safe && (err == nil || result.Fingerprint != "") {
						t.Fatal("unsafe readiness accepted")
					}
					if calls != 3 {
						t.Fatal("readiness retried or escaped fixed sequence", calls)
					}
				}
			})
		}
	}
}

// Model the CLI's buffered typed attempt followed by its strict raw-JSON
// template fallback. This is local rendering, not another Docker request.
func c4RenderCLIImage(format string, typed any, raw []byte) ([]byte, error) {
	tmpl, err := template.New("image").Funcs(template.FuncMap{"json": func(v any) (string, error) {
		b, e := json.Marshal(v)
		return string(b), e
	}}).Parse(format)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if typed != nil {
		if err = tmpl.Execute(&out, typed); err == nil {
			return out.Bytes(), nil
		}
		out.Reset()
	}
	var data any
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if err = tmpl.Option("missingkey=error").Execute(&out, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func TestC4ImageProjectionStrictRawOptionalFields(t *testing.T) {
	p := c4DriverPlan()
	for _, tt := range []struct {
		name, config, extra string
		valid               bool
	}{
		{"both omitted", `{"Env":["GOLANG_VERSION=1.27.3"]}`, "", true},
		{"cmd omitted", `{"Env":null,"Volumes":{}}`, "", true},
		{"volumes omitted", `{"Env":null,"Cmd":[]}`, "", true},
		{"explicit null", `{"Env":null,"Cmd":null,"Volumes":null}`, "", true},
		{"explicit empty", `{"Env":[],"Cmd":[],"Volumes":{}}`, "", true},
		{"command", `{"Env":null,"Cmd":["sh"]}`, "", false},
		{"volume", `{"Env":null,"Volumes":{"/data":{}}}`, "", false},
		{"wrong cmd type", `{"Env":null,"Cmd":{}}`, "", false},
		{"wrong volumes type", `{"Env":null,"Volumes":[]}`, "", false},
		{"false cmd", `{"Env":null,"Cmd":false}`, "", false},
		{"false volumes", `{"Env":null,"Volumes":false}`, "", false},
		{"cmd alias", `{"Env":null,"cmd":["sh"]}`, "", false},
		{"volumes alias", `{"Env":null,"volumes":{"/data":{}}}`, "", false},
		{"env alias", `{"Env":null,"env":["LD_PRELOAD=/evil"]}`, "", false},
		{"environment", `{"Env":["LD_PRELOAD=/evil"]}`, "", false},
		{"missing environment", `{}`, "", false},
		{"null config", `null`, "", false},
		{"array config", `[]`, "", false},
		{"config alias", `{"Env":null}`, `,"config":{"Cmd":["sh"]}`, false},
		{"identity alias", `{"Env":null}`, `,"id":"other"`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"Id":%q,"Os":"linux","Architecture":"arm64","Config":%s%s}`, p.profile.ImageID, tt.config, tt.extra))
			out, err := c4RenderCLIImage(p.imageInspection().Args[5], nil, raw)
			if err == nil {
				err = validateC4DockerImage(out, p.profile)
			}
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
			if tt.valid {
				m, err := c4DockerInspectObject(out)
				if err != nil {
					t.Fatal(err)
				}
				c := m.object("Config")
				for _, key := range []string{"Cmd", "Volumes", "Env"} {
					if _, ok := c[key]; !ok {
						t.Fatal("missing required wire field", key)
					}
				}
			}
		})
	}
}
