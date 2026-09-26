package api_test

import (
	"testing"
)

type loadJSON struct {
	Promises      int64 `json:"promises"`
	Rows          int64 `json:"rows"`
	Bytes         int64 `json:"bytes"`
	StoredBytes   int64 `json:"stored_bytes"`
	RowsPerBlob   int64 `json:"rows_per_blob"`
	EstIngressBps int64 `json:"est_ingress_bps"`
	EstDiskBytes  int64 `json:"est_disk_bytes"`
}

// Load counts the rows every settled promise assigned a validator and their
// row data (blob_size / original_rows each), leaves out a failed transaction
// and an assignment made while the validator had no host, and sizes the
// newest assignment at mainnet scale: 148 rows of a 128 MiB blob at 2.2 GB/s,
// kept for four hours.
func TestLoadPerValidator(t *testing.T) {
	ts, _, _ := signingFixture(t)
	// p1..p4 settled (p4 before signatures were verified still carries an
	// assignment); p5's transaction failed. Every assignment is 148 rows of a
	// 1024-byte blob with 4096 original rows: 37 bytes of row data.
	var det struct {
		Validator struct {
			Load loadJSON `json:"load"`
		} `json:"validator"`
	}
	if code := get(t, ts, "/v1/validators/"+sigV1+"?window=all", &det); code != 200 {
		t.Fatalf("detail: %d", code)
	}
	l := det.Validator.Load
	if l.Promises != 4 || l.Rows != 4*148 || l.Bytes != 4*37 {
		t.Fatalf("load = %+v, want 4 promises, 592 rows, 148 bytes", l)
	}
	if l.StoredBytes != 0 {
		t.Errorf("stored = %d, want 0: every window in the fixture has ended", l.StoredBytes)
	}
	if l.RowsPerBlob != 148 {
		t.Errorf("rows per blob = %d, want 148", l.RowsPerBlob)
	}
	// 148 rows x 32 KiB = 4,849,664 bytes per 128 MiB blob, at 2.2e9 B/s of blobs
	perBlob := 148.0 * 32768
	bps := perBlob * 2.2e9 / float64(128<<20)
	if want := int64(bps * 8); l.EstIngressBps != want {
		t.Errorf("ingress estimate = %d bit/s, want %d", l.EstIngressBps, want)
	}
	if want := int64(bps * 4 * 3600); l.EstDiskBytes != want {
		t.Errorf("disk estimate = %d bytes, want %d", l.EstDiskBytes, want)
	}

}

// An assignment made while the validator had no host is not load it could
// carry: it is left out, as it is from the endorsement rate.
func TestLoadLeavesOutAssignmentsWithoutAHost(t *testing.T) {
	ts, st, _ := signingFixture(t)
	if _, err := st.DB().Exec(`UPDATE assignments SET host_at_settlement = '' WHERE validator_address = ? AND promise_hash = 'p1'`, sigV1); err != nil {
		t.Fatal(err)
	}
	var det struct {
		Validator struct {
			Load loadJSON `json:"load"`
		} `json:"validator"`
	}
	if code := get(t, ts, "/v1/validators/"+sigV1+"?window=all", &det); code != 200 {
		t.Fatalf("detail: %d", code)
	}
	if l := det.Validator.Load; l.Promises != 3 || l.Rows != 3*148 || l.Bytes != 3*37 {
		t.Fatalf("load = %+v, want 3 promises, 444 rows, 111 bytes", l)
	}
}
