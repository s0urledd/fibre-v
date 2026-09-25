package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The collector judges confirmations every pass: the query has to start from
// the partial index of unjudged answers and reach each fault by its key,
// never walk probes or the confirmations already judged.
func TestJudgeConfirmationsSeeksWhatIsNew(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plan, err := st.QueryPlan(context.Background(), judgeConfirmationsSQL)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if bad := FullScans(plan, nil, []string{"probe_confirmations_open"}); len(bad) > 0 {
		t.Errorf("walks a whole table: %v\n%s", bad, joined)
	}
	for _, want := range []string{"probe_confirmations_open", "pr USING INDEX sqlite_autoindex_probes_1 (dedupe_key=?)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("plan does not use %q:\n%s", want, joined)
		}
	}
}
