package probe

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// hostRegistry tails registry.jsonl, the collector's own log of every
// (validator, host) pair this observer saw enter or leave the chain's
// bonded Fibre provider list. The prober's fallback for a validator that
// left the bonded set (jailed, unbonding) is the last host it registered,
// and until this file was read that fallback lived in memory only: a
// restart lost it, and a jailed validator's remaining obligations went
// NOT_REGISTERED at the next probe instead of being checked at the host it
// still runs. The registry is the durable copy, and it is replayed from the
// start on every boot and tailed from then on.
//
// A trailing partial line (the collector mid-write) is left until it is
// complete. A malformed complete line is skipped and counted, not fatal:
// the collector, not the prober, owns this file, and a bad line must not
// stop probing.
type hostRegistry struct {
	path   string
	offset int64
	line   int64
	// missingLogged: the file is optional (a vantage without a collector
	// never has one), so its absence is said once, not every cycle.
	missingLogged bool
}

// registryEvent is the on-disk record (observer/store.EndpointEvent). The
// prober reads only the fields it needs and does not import the store.
type registryEvent struct {
	Kind        string    `json:"kind"`
	ConsAddress string    `json:"validator_cons_address"`
	Host        string    `json:"host"`
	At          time.Time `json:"at"`
}

const (
	registryOpened = "endpoint_opened"
	registryClosed = "endpoint_closed"
)

func newHostRegistry(path string) *hostRegistry {
	return &hostRegistry{path: path}
}

// refresh reads the records appended since the last call and seeds the
// resolver's last-known hosts with them. It returns how many records were
// applied and how many were skipped as malformed. A missing file is not an
// error; a file that shrank is re-read from the start.
func (h *hostRegistry) refresh(r *Resolver) (applied, skipped int, err error) {
	fh, err := os.Open(h.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("open %s: %w", h.path, err)
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return 0, 0, err
	}
	if info.Size() < h.offset {
		h.offset, h.line = 0, 0
	}
	if _, err := fh.Seek(h.offset, io.SeekStart); err != nil {
		return 0, 0, err
	}
	rd := bufio.NewReaderSize(fh, 1<<16)
	for {
		raw, err := rd.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break // partial trailing line: wait for the newline
			}
			return applied, skipped, err
		}
		h.line++
		h.offset += int64(len(raw))
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		var e registryEvent
		if err := json.Unmarshal(trimmed, &e); err != nil {
			skipped++
			continue
		}
		if e.Kind != registryOpened && e.Kind != registryClosed {
			skipped++
			continue
		}
		addr, err := consHex(e.ConsAddress)
		if err != nil || e.Host == "" || e.At.IsZero() {
			skipped++
			continue
		}
		// An opening says the host was registered at e.At; a closing says
		// it was still registered up to e.At. Either way, the newest
		// record for a validator names the host it was last seen with.
		r.seedLastKnown(addr, e.Host, e.At)
		applied++
	}
	return applied, skipped, nil
}

// consHex turns a bech32 consensus address into the lower-case 20-byte hex
// the resolver keys on.
func consHex(bech string) (string, error) {
	_, raw, err := bech32.DecodeAndConvert(bech)
	if err != nil {
		return "", err
	}
	if len(raw) != 20 {
		return "", fmt.Errorf("consensus address %d bytes, want 20", len(raw))
	}
	return strings.ToLower(hex.EncodeToString(raw)), nil
}
