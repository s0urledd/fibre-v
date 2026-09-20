package main

import (
	"log"
	"strconv"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// bumpHoldsRevision tells the API that a hold was raised or lifted, or a
// verdict moved. Its cached aggregates run to a thirty-minute TTL, and a
// withheld fault republished for half an hour after the hold landed is the
// accusation the hold exists to stop.
func bumpHoldsRevision(st *store.Store, now time.Time) {
	if err := st.SetMeta(store.MetaParamHoldsRev, strconv.FormatInt(now.UTC().UnixNano(), 10), now); err != nil {
		log.Printf("param holds revision: %v", err)
	}
}
