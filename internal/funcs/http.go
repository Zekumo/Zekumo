package funcs

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zekumo/internal/netsafe"
)

const (
	maxFetchCalls   = 5
	fetchTimeout    = 5 * time.Second
	maxResponseSize = 1 << 20 // 1MB
)

// hostAllowed matches a hostname against newline-separated allowlist entries:
// exact host, or "*.suffix" for subdomains.
func hostAllowed(allowlist, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, line := range strings.Split(allowlist, "\n") {
		entry := strings.ToLower(strings.TrimSpace(line))
		if entry == "" {
			continue
		}
		if entry == host {
			return true
		}
		// A bare "*." would otherwise match every host, so require a suffix.
		if suffix, ok := strings.CutPrefix(entry, "*."); ok && suffix != "" &&
			strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// checkTarget validates one URL against the game's allowlist. Address-level
// checks are not done here: netsafe validates the resolved IP at connect
// time, which is the only point where DNS rebinding cannot intervene.
func checkTarget(allowlist string, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only http/https URLs are allowed")
	}
	if u.User != nil {
		return errors.New("credentials in the URL are not allowed")
	}
	if !hostAllowed(allowlist, u.Hostname()) {
		return fmt.Errorf("host %q is not in this game's outbound allowlist", u.Hostname())
	}
	return nil
}

type fetchOptions struct {
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type fetchResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	JSON   any    `json:"json"`
}

// doFetch performs one outbound request under the game's allowlist. budget
// caps how long it may take, so a slow endpoint cannot stretch the
// invocation past its deadline.
func (rt *Runtime) doFetch(allowlist, rawURL string, opts fetchOptions, budget time.Duration) (*fetchResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	if err := checkTarget(allowlist, u); err != nil {
		return nil, err
	}
	if budget <= 0 {
		return nil, errors.New("time budget for outbound requests exhausted")
	}
	method := strings.ToUpper(opts.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if opts.Body != "" {
		body = strings.NewReader(opts.Body)
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "Zekumo-Function/1.0")

	client := netsafe.Client(min(fetchTimeout, budget), rt.AllowPrivateHTTP,
		func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return checkTarget(allowlist, next.URL) // each hop re-validated
		})
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	return &fetchResult{Status: res.StatusCode, Body: string(data)}, nil
}
