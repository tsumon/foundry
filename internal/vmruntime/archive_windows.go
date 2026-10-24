package vmruntime

import "foundry/internal/platformfs"

func publishArchive(source, target string) error { return platformfs.PublishAbsent(source, target) }
func checkArchiveDirectory(path string) error    { return platformfs.PrivateDirectory(path) }
