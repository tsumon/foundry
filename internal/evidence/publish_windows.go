package evidence

import "foundry/internal/platformfs"

func publishEvidence(source, target string) error { return platformfs.PublishAbsent(source, target) }
