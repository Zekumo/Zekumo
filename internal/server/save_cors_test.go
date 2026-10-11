package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudSaveConditionalCORS(t *testing.T) {
	called := false
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Header().Set("ETag", `"revision"`) }))
	request := httptest.NewRequest(http.MethodOptions, "/v1/player/data/save", nil)
	request.Header.Set("Origin", "https://game.example")
	request.Header.Set("Access-Control-Request-Headers", "authorization,if-match,if-none-match")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || called {
		t.Fatal("preflight reached the data handler")
	}
	headers := response.Header().Get("Access-Control-Allow-Headers")
	for _, name := range []string{"If-Match", "If-None-Match"} {
		if !strings.Contains(headers, name) {
			t.Fatalf("missing %s in %s", name, headers)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(response.Header().Get("Access-Control-Expose-Headers"), "ETag") {
		t.Fatal("browser cannot read ETag")
	}
}
