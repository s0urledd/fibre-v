package keybase

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidIdentity(t *testing.T) {
	for s, want := range map[string]bool{"D27EE330254D4F6A": true, "d27ee330254d4f6a": true, "": false, "huginn": false, "D27EE330254D4F6": false, "D27EE330254D4F6A0": false, "https://x": false} {
		if got := ValidIdentity(s); got != want {
			t.Errorf("ValidIdentity(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLookupAndFetch(t *testing.T) {
	var picURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/_/api/1.0/user/lookup.json"):
			switch r.URL.Query().Get("key_suffix") {
			case "D27EE330254D4F6A":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":{"code":0,"name":"OK"},"them":[{"id":"x","pictures":{"primary":{"url":"` + picURL + `"}}}]}`))
			case "0000000000000000":
				_, _ = w.Write([]byte(`{"status":{"code":0,"name":"OK"},"them":[null]}`))
			case "1111111111111111":
				_, _ = w.Write([]byte(`{"status":{"code":0,"name":"OK"},"them":[{"id":"y"}]}`))
			default:
				_, _ = w.Write([]byte(`{"status":{"code":205,"name":"NOT_FOUND"},"them":[]}`))
			}
		case r.URL.Path == "/pic.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("\xff\xd8\xff not really a jpeg"))
		case r.URL.Path == "/big.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(make([]byte, MaxImageBytes+1))
		case r.URL.Path == "/page.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	// the picture URL the lookup hands back must be https to be accepted;
	// the test server is http, so hand the fetch the plain URL directly
	picURL = strings.Replace(srv.URL, "http://", "https://", 1) + "/pic.jpg"
	c := &Client{HTTP: srv.Client(), Base: srv.URL}
	ctx := context.Background()

	got, err := c.Lookup(ctx, "D27EE330254D4F6A")
	if err != nil || got != picURL {
		t.Fatalf("lookup = %q, %v", got, err)
	}
	if _, err := c.Lookup(ctx, "0000000000000000"); !errors.Is(err, ErrNoPicture) {
		t.Fatalf("nobody: %v, want ErrNoPicture", err)
	}
	if _, err := c.Lookup(ctx, "1111111111111111"); !errors.Is(err, ErrNoPicture) {
		t.Fatalf("no picture: %v, want ErrNoPicture", err)
	}
	if _, err := c.Lookup(ctx, "2222222222222222"); err == nil || errors.Is(err, ErrNoPicture) {
		t.Fatalf("API error must not read as no picture: %v", err)
	}
	if _, err := c.Lookup(ctx, "huginn"); err == nil {
		t.Fatal("a name is not a key suffix")
	}

	ct, data, err := c.Fetch(ctx, srv.URL+"/pic.jpg")
	if err != nil || ct != "image/jpeg" || len(data) == 0 {
		t.Fatalf("fetch: %q %d %v", ct, len(data), err)
	}
	if _, _, err := c.Fetch(ctx, srv.URL+"/big.jpg"); err == nil {
		t.Fatal("an oversized picture was accepted")
	}
	if _, _, err := c.Fetch(ctx, srv.URL+"/page.html"); err == nil {
		t.Fatal("a non-image was accepted")
	}
	if _, _, err := c.Fetch(ctx, srv.URL+"/missing.jpg"); err == nil {
		t.Fatal("a 404 was accepted")
	}
}
