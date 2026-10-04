package httpx

import "testing"

func TestRedirectAllowed(t *testing.T) {
	const list = "https://good.com/cb\nhttps://other.example.com"

	allowed := []string{
		"https://good.com/cb",
		"https://good.com/cb?ticket=x",
		"https://good.com/cb/done",
		"https://GOOD.com/cb",              // host case is insignificant
		"https://other.example.com/anything", // entry has no path: whole host
	}
	for _, uri := range allowed {
		if !RedirectAllowed(list, uri) {
			t.Errorf("RedirectAllowed(%q) = false, want true", uri)
		}
	}

	denied := []string{
		"https://good.com.evil.com/cb", // suffix attack on a prefix match
		"https://good.com@evil.com/cb", // userinfo hides the real host
		"https://evil.com/cb",
		"http://good.com/cb",     // scheme must match
		"https://good.com/cbx",   // must break at a segment boundary
		"https://good.com/other", // outside the allowed path
		"https://good.com/cb/../../etc",
		"javascript:alert(1)//good.com/cb",
		"",
	}
	for _, uri := range denied {
		if RedirectAllowed(list, uri) {
			t.Errorf("RedirectAllowed(%q) = true, want false", uri)
		}
	}

	if RedirectAllowed("", "https://good.com/cb") {
		t.Error("empty allowlist must deny everything")
	}
}
