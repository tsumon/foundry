package vmguest

import "errors"

type scratchMount struct {
	path, options string
	maxBytes      uint64
	noexec        bool
}

func scratchMounts() []scratchMount {
	return []scratchMount{
		{"/tmp", "size=64m,nr_inodes=16384,mode=1777", 64 << 20, true},
		{"/foundry-build", "size=256m,nr_inodes=16384,mode=0755", 256 << 20, false},
	}
}

// The Linux adapter reads these observations from statfs, never mount intent.
type scratchStat struct {
	bytes, inodes                uint64
	tmpfs, nosuid, nodev, noexec bool
}

func checkScratchMounts(stat func(string) (scratchStat, error)) error {
	for _, m := range scratchMounts() {
		s, err := stat(m.path)
		if err != nil {
			return err
		}
		if !s.tmpfs || s.bytes > m.maxBytes || s.inodes != 16384 || !s.nosuid || !s.nodev || (m.noexec && !s.noexec) {
			return errors.New("unsafe scratch mount")
		}
	}
	return nil
}
