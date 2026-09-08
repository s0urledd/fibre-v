// Command sentinel-verify checks a scanner's publications.jsonl against a set
// of expected commitments and independently recomputed must_serve_until values.
// It is a test/CI helper.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func main() {
	var (
		dataDir    = flag.String("data-dir", "./sentinel-data", "scanner data dir")
		expect     = flag.String("expect-commitments", "", "comma-separated hex commitments that must be present")
		promiseTO  = flag.Duration("promise-timeout", 10*time.Minute, "devnet genesis payment_promise_timeout")
		retention  = flag.Duration("shard-retention", 10*time.Minute, "devnet genesis shard_retention")
		minWithRow = flag.Int("min-validators-with-rows", 2, "minimum validators that must hold rows")
		tol        = flag.Duration("tolerance", time.Second, "allowed must_serve_until slack")
	)
	flag.Parse()

	pubs := load(filepath.Join(*dataDir, "publications.jsonl"))
	fmt.Printf("verify| loaded %d publications from %s\n", len(pubs), *dataDir)

	byCommit := map[string]scan.Publication{}
	for _, p := range pubs {
		byCommit[strings.ToLower(p.Promise.Commitment)] = p
	}

	window := *promiseTO
	if *retention > window {
		window = *retention
	}

	fails := 0
	var wanted []string
	if strings.TrimSpace(*expect) != "" {
		wanted = strings.Split(*expect, ",")
	} else {
		for c := range byCommit {
			wanted = append(wanted, c)
		}
	}

	for _, raw := range wanted {
		c := strings.ToLower(strings.TrimSpace(raw))
		if c == "" {
			continue
		}
		p, ok := byCommit[c]
		if !ok {
			fmt.Printf("FAIL %s: not found in publications.jsonl\n", short(c))
			fails++
			continue
		}
		var problems []string

		if p.SchemaVersion != scan.SchemaVersion {
			problems = append(problems, fmt.Sprintf("schema_version=%d", p.SchemaVersion))
		}
		wantMSU := p.Promise.CreationTimestamp.Add(window)
		if d := p.MustServeUntil.Sub(wantMSU); d > *tol || d < -*tol {
			problems = append(problems, fmt.Sprintf("must_serve_until=%s want %s (creation %s + %s)",
				p.MustServeUntil.Format(time.RFC3339), wantMSU.Format(time.RFC3339), p.Promise.CreationTimestamp.Format(time.RFC3339), window))
		}
		if p.ParamsAtPublication.PaymentPromiseTimeoutSeconds != int64(promiseTO.Seconds()) {
			problems = append(problems, fmt.Sprintf("params.payment_promise_timeout=%ds want %ds",
				p.ParamsAtPublication.PaymentPromiseTimeoutSeconds, int64(promiseTO.Seconds())))
		}
		if p.ParamsAtPublication.ShardRetentionSeconds != int64(retention.Seconds()) {
			problems = append(problems, fmt.Sprintf("params.shard_retention=%ds want %ds",
				p.ParamsAtPublication.ShardRetentionSeconds, int64(retention.Seconds())))
		}
		a := p.Assignment
		if a.Error != "" {
			problems = append(problems, "assignment error: "+a.Error)
		}
		if a.ValidatorsWithRows < *minWithRow {
			problems = append(problems, fmt.Sprintf("validators_with_rows=%d < %d", a.ValidatorsWithRows, *minWithRow))
		}
		sum := 0
		for _, v := range a.Validators {
			if v.RowCount != len(v.Rows) && len(v.Rows) != 0 {
				problems = append(problems, fmt.Sprintf("validator %s row_count=%d len(rows)=%d", short(v.Address), v.RowCount, len(v.Rows)))
			}
			sum += v.RowCount
		}
		if sum != a.Sigma {
			problems = append(problems, fmt.Sprintf("sum(row_count)=%d != sigma=%d", sum, a.Sigma))
		}
		if a.Distinct+a.WrapOverlaps > a.Sigma && a.Sigma > 0 {
			problems = append(problems, fmt.Sprintf("distinct=%d overlaps=%d inconsistent with sigma=%d", a.Distinct, a.WrapOverlaps, a.Sigma))
		}
		if a.ValidatorSetHeight != p.Promise.Height {
			problems = append(problems, fmt.Sprintf("assignment valset height %d != promise height %d", a.ValidatorSetHeight, p.Promise.Height))
		}

		if len(problems) == 0 {
			fmt.Printf("PASS %s  settle_h=%d valset_h=%d with_rows=%d sigma=%d distinct=%d overlaps=%d must_serve_until=%s\n",
				short(c), p.SettlementHeight, p.Promise.Height, a.ValidatorsWithRows, a.Sigma, a.Distinct, a.WrapOverlaps,
				p.MustServeUntil.Format(time.RFC3339))
		} else {
			fmt.Printf("FAIL %s:\n", short(c))
			for _, pr := range problems {
				fmt.Printf("     - %s\n", pr)
			}
			fails++
		}
	}

	fmt.Printf("verify| %d checked, %d failed\n", len(wanted), fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func load(path string) []scan.Publication {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-FATAL: open %s: %v\n", path, err)
		os.Exit(2)
	}
	defer f.Close()
	var out []scan.Publication
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<27)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		var p scan.Publication
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			fmt.Fprintf(os.Stderr, "verify-FATAL: bad jsonl line: %v\n", err)
			os.Exit(2)
		}
		out = append(out, p)
	}
	return out
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
