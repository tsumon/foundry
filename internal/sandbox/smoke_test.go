package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in: this test uses an already-present image, never pulls, and mounts only
// this test's temporary candidate. It exercises the production Docker runner.
func TestDockerSmoke(t *testing.T) {
	path := os.Getenv("FOUNDRY_TEST_DOCKER")
	image := os.Getenv("FOUNDRY_TEST_IMAGE")
	if path == "" || image == "" {
		t.Skip("set FOUNDRY_TEST_DOCKER and FOUNDRY_TEST_IMAGE for local pre-existing image smoke")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	os.WriteFile(filepath.Join(dir, "source.txt"), []byte("frozen\n"), 0644)
	sum := sha256.Sum256([]byte(dir))
	d := New(path, hex.EncodeToString(sum[:12]))
	ctx := context.Background()
	id, err := d.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.Run(ctx, "smoke", dir, id, `test "$(id -u)" = 65534 && test ! -e /var/run/docker.sock && test ! -e /workspace/.git && test "$(cat /workspace/source.txt)" = frozen && ! touch /workspace/write && ! touch /root/write && touch /tmp/ok && echo isolated`)
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "isolated") {
		t.Fatalf("isolation: %+v %v", result, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "source.txt"))
	if string(b) != "frozen\n" {
		t.Fatal("canonical candidate changed")
	}
	result, err = d.Run(ctx, "smoke", dir, id, "echo failed; exit 7")
	if err != nil || result.ExitCode != 7 {
		t.Fatalf("exit status %+v %v", result, err)
	}
	cancelCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_, err = d.Run(cancelCtx, "smoke", dir, id, "sleep 30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	if err = d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	b, err = d.command(ctx, "ps", "-aq", "--filter", "label=foundry.owner="+d.Owner)
	if err != nil || len(strings.TrimSpace(string(b))) != 0 {
		t.Fatalf("owned containers remain: %s %v", b, err)
	}
}
