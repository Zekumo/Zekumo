package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise only hosted pages: these must work without database dependencies.
func TestHostedPages(t *testing.T) {
	rt := router{ServeMux: http.NewServeMux(), d: &deps{}}
	registerPages(rt)
	for _, tc := range []struct {
		path        string
		status      int
		contentType string
		contains    string
	}{
		{"/", 200, "text/html", "让每一个"},
		{"/site/style.css", 200, "text/css", ".hero"},
		{"/site/app.js", 200, "javascript", "activateTab"},
		{"/site/assets/cloud-adventure.png", 200, "image/png", ""},
		{"/site/assets/brand.png", 200, "image/png", ""},
		{"/admin/", 200, "text/html", "<html"},
		{"/sso/authorize", 200, "text/html", "<html"},
		{"/oauth/authorize", 200, "text/html", "<html"},
		{"/not-a-page", 404, "text/plain", ""},
		{"/site/missing.png", 404, "text/plain", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res := httptest.NewRecorder()
			rt.ServeHTTP(res, httptest.NewRequest("GET", tc.path, nil))
			if res.Code != tc.status {
				t.Fatalf("status = %d, want %d", res.Code, tc.status)
			}
			if !strings.Contains(res.Header().Get("Content-Type"), tc.contentType) {
				t.Errorf("content type = %q, want %q", res.Header().Get("Content-Type"), tc.contentType)
			}
			if !strings.Contains(res.Body.String(), tc.contains) {
				t.Errorf("body missing %q", tc.contains)
			}
		})
	}
}
