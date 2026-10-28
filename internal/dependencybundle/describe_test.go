package dependencybundle

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDescribeVerifiedReturnsOnlyFullyVerifiedManifest(t *testing.T) {
	root, src, want := fixture(t)
	b, err := ImportFrozen(context.Background(), root, src, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DescribeVerified(context.Background(), root, b)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("description: %+v %v", got, err)
	}
	got.Files[0].Path = "changed"
	again, err := DescribeVerified(context.Background(), root, b)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal("manifest aliases caller storage")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := DescribeVerified(ctx, root, b); err == nil || got.Files != nil {
		t.Fatal("canceled description exposed metadata")
	}
	if err := os.WriteFile(filepath.Join(root, b.BundleDigest, "payload/lib/a"), []byte("bad"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := DescribeVerified(context.Background(), root, b); err == nil || got.Files != nil {
		t.Fatal("corrupt description exposed metadata")
	}
}
