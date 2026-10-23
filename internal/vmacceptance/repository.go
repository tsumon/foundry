package vmacceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"foundry/internal/gitexec"
	"foundry/internal/platformfs"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

const protectedTests = `package triple
import("testing";"os";"net";"strings";"syscall")
func TestTriplePositive(t *testing.T){
 if os.Getuid()!=65534||os.Getgid()!=65534{t.Fatal("test identity is not nobody")}
 groups,err:=os.Getgroups();if err!=nil||len(groups)!=0{t.Fatal("supplemental groups are not empty")}
 for _,p:=range []string{"/workspace/.foundry-acceptance-marker","/usr/local/go/.foundry-acceptance-marker"}{
  f,err:=os.OpenFile(p,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600)
  if err==nil{f.Close();os.Remove(p);t.Fatal("protected filesystem is writable")}
 }
 for _,p:=range []string{"/proc/1/mem","/dev/console","/dev/hvc0"}{fd,err:=syscall.Open(p,syscall.O_RDONLY|syscall.O_NONBLOCK,0);if err==nil{syscall.Close(fd);t.Fatal("privileged control file is accessible")}}
 status,err:=os.ReadFile("/proc/self/status");if err!=nil||!strings.Contains(string(status),"NoNewPrivs:\t1"){t.Fatal("no_new_privileges is not set")}
 interfaces,err:=net.Interfaces();if err!=nil||len(interfaces)!=1{t.Fatal("unexpected network interface")}
 for _,iface:=range interfaces{addresses,err:=iface.Addrs();if err!=nil||iface.Name!="lo"||iface.Flags&net.FlagUp!=0||len(addresses)!=0{t.Fatal("network is not isolated")}}
 if got:=Triple(4);got!=12{t.Fatalf("Triple(4)=%d; want 12",got)}
}
func TestTripleNegative(t *testing.T){if got:=Triple(-2);got!=-6{t.Fatalf("Triple(-2)=%d; want -6",got)}}
`
const moduleSource = "module foundry-native-acceptance\n\ngo 1.27.3\n"

func createRepository(ctx context.Context, path string) error {
	if err := platformfs.PrivateDirectory(path); err != nil {
		return err
	}
	for name, body := range map[string]string{"triple.go": initialSource, "triple_test.go": protectedTests, "go.mod": moduleSource} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0600); err != nil {
			return err
		}
	}
	cfg, err := gitexec.New()
	if err != nil {
		return err
	}
	defer cfg.Close()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "--", "triple.go", "triple_test.go", "go.mod"}, {"-c", "user.name=SYNTHETIC acceptance", "-c", "user.email=synthetic@example.invalid", "commit", "-qm", "SYNTHETIC disposable native acceptance input"}} {
		cmd := exec.CommandContext(ctx, cfg.Path, append([]string{"-c", "core.hooksPath=" + cfg.Hooks, "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
		cmd.Env = cfg.Environment
		cmd.Dir = path
		if err := cmd.Run(); err != nil {
			return errors.New("disposable_repository_creation_failed")
		}
	}
	return nil
}
func digestBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func protectedDigests(path string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range []string{"go.mod", "triple_test.go"} {
		b, e := os.ReadFile(filepath.Join(path, name))
		if e != nil {
			return nil, e
		}
		out[name] = digestBytes(b)
	}
	return out, nil
}

func sourceTreeDigest(root string) (string, error) {
	h := sha256.New()
	count, total := 0, int64(0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() && !info.Mode().IsRegular() {
			return errors.New("source_tree_not_regular")
		}
		count++
		total += info.Size()
		if count > 1024 || total > 8<<20 {
			return errors.New("source_tree_unbounded")
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), info.Mode())
		if !entry.IsDir() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			fmt.Fprintf(h, "%d\x00", len(b))
			h.Write(b)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
