package status

import "testing"

// A test binary carries no VCS stamp, so BuildRevision falls through to the
// link-time revision: what a tarball or Docker build ships with.
func TestBuildRevisionFallsBackToTheLinkTimeStamp(t *testing.T) {
	old := revision
	t.Cleanup(func() { revision = old })

	revision = ""
	if got := BuildRevision(); got != "unknown" {
		t.Errorf("no VCS and no stamp: %q, want unknown", got)
	}
	revision = "f05c63a1b2c3\n"
	if got := BuildRevision(); got != "f05c63a1b2c3" {
		t.Errorf("stamped build: %q, want the stamp", got)
	}
}
