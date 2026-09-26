package probe

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShouldRetry(t *testing.T) {
	settle := time.Now().UTC()
	msu := settle.Add(10 * time.Minute)
	p := pub(settle, msu)
	cfg := DefaultScheduleConfig()
	delay := 20 * time.Second

	tcpTimeout := Measurement{Outcome: OutcomeTCPTimeout, Phase: PhaseInWindow, TCP: StepResult{Attempted: true, Error: "dial tcp 10.0.0.1:7980: i/o timeout"}}
	tlsTimeout := Measurement{Outcome: OutcomeTLSFail, Phase: PhaseInWindow, TLS: TLSResult{Attempted: true, Error: "context deadline exceeded"}}
	tlsBadCert := Measurement{Outcome: OutcomeTLSFail, Phase: PhaseInWindow, TLS: TLSResult{Attempted: true, Error: "tls: protocol version not supported"}}
	rpcUnavailTimeout := Measurement{Outcome: OutcomeRPCUnavailable, Phase: PhaseInWindow, Download: DownloadResult{Attempted: true, Error: "rpc error: code = Unavailable desc = connection error: i/o timeout"}}
	rpcUnavailRefused := Measurement{Outcome: OutcomeRPCUnavailable, Phase: PhaseInWindow, Download: DownloadResult{Attempted: true, Error: "rpc error: code = Unavailable desc = connection refused"}}
	rpcErrDeadline := Measurement{Outcome: OutcomeRPCError, Phase: PhaseInWindow, Download: DownloadResult{Attempted: true, Error: "rpc error: code = DeadlineExceeded"}}
	refused := Measurement{Outcome: OutcomeTCPRefused, Phase: PhaseInWindow}
	notFound := Measurement{Outcome: OutcomeNotFound, Phase: PhaseInWindow}
	// A download that stalled: early in the window it is left to the later
	// points; in the tail (the last quarter, msu-2m30s on this 10-minute
	// window) it is the reading the served verdict rests on, so it is retried.
	tailDeadline := Measurement{Outcome: OutcomeRPCDeadline, Phase: PhaseInWindow, ScheduledAt: msu.Add(-2 * time.Minute)}
	earlyDeadline := Measurement{Outcome: OutcomeRPCDeadline, Phase: PhaseInWindow, ScheduledAt: settle.Add(time.Minute)}
	alreadyRetried := tcpTimeout
	alreadyRetried.Retry = &RetryInfo{Attempts: 2}

	now := msu.Add(-5 * time.Minute) // retry lands in-window too
	cases := []struct {
		name string
		m    Measurement
		now  time.Time
		want bool
	}{
		{"tcp timeout", tcpTimeout, now, true},
		{"tls handshake timeout", tlsTimeout, now, true},
		{"tls protocol error", tlsBadCert, now, false},
		{"grpc unavailable timeout", rpcUnavailTimeout, now, true},
		{"grpc unavailable refused", rpcUnavailRefused, now, false},
		{"download deadline is not transport", rpcErrDeadline, now, false},
		{"tcp refused", refused, now, false},
		{"not found", notFound, now, false},
		{"already retried", alreadyRetried, now, false},
		{"retry would cross into grace", tcpTimeout, msu.Add(-delay / 2), false},
		{"retry stays in window", tcpTimeout, msu.Add(-delay - time.Second), true},
		{"download deadline in the tail", tailDeadline, msu.Add(-time.Minute), true},
		{"download deadline early in the window", earlyDeadline, msu.Add(-time.Minute), false},
		{"tail deadline whose retry would cross into grace", tailDeadline, msu.Add(-delay / 2), false},
	}
	for _, c := range cases {
		if got := shouldRetry(c.m, p, cfg, delay, c.now); got != c.want {
			t.Errorf("%s: shouldRetry = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRetryOnce_RecordsFirstAttempt(t *testing.T) {
	settle := time.Now().UTC()
	msu := settle.Add(10 * time.Minute)
	p := pub(settle, msu)
	first := Measurement{
		Outcome: OutcomeTCPTimeout, Phase: PhaseInWindow, RawError: "dial tcp: i/o timeout",
		StartedAt: time.Now().UTC().Add(-25 * time.Second), TotalDurationMS: 5000,
	}
	// A target whose host cannot be split fails before any network step,
	// deterministically, so the second attempt is offline-testable.
	in := Input{
		Vantage: "test", PromiseHash: p.PromiseHash, MustServeUntil: msu,
		Target:         Target{AddressHex: "aa", Host: "no-port-here", Assigned: true, RowCount: 148, PubKey: make([]byte, 32)},
		SchedulePoint:  SchedulePoint{Label: "w1", At: time.Now().UTC()},
		PruneTolerance: DefaultScheduleConfig().PruneTolerance,
	}
	m := retryOnce(context.Background(), in, nil, StepTimeouts{}, first, 20*time.Second)
	if m.Retry == nil {
		t.Fatal("Retry not recorded")
	}
	if m.Retry.Attempts != 2 || m.Retry.FirstOutcome != OutcomeTCPTimeout || m.Retry.FirstError != first.RawError || m.Retry.DelayMS != 20000 || m.Retry.FirstDurationMS != 5000 {
		t.Errorf("Retry = %+v", *m.Retry)
	}
	if !m.Retry.FirstStartedAt.Equal(first.StartedAt) {
		t.Errorf("FirstStartedAt = %s, want %s", m.Retry.FirstStartedAt, first.StartedAt)
	}
	if m.Outcome != OutcomeProbeError {
		t.Errorf("second attempt outcome = %s, want %s", m.Outcome, OutcomeProbeError)
	}
	if !containsStr(m.ClassificationReason, "first attempt TCP_TIMEOUT") {
		t.Errorf("classification reason does not mention the first attempt: %q", m.ClassificationReason)
	}
}

func TestSleepCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Minute) {
		t.Error("sleepCtx returned true on a cancelled context")
	}
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Error("sleepCtx returned false after the wait completed")
	}
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }
