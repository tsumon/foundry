package vmruntime

import (
	"errors"
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"path/filepath"
	"regexp"
	"strings"
)

var localVolumeDevice = regexp.MustCompile(`^\\Device\\HarddiskVolume[0-9]+$`)

// Compare actual NT device paths, not DOS drive letters (Rel fails across
// drives), nor volume GUID aliases (one volume may have several). All existing
// canonical/manifest checks precede this, and handles retain pinned ancestors.
// This is a metadata check only: it launches nothing and creates no instances.
func validateInstanceLocation(bundleRoot, instanceRoot string) error {
	bundle, err := platformfs.OpenDirectory(bundleRoot)
	if err != nil {
		return errors.New("cannot verify immutable bundle location")
	}
	defer bundle.Close()
	instances, err := platformfs.OpenDirectory(instanceRoot)
	if err != nil {
		return errors.New("cannot verify instance location")
	}
	defer instances.Close()
	bundlePath, err := physicalDirectoryPath(bundle)
	if err != nil {
		return err
	}
	instancePath, err := physicalDirectoryPath(instances)
	if err != nil {
		return err
	}
	if strings.EqualFold(bundlePath, instancePath) || strings.HasPrefix(strings.ToLower(instancePath), strings.ToLower(bundlePath)+`\`) {
		return errors.New("instance root must be outside immutable bundle")
	}
	return nil
}

func physicalDirectoryPath(directory *platformfs.File) (string, error) {
	// VOLUME_NAME_NT | FILE_NAME_NORMALIZED; no resize/retry or weaker fallback.
	// Win32's maximum extended path fits this bounded UTF-16 buffer.
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(directory.Fd()), &buf[0], uint32(len(buf)), 0x2)
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		return "", errors.New("physical directory identity unavailable")
	}
	physical := windows.UTF16ToString(buf[:n])
	drive, err := windows.UTF16PtrFromString(filepath.VolumeName(directory.Name()))
	if err != nil {
		return "", errors.New("physical drive identity unavailable")
	}
	n, err = windows.QueryDosDevice(drive, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		return "", errors.New("physical drive identity unavailable")
	}
	// QueryDosDevice's first string is the current mapping. Reject SUBST,
	// network and indirect namespaces; do not add support for drive aliases.
	device := windows.UTF16ToString(buf[:n])
	if !localVolumeDevice.MatchString(device) || !strings.EqualFold(physical, device+directory.Name()[2:]) {
		return "", errors.New("indirect or mismatched drive mapping refused")
	}
	return physical, nil
}
