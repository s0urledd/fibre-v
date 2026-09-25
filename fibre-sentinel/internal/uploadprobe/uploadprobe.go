// Package uploadprobe is groundwork for measuring the upload side of Fibre:
// what each validator did with a shard this observer uploaded as a
// synthetic publisher. Nothing in the observer runs it; cmd/sentinel-pub
// uses it only behind -upload-results, which is off by default.
//
// Where per-validator results come from. celestia-app's fibre.Client.Upload
// returns one thing to its caller: the signed payment promise, whose
// ValidatorSignatures say which validators signed before the client stopped
// at two thirds of stake. Why a validator did not sign — refused the shard,
// ran out of storage budget, timed out, or simply had not answered yet when
// quorum was reached — is not returned. The client does report it, though,
// through the OpenTelemetry tracer it is configured with (ClientConfig.Tracer):
// every per-validator delivery is an "upload_to" span carrying
// validator_address and rows_count, an event "rows_uploaded" when the shard
// was accepted, and, on failure, the final gRPC error recorded on the span
// (fibre/client_upload.go uploadTo). A span processor sees every span as it
// ends, including those of the deliveries the client keeps running in the
// background after quorum. That is the whole trick: no fork of the client, no
// log scraping, the reference client's own account of each attempt.
//
// Limits of that account, stated where the schema is: only the final attempt
// is described (a ResourceExhausted that succeeded on retry shows as a
// success with a longer duration); the duration includes the client's own
// RetryInfo waits; and a delivery the client abandoned because it was
// stopped reads as "canceled", which is the client's decision, not the
// validator's. None of these outcomes is a fault: see Reason.
package uploadprobe

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// SchemaVersion versions Result.
const SchemaVersion = 1

// Reason is what a delivery came to, in words that describe the exchange and
// accuse nobody. The probe load policy applies: an upload probe adds load
// to the validator it measures, so a refusal under load is information about
// capacity, and budget_exceeded in particular is the server doing exactly
// what x/fibre's storage budget tells it to.
type Reason string

const (
	// ReasonAccepted: the validator stored the shard and returned its
	// signature.
	ReasonAccepted Reason = "accepted"
	// ReasonBudgetExceeded: ResourceExhausted "fibre storage budget
	// exceeded" — the storage limiter refused it (server_upload.go). The
	// protocol's intended back-pressure; never a fault.
	ReasonBudgetExceeded Reason = "budget_exceeded"
	// ReasonResourceExhausted: any other ResourceExhausted (a per-peer rate
	// limit, a gRPC message bound).
	ReasonResourceExhausted Reason = "resource_exhausted"
	// ReasonRejected: InvalidArgument — the server did not accept the
	// promise, the assignment or the shard. Most often the server's chain
	// view differs from the client's (a validator set or params a block
	// behind); it is a disagreement, and the message says which check.
	ReasonRejected Reason = "rejected"
	// ReasonUnavailable: no connection, or the connection dropped.
	ReasonUnavailable Reason = "unavailable"
	// ReasonDeadline: the RPC did not finish within the client's per-peer
	// timeout.
	ReasonDeadline Reason = "deadline"
	// ReasonCanceled: the client stopped the delivery itself (it was
	// stopped, or the caller's context ended). The client's decision.
	ReasonCanceled Reason = "canceled"
	// ReasonServerError: Internal — the server failed to store the shard.
	ReasonServerError Reason = "server_error"
	// ReasonBadSignature: the server answered, but its signature could not
	// be parsed or did not verify against its key.
	ReasonBadSignature Reason = "bad_signature"
	// ReasonOther: anything else, with the error kept verbatim.
	ReasonOther Reason = "other"
)

// Classify maps the error text the client recorded on an upload_to span to
// a Reason. The text is a gRPC status string ("rpc error: code = X desc =
// ...") or one of the client's own wrapped errors; matching on the code
// words is deliberate, since the span carries the error's text and not the
// status object.
func Classify(errText string) Reason {
	e := errText
	switch {
	case e == "":
		return ReasonAccepted
	case strings.Contains(e, "code = ResourceExhausted"):
		if strings.Contains(e, "storage budget exceeded") {
			return ReasonBudgetExceeded
		}
		return ReasonResourceExhausted
	case strings.Contains(e, "code = InvalidArgument"):
		return ReasonRejected
	case strings.Contains(e, "code = Unavailable"):
		return ReasonUnavailable
	case strings.Contains(e, "code = DeadlineExceeded"), strings.Contains(e, "context deadline exceeded"):
		return ReasonDeadline
	case strings.Contains(e, "code = Canceled"), strings.Contains(e, "context canceled"):
		return ReasonCanceled
	case strings.Contains(e, "code = Internal"):
		return ReasonServerError
	case strings.Contains(e, "signature"):
		return ReasonBadSignature
	default:
		return ReasonOther
	}
}

// Result is one validator's handling of one uploaded shard: one line of the
// upload results file.
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	Vantage       string `json:"vantage,omitempty"`
	// Labels identify the upload (promise hash, commitment, blob size, run):
	// whatever the caller attached with Recorder.Label.
	Labels map[string]string `json:"labels,omitempty"`
	// ValidatorAddress is the consensus address as the client logs it
	// (uppercase hex).
	ValidatorAddress string    `json:"validator_address"`
	Rows             int64     `json:"rows"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	// DurationMS is the whole delivery: proof generation, every attempt and
	// any RetryInfo wait the client honoured.
	DurationMS int64 `json:"duration_ms"`
	// Delivered: the server accepted the rows (the client's rows_uploaded
	// event). Signed: the delivery ended without error, so the signature
	// was parsed and added.
	Delivered bool   `json:"delivered"`
	Signed    bool   `json:"signed"`
	Reason    Reason `json:"reason"`
	Error     string `json:"error,omitempty"`
}

// Recorder is an OpenTelemetry span processor that turns the fibre client's
// upload_to spans into Results. Install it on the tracer provider whose
// tracer goes into fibre.ClientConfig.Tracer.
type Recorder struct {
	Vantage string

	mu      sync.Mutex
	results []traced
	labels  map[trace.TraceID]map[string]string
}

type traced struct {
	trace trace.TraceID
	r     Result
}

var _ sdktrace.SpanProcessor = (*Recorder)(nil)

// NewRecorder returns an empty Recorder.
func NewRecorder(vantage string) *Recorder {
	return &Recorder{Vantage: vantage, labels: map[trace.TraceID]map[string]string{}}
}

// Label attaches labels to every upload_to span of the trace: the caller
// starts a span around fibre.Client.Upload with the same tracer and labels
// its trace once it knows the promise hash. Spans that ended before the
// label arrived get it when the results are written.
func (r *Recorder) Label(id trace.TraceID, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := map[string]string{}
	for k, v := range r.labels[id] {
		cp[k] = v
	}
	for k, v := range labels {
		cp[k] = v
	}
	r.labels[id] = cp
}

// OnStart implements sdktrace.SpanProcessor.
func (r *Recorder) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

// OnEnd implements sdktrace.SpanProcessor.
func (r *Recorder) OnEnd(s sdktrace.ReadOnlySpan) {
	if s.Name() != "upload_to" {
		return
	}
	res := Result{SchemaVersion: SchemaVersion, Vantage: r.Vantage, StartedAt: s.StartTime().UTC(), FinishedAt: s.EndTime().UTC()}
	res.DurationMS = s.EndTime().Sub(s.StartTime()).Milliseconds()
	for _, kv := range s.Attributes() {
		switch kv.Key {
		case "validator_address":
			res.ValidatorAddress = kv.Value.AsString()
		case "rows_count":
			res.Rows = kv.Value.AsInt64()
		}
	}
	var errText string
	for _, ev := range s.Events() {
		switch ev.Name {
		case "rows_uploaded":
			res.Delivered = true
		case "exception": // span.RecordError
			for _, kv := range ev.Attributes {
				if kv.Key == "exception.message" {
					errText = kv.Value.AsString()
				}
			}
		}
	}
	if s.Status().Code == codes.Error && errText == "" {
		errText = s.Status().Description
	}
	res.Signed = s.Status().Code != codes.Error
	if res.Signed {
		res.Reason = ReasonAccepted
	} else {
		res.Error = errText
		res.Reason = Classify(errText)
		if res.Reason == ReasonAccepted { // an error status with no text
			res.Reason = ReasonOther
		}
	}
	r.mu.Lock()
	r.results = append(r.results, traced{trace: s.SpanContext().TraceID(), r: res})
	r.mu.Unlock()
}

// Shutdown implements sdktrace.SpanProcessor.
func (r *Recorder) Shutdown(context.Context) error { return nil }

// ForceFlush implements sdktrace.SpanProcessor.
func (r *Recorder) ForceFlush(context.Context) error { return nil }

// Results returns what has been recorded so far, labelled, in start order.
func (r *Recorder) Results() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Result, 0, len(r.results))
	for _, t := range r.results {
		res := t.r
		if l := r.labels[t.trace]; len(l) > 0 {
			res.Labels = l
		}
		out = append(out, res)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// WriteJSONL writes every result as one JSON line.
func (r *Recorder) WriteJSONL(w io.Writer) error {
	enc := json.NewEncoder(w)
	for _, res := range r.Results() {
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
	return nil
}
