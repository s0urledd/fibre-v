package uploadprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The error texts are what the fibre server returns and the client records,
// produced here with the same gRPC status calls the server makes.
func TestClassify(t *testing.T) {
	cases := map[string]Reason{
		"": ReasonAccepted,
		status.Error(grpccodes.ResourceExhausted, "fibre storage budget exceeded").Error():                     ReasonBudgetExceeded,
		status.Error(grpccodes.ResourceExhausted, "grpc: received message larger than max").Error():            ReasonResourceExhausted,
		status.Error(grpccodes.InvalidArgument, "payment promise verification failed: height too far").Error(): ReasonRejected,
		status.Error(grpccodes.Unavailable, "connection refused").Error():                                      ReasonUnavailable,
		status.Error(grpccodes.DeadlineExceeded, "context deadline exceeded").Error():                          ReasonDeadline,
		"context canceled": ReasonCanceled,
		status.Error(grpccodes.Internal, "failed to store upload data: disk full").Error(): ReasonServerError,
		"validator signature is empty": ReasonBadSignature,
		"something new":                ReasonOther,
	}
	for text, want := range cases {
		if got := Classify(text); got != want {
			t.Errorf("Classify(%q) = %s, want %s", text, got, want)
		}
	}
}

// The recorder turns the client's upload_to spans — and only those — into
// results, as the client shapes them: attributes on start, rows_uploaded on
// acceptance, RecordError plus an error status on failure. Labels given to
// the trace after the spans ended still reach them.
func TestRecorderReadsTheClientsSpans(t *testing.T) {
	rec := NewRecorder("test")
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	defer tp.Shutdown(context.Background())
	tr := tp.Tracer("fibre-client")

	ctx, parent := tr.Start(context.Background(), "sentinel-pub.upload")
	_, up := tr.Start(ctx, "fibre.Client.Upload") // not a per-validator span
	_, ok := tr.Start(ctx, "upload_to", trace.WithAttributes(attribute.String("validator_address", "AAAA"), attribute.Int("rows_count", 148)))
	ok.AddEvent("proofs_added")
	ok.AddEvent("rows_uploaded")
	ok.End()
	_, full := tr.Start(ctx, "upload_to", trace.WithAttributes(attribute.String("validator_address", "BBBB"), attribute.Int("rows_count", 4096)))
	full.RecordError(status.Error(grpccodes.ResourceExhausted, "fibre storage budget exceeded"))
	full.SetStatus(codes.Error, "failed to upload rows")
	full.End()
	up.End()
	parent.End()
	rec.Label(parent.SpanContext().TraceID(), map[string]string{"promise_hash": "abcd"})

	got := rec.Results()
	if len(got) != 2 {
		t.Fatalf("%d results, want the two upload_to spans: %+v", len(got), got)
	}
	a, b := got[0], got[1]
	if a.ValidatorAddress != "AAAA" || a.Rows != 148 || !a.Delivered || !a.Signed || a.Reason != ReasonAccepted || a.Error != "" {
		t.Errorf("accepted = %+v", a)
	}
	if b.ValidatorAddress != "BBBB" || b.Delivered || b.Signed || b.Reason != ReasonBudgetExceeded || !strings.Contains(b.Error, "budget exceeded") {
		t.Errorf("refused = %+v", b)
	}
	for _, r := range got {
		if r.Labels["promise_hash"] != "abcd" || r.Vantage != "test" || r.SchemaVersion != SchemaVersion {
			t.Errorf("labels/vantage = %+v", r)
		}
	}
	var buf bytes.Buffer
	if err := rec.WriteJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var back Result
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &back) != nil || back.Reason != ReasonBudgetExceeded {
		t.Errorf("jsonl = %q", buf.String())
	}
}
