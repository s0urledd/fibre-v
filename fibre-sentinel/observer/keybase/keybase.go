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

// ValidIdentity reports whether s is a Keybase key suffix as validators set
// it: sixteen hex characters. Anything else on chain (a name, a URL, an
// empty string) is not looked up.
func ValidIdentity(s string) bool { return identityRe.MatchString(s) }

// Client talks to one Keybase API base.
type Client struct {
	HTTP *http.Client
	Base string
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
		if err != nil || pu.Scheme != "https" {
			continue
		}
		return u.Pictures.Primary.URL, nil
	}
	return "", ErrNoPicture
}

// Fetch downloads a picture, refusing anything that is not an image or is
// larger than MaxImageBytes. The content type returned is the server's,
// trimmed of parameters.
func (c *Client) Fetch(ctx context.Context, pictureURL string) (contentType string, data []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pictureURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "fibrescope-observer (+https://github.com/plsgiveup/fibre)")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("picture: HTTP %d", resp.StatusCode)
	}
	ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(ct, "image/") {
		return "", nil, fmt.Errorf("picture: content type %q is not an image", ct)
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
