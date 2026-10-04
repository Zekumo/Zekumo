package httpx

import (
	"net/url"
	"strings"
)

// RedirectAllowed reports whether uri is covered by one of the
// newline-separated allowlist entries.
//
// Scheme and host must match exactly; only the path is treated as a prefix,
// and only at a segment boundary. Naive string prefixing is not safe here:
// an entry of "https://good.com" would otherwise match
// "https://good.com.evil.com/cb" and "https://good.com@evil.com/cb", both of
// which a browser sends to evil.com — enough to steal an SSO ticket or an
// OAuth authorization code.
func RedirectAllowed(allowlist, uri string) bool {
	target, err := url.Parse(uri)
	if err != nil || target.Host == "" || target.User != nil {
		return false
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return false
	}
	if strings.Contains(target.EscapedPath(), "..") { // no climbing out of an allowed path
		return false
	}
	for _, line := range strings.Split(allowlist, "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		allowed, err := url.Parse(entry)
		if err != nil || allowed.Host == "" {
			continue
		}
		if !strings.EqualFold(allowed.Scheme, target.Scheme) ||
			!strings.EqualFold(allowed.Host, target.Host) {
			continue
		}
		if pathCovers(allowed.Path, target.Path) {
			return true
		}
	}
	return false
}

// pathCovers reports whether prefix covers p at a segment boundary:
// "/cb" covers "/cb" and "/cb/done", but not "/cbx".
func pathCovers(prefix, p string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return true
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}
