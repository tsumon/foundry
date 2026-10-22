package workspace

import "foundry/internal/platformfs"

func publishAbsent(source, target string) error { return platformfs.PublishAbsent(source, target) }
