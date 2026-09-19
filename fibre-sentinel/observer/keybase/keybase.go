// Package keybase resolves a validator's avatar from the Keybase identity
// its operator set in the staking module. The chain carries only the key
// suffix (sixteen hex characters); Keybase's public lookup turns it into a
// picture URL, and the picture is fetched once and kept in the store, so
// readers of the site never contact Keybase or its CDN themselves.
package keybase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBase is Keybase's public API.
const DefaultBase = "https://keybase.io"

// MaxImageBytes bounds one picture. Keybase serves 360×360 processed
// uploads of a few tens of kilobytes; a megabyte is a generous ceiling.
const MaxImageBytes = 1 << 20

var identityRe = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)

// InertTypes are the picture types this observer will hold and serve back.
//
// "image/" as a prefix test is not enough: image/svg+xml is an image and also
// a document that runs script, and the avatar is served from this API's own
// origin under a policy that allows inline script. An operator's Keybase
// picture is not something this site should be able to be made to execute, so
// the test is an allowlist of inert raster formats rather than a family name.
var InertTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// InertType reports whether a content type is one of InertTypes, ignoring any
// parameters and case.
func InertType(ct string) bool {
	return InertTypes[strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))]
}

// pictureHosts are the hosts a picture may be fetched from. The lookup
// response names the URL, and the lookup response is not this observer's to
// trust: without a test, whatever it named is where the collector connects,
// through up to ten redirects and a downgrade to plain http.
var pictureHosts = map[string]bool{
	"keybase.io":       true,
	"s3.amazonaws.com": true,
}

// allowedPicture is the test applied to the lookup's URL and again to every
// redirect: https, and a host Keybase actually serves pictures from. hosts
// overrides the default set (tests point it at their own server).
func allowedPicture(u *url.URL, hosts map[string]bool) bool {
	if u == nil || u.Scheme != "https" {
		return false
	}
	if hosts == nil {
		hosts = pictureHosts
	}
	h := strings.ToLower(u.Hostname())
	if hosts[h] {
		return true
	}
	for allowed := range hosts {
		if strings.HasSuffix(h, "."+allowed) {
			return true
		}
	}
	return false
}

// ValidIdentity reports whether s is a Keybase key suffix as validators set
// it: sixteen hex characters. Anything else on chain (a name, a URL, an
// empty string) is not looked up.
func ValidIdentity(s string) bool { return identityRe.MatchString(s) }

// Client talks to one Keybase API base.
type Client struct {
	HTTP *http.Client
	Base string
	// PictureHosts overrides the hosts a picture may be fetched from. Nil is
	// the Keybase set, which is what production uses; a test points it at
	// its own server. Widening it in a deployment would let whatever the
	// lookup response names decide where the collector connects.
	PictureHosts map[string]bool
	// AllowInsecurePictures permits a plain-http picture URL. Tests only:
	// the scheme check is part of what stops a lookup response redirecting
	// the fetch anywhere it likes.
	AllowInsecurePictures bool
}

// New is a client with a bounded timeout against DefaultBase.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, Base: DefaultBase}
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return DefaultBase
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// ErrNoPicture is returned by Lookup when Keybase knows no user with the
// suffix, or the user has no primary picture.
var ErrNoPicture = errors.New("no picture")

// Lookup returns the primary picture URL for a key suffix. ErrNoPicture
// when the suffix resolves to nobody or to a user without one; any other
// error is transport or a malformed answer.
func (c *Client) Lookup(ctx context.Context, identity string) (string, error) {
	if !ValidIdentity(identity) {
		return "", fmt.Errorf("not a key suffix: %q", identity)
	}
	q := url.Values{"key_suffix": {identity}, "fields": {"pictures"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/_/api/1.0/user/lookup.json?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "fibrescope-observer (+https://github.com/plsgiveup/fibre)")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keybase lookup: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Status struct {
			Code int    `json:"code"`
			Name string `json:"name"`
		} `json:"status"`
		Them []*struct {
			Pictures *struct {
				Primary *struct {
					URL string `json:"url"`
				} `json:"primary"`
			} `json:"pictures"`
		} `json:"them"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("keybase lookup: decode: %w", err)
	}
	if body.Status.Code != 0 {
		return "", fmt.Errorf("keybase lookup: status %d %s", body.Status.Code, body.Status.Name)
	}
	for _, u := range body.Them {
		if u == nil || u.Pictures == nil || u.Pictures.Primary == nil || u.Pictures.Primary.URL == "" {
			continue
		}
		pu, err := url.Parse(u.Pictures.Primary.URL)
		if err != nil || !c.allow(pu) {
			continue
		}
		return u.Pictures.Primary.URL, nil
	}
	return "", ErrNoPicture
}

// allow is allowedPicture against this client's configuration.
func (c *Client) allow(u *url.URL) bool {
	if c.AllowInsecurePictures && u != nil && u.Scheme == "http" {
		v := *u
		v.Scheme = "https"
		return allowedPicture(&v, c.PictureHosts)
	}
	return allowedPicture(u, c.PictureHosts)
}

// Fetch downloads a picture, refusing anything that is not an image or is
// larger than MaxImageBytes. The content type returned is the server's,
// trimmed of parameters.
func (c *Client) Fetch(ctx context.Context, pictureURL string) (contentType string, data []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pictureURL, nil)
	if err != nil {
		return "", nil, err
	}
	if !c.allow(req.URL) {
		return "", nil, fmt.Errorf("picture: %s is not a Keybase picture host", req.URL.Host)
	}
	// The same test on every hop, not only on the URL the lookup named. A
	// redirect is chosen by the far end.
	if c.HTTP == nil || c.HTTP.CheckRedirect == nil {
		cl := *c.http()
		cl.CheckRedirect = func(r *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("picture: too many redirects")
			}
			if !c.allow(r.URL) {
				return fmt.Errorf("picture: redirect to %s is not a Keybase picture host", r.URL.Host)
			}
			return nil
		}
		req2 := req.Clone(ctx)
		return c.fetchWith(&cl, req2)
	}
	return c.fetchWith(c.http(), req)
}

func (c *Client) fetchWith(cl *http.Client, req *http.Request) (contentType string, data []byte, err error) {
	req.Header.Set("User-Agent", "fibrescope-observer (+https://github.com/plsgiveup/fibre)")
	resp, err := cl.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("picture: HTTP %d", resp.StatusCode)
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !InertTypes[ct] {
		return "", nil, fmt.Errorf("picture: content type %q is not one this observer will serve back", ct)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > MaxImageBytes {
		return "", nil, fmt.Errorf("picture: larger than %d bytes", MaxImageBytes)
	}
	if len(data) == 0 {
		return "", nil, errors.New("picture: empty body")
	}
	return ct, data, nil
}
