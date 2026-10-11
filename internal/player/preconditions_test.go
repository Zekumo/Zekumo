package player

import (
	"net/http/httptest"
	"testing"
)

func TestSaveConditionsFailClosed(t *testing.T) {
	revision := `"12345678-1234-4234-8234-123456789abc"`
	cases := []struct {
		name, match, absent string
		allow, valid        bool
	}{
		{"legacy", "", "", true, true},
		{"revision", revision, "", true, true},
		{"exists", "*", "", true, true},
		{"create", "", "*", true, true},
		{"both", revision, "*", true, false},
		{"weak", "W/" + revision, "", true, false},
		{"list", revision + "," + revision, "", true, false},
		{"unquoted", revision[1 : len(revision)-1], "", true, false},
		{"malformed", `"bad"`, "", true, false},
		{"absent-specific", "", revision, true, false},
		{"delete-create", "", "*", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "/", nil)
			if tc.match != "" {
				r.Header.Set("If-Match", tc.match)
			}
			if tc.absent != "" {
				r.Header.Set("If-None-Match", tc.absent)
			}
			w := httptest.NewRecorder()
			_, ok := saveCondition(w, r, tc.allow)
			if ok != tc.valid {
				t.Fatalf("valid=%v status=%d body=%s", ok, w.Code, w.Body.String())
			}
			if !ok && w.Code != 400 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	for _, headers := range [][]string{{}, {""}, {"*", "*"}} {
		r := httptest.NewRequest("PUT", "/", nil)
		r.Header["If-Match"] = headers
		if _, ok := saveCondition(httptest.NewRecorder(), r, true); ok {
			t.Fatalf("ambiguous headers accepted: %#v", headers)
		}
	}
}
