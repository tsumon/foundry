package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSharedBuildRejectsMissingGo(t *testing.T) {
	fixture := launchFixture(t)
	command := exec.Command("sh", filepath.Join(fixture.root, "scripts", "build.sh"))
	command.Env = append(os.Environ(),
		"FOUNDRY_GO="+filepath.Join(fixture.root, "missing-go"),
		"PATH="+fixture.toolDir+":/usr/bin:/bin",
		"LAUNCH_TEST_LOG="+fixture.logPath,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("shared build accepted a missing Go executable")
	}
	if !strings.Contains(string(output), "Go was not found") {
		t.Fatalf("missing Go error was not actionable: %s", output)
	}
	if log := readLaunchLog(t, fixture.logPath); log != "" {
		t.Fatalf("build commands ran after failed preflight: %q", log)
	}
}

func TestBothLaunchersUseSharedPreflightAndBuild(t *testing.T) {
	for _, launcher := range []string{"start.sh", "dev.sh"} {
		t.Run(launcher, func(t *testing.T) {
			fixture := launchFixture(t)
			command := exec.Command("sh", filepath.Join(fixture.root, "scripts", launcher))
			command.Env = append(os.Environ(),
				"FOUNDRY_GO="+fixture.goPath,
				"PATH="+fixture.toolDir+":/usr/bin:/bin",
				"LAUNCH_TEST_LOG="+fixture.logPath,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s failed: %v\n%s", launcher, err, output)
			}
			log := readLaunchLog(t, fixture.logPath)
			for _, boundary := range []string{"npm run build", "make FOUNDRY_GO=" + fixture.goPath + " build", "foundry"} {
				if count := strings.Count(log, boundary+"\n"); count != 1 {
					t.Errorf("%s boundary %q count=%d log=%q", launcher, boundary, count, log)
				}
			}
			if launcher == "dev.sh" {
				if count := strings.Count(log, "vite build --watch\n"); count != 1 {
					t.Errorf("dev watcher count=%d log=%q", count, log)
				}
			} else if strings.Contains(log, "vite build --watch") {
				t.Errorf("production launcher started watcher: %q", log)
			}
		})
	}
}

type launchTestFixture struct {
	root, toolDir, logPath, goPath string
}

func launchFixture(t *testing.T) launchTestFixture {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	projectRoot := filepath.Dir(filepath.Dir(filename))
	root := t.TempDir()
	for _, directory := range []string{
		filepath.Join(root, "scripts"),
		filepath.Join(root, "web", "node_modules", ".bin"),
		filepath.Join(root, "bin"),
		filepath.Join(root, "tools"),
	} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"build.sh", "start.sh", "dev.sh"} {
		content, err := os.ReadFile(filepath.Join(projectRoot, "scripts", name))
		if err != nil {
			t.Fatalf("read shared launcher %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, "scripts", name), content, 0755); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(root, "launch.log")
	goPath := filepath.Join(root, "tools", "go")
	writeExecutable(t, goPath, "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(root, "tools", "npm"), "#!/bin/sh\nprintf 'npm %s\\n' \"$*\" >> \"$LAUNCH_TEST_LOG\"\n")
	writeExecutable(t, filepath.Join(root, "tools", "make"), "#!/bin/sh\nprintf 'make %s\\n' \"$*\" >> \"$LAUNCH_TEST_LOG\"\n")
	writeExecutable(t, filepath.Join(root, "web", "node_modules", ".bin", "vite"), "#!/bin/sh\nprintf 'vite %s\\n' \"$*\" >> \"$LAUNCH_TEST_LOG\"\n")
	writeExecutable(t, filepath.Join(root, "web", "node_modules", ".bin", "tsc"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(root, "bin", "foundry"), "#!/bin/sh\nprintf 'foundry\\n' >> \"$LAUNCH_TEST_LOG\"\n")
	return launchTestFixture{root: root, toolDir: filepath.Join(root, "tools"), logPath: logPath, goPath: goPath}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func readLaunchLog(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
