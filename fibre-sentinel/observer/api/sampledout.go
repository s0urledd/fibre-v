package api

import (
	"context"
	"strings"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// A publication the load policy sampled out is recorded once, as a decision
// (store/sampledout.go), not as a NOT_PROBED row per assigned validator per
// point. Every figure counts the rows the decision stands for (probe_rows);
// what lists rows (/v1/probes, a blob's probe table) lists the decision
// itself instead, once, with the probability it was drawn at.

// sampledOutFor returns the decision behind each of hashes that was sampled
// out, this observer's own vantage first when more than one decided it.
func (s *Server) sampledOutFor(ctx context.Context, hashes []string) (map[string]*store.SampledOutDecision, error) {
	out := map[string]*store.SampledOutDecision{}
	if len(hashes) == 0 {
		return out, nil
	}
	args := make([]any, len(hashes))
	marks := make([]string, len(hashes))
	for i, h := range hashes {
		args[i], marks[i] = h, "?"
	}
	ds, err := s.st.SampledOutDecisions(ctx, `d.promise_hash IN (`+strings.Join(marks, ",")+`)`, len(hashes)*4, args...)
	if err != nil {
		return nil, err
	}
	for i := range ds {
		d := &ds[i]
		if cur, ok := out[d.PromiseHash]; ok && cur.Vantage == s.vantage {
			continue
		}
		out[d.PromiseHash] = d
	}
	return out, nil
}
