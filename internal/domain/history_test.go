package domain

import "testing"

func TestArchivedPassingEvidenceCannotAuthorizeDelivery(t *testing.T) {
	m := deliverableMission()
	m.RunArchives = []RunArchive{{Epoch: 1, PlanRevision: 3, CandidateDigest: "digest", Evidence: m.Evidence, Reviews: m.Reviews}}
	m.Evidence = nil
	m.Reviews = nil
	if err := CanDeliver(m, "digest"); err == nil {
		t.Fatal("historical passing evidence authorized current delivery")
	}
}
