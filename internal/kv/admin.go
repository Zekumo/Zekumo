package kv

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

func adminPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 20, 0
	for _, field := range []struct {
		name  string
		value *int
	}{{"limit", &limit}, {"offset", &offset}} {
		if raw := r.URL.Query().Get(field.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || (field.name == "limit" && (n == 0 || n > 50)) || (field.name == "offset" && n > 10000) {
				httpx.Error(w, 400, "bad_pagination", "limit must be 1..50; offset 0..10000")
				return 0, 0, false
			}
			*field.value = n
		}
	}
	return limit, offset, true
}

func (h *Handler) AdminNamespaces(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := adminPagination(w, r)
	if !ok {
		return
	}
	items, err := h.Data.Namespaces(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		httpx.Error(w, 500, "internal", "could not list namespaces")
		return
	}
	httpx.JSON(w, 200, map[string]any{"namespaces": items, "limit": limit, "offset": offset, "has_more": len(items) == limit})
}
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	ns, _, ok := pathNames(w, r, false)
	if !ok {
		return
	}
	limit, offset, ok := adminPagination(w, r)
	if !ok {
		return
	}
	entries, err := h.Data.AdminKeys(r.Context(), r.PathValue("id"), ns, limit, offset)
	if err != nil {
		httpx.Error(w, 500, "internal", "could not list keys")
		return
	}
	httpx.JSON(w, 200, map[string]any{"namespace": ns, "public": isPublicNamespace(ns), "keys": entries, "limit": limit, "offset": offset, "has_more": len(entries) == limit})
}
func (h *Handler) AdminGet(w http.ResponseWriter, r *http.Request) {
	ns, key, ok := pathNames(w, r, true)
	if !ok {
		return
	}
	entry, err := h.Data.AdminEntry(r.Context(), r.PathValue("id"), ns, key)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, 404, "not_found", "key not found or expired")
		return
	}
	if err != nil {
		httpx.Error(w, 500, "internal", "could not read key")
		return
	}
	httpx.JSON(w, 200, map[string]any{"namespace": ns, "public": isPublicNamespace(ns), "entry": entry})
}
func (h *Handler) AdminPut(w http.ResponseWriter, r *http.Request) {
	ns, key, ok := pathNames(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Value   json.RawMessage `json:"value"`
		TTLSecs *int64          `json:"ttl_seconds"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if len(req.Value) == 0 || !json.Valid(req.Value) {
		httpx.Error(w, 400, "bad_json", "value must be valid JSON")
		return
	}
	if len(req.Value) > maxValueSize {
		httpx.Error(w, 413, "value_too_large", "value exceeds 64KB")
		return
	}
	if req.TTLSecs == nil || *req.TTLSecs < 0 || *req.TTLSecs > maxTTLSecs {
		httpx.Error(w, 400, "bad_ttl", "ttl_seconds must be explicitly set to 0..31536000 (0 means permanent)")
		return
	}
	var expiry *time.Time
	if *req.TTLSecs > 0 {
		t := time.Now().Add(time.Duration(*req.TTLSecs) * time.Second)
		expiry = &t
	}
	if err := h.Data.PutTTL(r.Context(), r.PathValue("id"), ns+"/"+key, req.Value, expiry); err != nil {
		httpx.Error(w, 500, "internal", "could not save key")
		return
	}
	httpx.JSON(w, 200, map[string]any{"stored": true, "expires_at": expiry})
}
func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	ns, key, ok := pathNames(w, r, true)
	if !ok {
		return
	}
	if err := h.Data.Delete(r.Context(), r.PathValue("id"), ns+"/"+key); err != nil {
		httpx.Error(w, 500, "internal", "could not delete key")
		return
	}
	httpx.JSON(w, 200, map[string]bool{"deleted": true})
}
