package vmruntime

import "foundry/internal/platformfs"

// Keep the complete no-reparse/no-delete-sharing ancestor chain open through
// every destination write and publication. The private parent prevents another
// user from substituting archive children while these names are pinned.
func protectArchiveParent(path string) (func(), error) {
	if err := canonicalDirectory(path); err != nil {
		return nil, err
	}
	parent, err := platformfs.OpenDirectory(path)
	if err != nil {
		return nil, err
	}
	if err := checkArchiveDirectory(path); err != nil {
		parent.Close()
		return nil, err
	}
	return func() { parent.Close() }, nil
}

func publishArchive(source, target string) error { return platformfs.PublishAbsent(source, target) }
func checkArchiveDirectory(path string) error    { return platformfs.PrivateDirectory(path) }
