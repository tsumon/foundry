package vmguest

import (
	"errors"
	"fmt"
	"foundry/internal/domain"
	"strings"
	"testing"
)

func TestScratchMountOptionsEnforceApprovedInodeLimits(t *testing.T) {
	mounts := scratchMounts()
	policy := domain.FixedC4VMPolicy().Test.Tmpfs
	if len(mounts) != 2 || len(policy) != 2 {
		t.Fatal("missing scratch mount")
	}
	for i, m := range mounts {
		if m.path != policy[i].Path || m.maxBytes != uint64(policy[i].SizeBytes) || m.noexec == policy[i].Exec {
			t.Fatalf("mount differs from approved policy: %+v", m)
		}
		if policy[i].Inodes != 16384 || !strings.Contains(","+m.options+",", ",nr_inodes=16384,") {
			t.Errorf("%s omits approved inode limit: %s", m.path, m.options)
		}
	}
}

func TestScratchSelfCheckRejectsMissingOrWrongInodeLimits(t *testing.T) {
	for _, path := range []string{"/tmp", "/foundry-build"} {
		for _, inodes := range []uint64{0, 16383, 16384, 16385, ^uint64(0)} {
			t.Run(fmt.Sprintf("%s/%d", path, inodes), func(t *testing.T) {
				seen := map[string]bool{}
				err := checkScratchMounts(func(p string) (scratchStat, error) {
					seen[p] = true
					s := scratchStat{bytes: 64 << 20, inodes: 16384, tmpfs: true, nosuid: true, nodev: true, noexec: true}
					if p == path {
						s.inodes = inodes
					}
					return s, nil
				})
				if (err == nil) != (inodes == 16384) {
					t.Fatalf("observed inodes=%d: %v", inodes, err)
				}
				if inodes == 16384 && len(seen) != 2 {
					t.Fatal("did not observe both scratch filesystems")
				}
			})
		}
	}
}

func TestScratchSelfCheckPreservesOtherRequiredControls(t *testing.T) {
	for _, field := range []string{"stat", "tmpfs", "bytes", "nosuid", "nodev", "noexec"} {
		t.Run(field, func(t *testing.T) {
			err := checkScratchMounts(func(string) (scratchStat, error) {
				s := scratchStat{bytes: 64 << 20, inodes: 16384, tmpfs: true, nosuid: true, nodev: true, noexec: true}
				switch field {
				case "stat":
					return s, errors.New("missing mount")
				case "tmpfs":
					s.tmpfs = false
				case "bytes":
					s.bytes++
				case "nosuid":
					s.nosuid = false
				case "nodev":
					s.nodev = false
				case "noexec":
					s.noexec = false
				}
				return s, nil
			})
			if err == nil {
				t.Fatal("unsafe scratch accepted")
			}
		})
	}
}
