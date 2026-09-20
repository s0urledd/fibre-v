package scan

import (
	"testing"
	"time"
)

func verified(from, to int64, values ...ResolvedValue) ParamUncertainty {
	return ParamUncertainty{
		Kind: UncertaintySilentChange, FromHeight: from, ToHeight: to,
		Resolution: ResolutionVerified, Values: values,
	}
}

func value(at int64, timeout, retention time.Duration) ResolvedValue {
	p := params(timeout, retention, 13*time.Hour)
	return ResolvedValue{FromHeight: at, Params: snapshotParams(p, at, -1, "verified")}
}

// The whole correction is this: put the values that were really in force
// into the history, then call the function that was always right. A proven
// shorter window moves the deadline earlier, which is what turns an
// in-window NOT_FOUND into an expected one.
func TestAProvenShorterWindowMovesTheDeadlineEarlier(t *testing.T) {
	created := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	base := NewParamHistory(100, params(10*time.Minute, 4*time.Hour, 13*time.Hour))
	// The reconcile at 180 saw the shorter value and could only vouch for
	// it from 181, which is after this publication settled.
	base.add(181, -1, "reconcile", params(10*time.Minute, time.Hour, 13*time.Hour))

	msu, _, _, _, ok := base.MustServeUntilForPromise(created, 149, 150, 0)
	if !ok || !msu.Equal(created.Add(4*time.Hour)) {
		t.Fatalf("as recorded: msu=%v ok=%v, want creation+4h", msu, ok)
	}

	// Verification proves the shorter value was in force from 150.
	h := ResolvedHistory(base.Entries(), []ParamUncertainty{
		verified(121, 180, value(120, 10*time.Minute, 4*time.Hour), value(150, 10*time.Minute, time.Hour)),
	})
	got, _, _, _, ok := h.MustServeUntilForPromise(created, 149, 150, 0)
	if !ok {
		t.Fatal("no entry after verification")
	}
	want := created.Add(time.Hour)
	if !got.Equal(want) {
		t.Fatalf("after verification: msu=%v, want %v", got, want)
	}
	if corrected, moved := CorrectedDeadline(msu, got); !moved || !corrected.Equal(want) {
		t.Fatalf("CorrectedDeadline(%v, %v) = %v, %v", msu, got, corrected, moved)
	}
}

// Verifying a range can make a recomputed deadline LATER: a value proven to
// have started before the promise height replaces the one the history had
// there rather than joining it, and if the replacement is longer the
// earliest bound rises. That would turn a validator that read clean into a
// FAULT on evidence the observer did not hold when it published the clean
// reading, so the correction clamps. This test exists because the clamp is
// not obviously necessary until you see this case.
func TestAVerificationNeverMovesAPublishedDeadlineLater(t *testing.T) {
	created := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	base := NewParamHistory(100, params(10*time.Minute, time.Hour, 13*time.Hour))
	base.add(181, -1, "reconcile", params(10*time.Minute, 4*time.Hour, 13*time.Hour))

	recorded, _, _, _, _ := base.MustServeUntilForPromise(created, 149, 150, 0)
	if want := created.Add(time.Hour); !recorded.Equal(want) {
		t.Fatalf("as recorded: %v, want %v", recorded, want)
	}

	// The longer value turns out to have been in force from 140, i.e.
	// before this publication's promise height.
	h := ResolvedHistory(base.Entries(), []ParamUncertainty{
		verified(121, 180, value(120, 10*time.Minute, time.Hour), value(140, 10*time.Minute, 4*time.Hour)),
	})
	recomputed, _, _, _, _ := h.MustServeUntilForPromise(created, 149, 150, 0)
	if !recomputed.After(recorded) {
		t.Fatalf("this test no longer exercises the case it exists for: recomputed=%v recorded=%v", recomputed, recorded)
	}
	corrected, moved := CorrectedDeadline(recorded, recomputed)
	if moved || !corrected.Equal(recorded) {
		t.Fatalf("a correction moved a deadline later: %v -> %v", recorded, corrected)
	}
}

// Over every arrangement of one range and one publication, the corrected
// deadline is never later than the recorded one. The clamp is what makes
// this hold, and it is the property that lets a correction be applied to a
// published figure without review: it can withdraw an accusation, never
// make one.
func TestACorrectionIsNeverAnAccusation(t *testing.T) {
	created := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	windows := []time.Duration{30 * time.Minute, time.Hour, 4 * time.Hour, 13 * time.Hour}
	for _, before := range windows {
		for _, after := range windows {
			for _, provenAt := range []int64{120, 140, 150, 160, 180} {
				for _, promise := range []int64{110, 130, 149, 175} {
					base := NewParamHistory(100, params(10*time.Minute, before, 13*time.Hour))
					base.add(181, -1, "reconcile", params(10*time.Minute, after, 13*time.Hour))
					recorded, _, _, _, ok := base.MustServeUntilForPromise(created, promise, promise+1, 0)
					if !ok {
						continue
					}
					h := ResolvedHistory(base.Entries(), []ParamUncertainty{
						verified(121, 180,
							value(120, 10*time.Minute, before),
							value(provenAt, 10*time.Minute, after)),
					})
					recomputed, _, _, _, _ := h.MustServeUntilForPromise(created, promise, promise+1, 0)
					corrected, _ := CorrectedDeadline(recorded, recomputed)
					if corrected.After(recorded) {
						t.Fatalf("before=%v after=%v provenAt=%d promise=%d: corrected %v is later than recorded %v",
							before, after, provenAt, promise, corrected, recorded)
					}
				}
			}
		}
	}
}

// Covers is the whole definition of "affected": the publication's upload
// interval, which starts one block before the promise height because the
// chain accepts a promise one block ahead of the validating node's latest,
// against the range. It is deliberately generous at both ends.
func TestCoversIsTheOverlapOfTheUploadIntervalAndTheRange(t *testing.T) {
	u := ParamUncertainty{FromHeight: 121, ToHeight: 180}
	cases := []struct {
		promise, settlement int64
		want                bool
		why                 string
	}{
		{150, 155, true, "wholly inside"},
		{100, 130, true, "settles inside"},
		{175, 200, true, "promise inside, settles after"},
		{100, 250, true, "straddles the whole range"},
		{122, 122, true, "one block inside"},
		{100, 120, false, "settles before the range"},
		{181, 190, true, "promise-1 is the range's last height: the server may have validated against state inside it"},
		{182, 190, false, "promise-1 is past the range's last height"},
		{122, 119, false, "settles before it started: not a real publication, but must not be covered"},
	}
	for _, c := range cases {
		if got := u.Covers(c.promise, c.settlement); got != c.want {
			t.Errorf("Covers(%d, %d) = %v, want %v (%s)", c.promise, c.settlement, got, c.want, c.why)
		}
	}
}

// Only an unresolved silent change withholds anything.
func TestOnlyAnUnresolvedSilentChangeHolds(t *testing.T) {
	cases := []struct {
		kind, resolution string
		want             bool
	}{
		{UncertaintySilentChange, ResolutionOpen, true},
		{UncertaintySilentChange, ResolutionUnresolvable, true},
		{UncertaintySilentChange, ResolutionVerified, false},
		{UncertaintyCheckSkipped, ResolutionOpen, false},
		{UncertaintyCheckSkipped, ResolutionUnresolvable, false},
	}
	for _, c := range cases {
		u := ParamUncertainty{Kind: c.kind, Resolution: c.resolution}
		if got := u.Holds(); got != c.want {
			t.Errorf("%s/%q holds = %v, want %v", c.kind, c.resolution, got, c.want)
		}
	}
}
