package main

import (
	"errors"
	"testing"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// Fibre is live when the chain is on its version and x/fibre answers, not
// when the version alone says so: at the upgrade height the old binary
// reports v10 with no module behind it.
func TestFibreIsActiveOnlyOnceTheModuleAnswers(t *testing.T) {
	unknownPath := &scan.ABCIError{Code: 6, Codespace: "sdk", Log: "unknown query path"}
	calls := 0
	for _, c := range []struct {
		name    string
		av      uint64
		err     error
		verdict string
		known   bool
		asks    bool
	}{
		{"below the Fibre version", 9, nil, "no", true, false},
		{"v10 reported, module not there (halted old binary)", 10, unknownPath, "no", true, true},
		{"v10 and x/fibre answers", 10, nil, "yes", true, true},
		{"v10, node busy", 10, errors.New("server busy"), "", false, true},
	} {
		calls = 0
		v, known := fibreActive(c.av, func() error { calls++; return c.err })
		if v != c.verdict || known != c.known || (calls > 0) != c.asks {
			t.Errorf("%s: verdict=%q known=%v asked=%d", c.name, v, known, calls)
		}
	}
}
