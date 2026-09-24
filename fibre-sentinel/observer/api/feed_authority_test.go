package api

import (
	"net/http/httptest"
	"testing"
)

// With the deployment's public host configured, a client cannot pick the
// feed's tag authority (and so its cache key) with a header.
func TestFeedAuthorityPrefersTheConfiguredHost(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/feed.atom", nil)
	r.Host = "127.0.0.1:8081"
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	if got := feedAuthority(r); got != "attacker.example" {
		t.Fatalf("unconfigured: %q, want the forwarded host (the old behaviour)", got)
	}
	t.Setenv(feedAuthorityEnv, "Tensile.Huginn.Tech ")
	if got := feedAuthority(r); got != "tensile.huginn.tech" {
		t.Fatalf("configured: %q, want tensile.huginn.tech", got)
	}
}
