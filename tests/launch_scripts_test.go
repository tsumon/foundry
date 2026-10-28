package tests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
	for _, testCase := range []struct {
		name, launcher string
		exitStatus     int
	}{
		{name: "start.sh", launcher: "start.sh"},
		{name: "dev.sh", launcher: "dev.sh"},
		{name: "start.sh/controller-failure", launcher: "start.sh", exitStatus: 23},
		{name: "dev.sh/controller-failure", launcher: "dev.sh", exitStatus: 23},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			launcher := testCase.launcher
			fixture := launchFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "sh", filepath.Join(fixture.root, "scripts", launcher))
			command.WaitDelay = time.Second
			command.Env = append(os.Environ(),
				"FOUNDRY_GO="+fixture.goPath,
				"PATH="+fixture.toolDir+":/usr/bin:/bin",
				"LAUNCH_TEST_LOG="+fixture.logPath,
				"LAUNCH_TEST_SERVER_EXIT_STATUS="+strconv.Itoa(testCase.exitStatus),
			)
			if launcher == "dev.sh" {
				command.Env = append(command.Env, "LAUNCH_TEST_EXPECT_WATCHER=1")
			}
			output, err := command.CombinedOutput()
			if testCase.exitStatus == 0 && err != nil {
				t.Fatalf("%s failed: %v\n%s", launcher, err, output)
			}
			if testCase.exitStatus != 0 {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != testCase.exitStatus {
					t.Fatalf("%s did not preserve controller exit status %d: %v\n%s", launcher, testCase.exitStatus, err, output)
				}
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
				if count := strings.Count(log, "vite stopped\n"); count != 1 {
					t.Errorf("dev watcher cleanup count=%d log=%q", count, log)
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
	writeExecutable(t, filepath.Join(root, "web", "node_modules", ".bin", "vite"), `#!/bin/sh
printf 'vite %s\n' "$*" >> "$LAUNCH_TEST_LOG"
trap 'printf "vite stopped\n" >> "$LAUNCH_TEST_LOG"; exit 0' TERM
: > "$LAUNCH_TEST_LOG.watcher-ready"
remaining=500
while [ "$remaining" -gt 0 ]; do
	remaining=$((remaining - 1))
	sleep 0.01
done
echo "dev watcher was not stopped" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(root, "web", "node_modules", ".bin", "tsc"), "#!/bin/sh\nexit 0\n")
	// A real controller stays alive while the watcher starts. Make that lifecycle
	// explicit in the fixture instead of racing an immediately exiting controller
	// against the launcher's cleanup trap. The bounded wait also makes a missing
	// watcher fail without leaving this test blocked.
	writeExecutable(t, filepath.Join(root, "bin", "foundry"), `#!/bin/sh
if [ "${LAUNCH_TEST_EXPECT_WATCHER:-}" = 1 ]; then
	remaining=500
	while [ ! -f "$LAUNCH_TEST_LOG.watcher-ready" ]; do
		remaining=$((remaining - 1))
		if [ "$remaining" -eq 0 ]; then
			echo "dev watcher did not become ready" >&2
			exit 1
		fi
		sleep 0.01
	done
fi
printf 'foundry\n' >> "$LAUNCH_TEST_LOG"
exit "${LAUNCH_TEST_SERVER_EXIT_STATUS:-0}"
`)
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
