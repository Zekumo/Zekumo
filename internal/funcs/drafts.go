package funcs

import (
	"errors"
	"github.com/dop251/goja"
	"net/http"
	"time"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

func (h *Handler) Draft(w http.ResponseWriter, r *http.Request) {
	draft, err := h.Functions.Draft(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, 500, "internal", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"draft": draft})
}

func (h *Handler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		repo.FunctionDraft
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 0 {
		httpx.Error(w, 400, "bad_revision", "expected_revision is required")
		return
	}
	if len(req.Code) > 256<<10 {
		httpx.Error(w, 413, "code_too_large", "code exceeds 256KB")
		return
	}
	if req.CronSecs < 0 || (req.CronSecs > 0 && req.CronSecs < 10) {
		httpx.Error(w, 400, "bad_cron", "cron_secs must be 0 or >= 10")
		return
	}
	// Incomplete code can be saved as a draft; syntax is checked on publication.
	d, err := h.Functions.SaveDraft(r.Context(), r.PathValue("id"), r.PathValue("name"), req.FunctionDraft, *req.ExpectedRevision)
	if errors.Is(err, repo.ErrFunctionConflict) {
		httpx.Error(w, 409, "revision_conflict", err.Error())
		return
	}
	if err != nil {
		httpx.Error(w, 500, "internal", err.Error())
		return
	}
	httpx.JSON(w, 200, d)
}

func (h *Handler) PublishDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExpectedRevision int64 `json:"expected_revision"`
		DraftRevision    int64 `json:"draft_revision"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.ExpectedRevision < 1 || req.DraftRevision < 1 {
		httpx.Error(w, 400, "bad_revision", "live and draft revisions are required")
		return
	}
	d, err := h.Functions.Draft(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, 404, "not_found", "draft not found")
		return
	}
	if err != nil {
		httpx.Error(w, 500, "internal", err.Error())
		return
	}
	if d.Revision != req.DraftRevision {
		httpx.Error(w, 409, "revision_conflict", "draft changed; reload first")
		return
	}
	if _, err = goja.Compile(r.PathValue("name"), d.Code, false); err != nil {
		httpx.Error(w, 400, "invalid_code", err.Error())
		return
	}
	f, err := h.Functions.PublishDraft(r.Context(), r.PathValue("id"), r.PathValue("name"), req.ExpectedRevision, req.DraftRevision)
	if errors.Is(err, repo.ErrFunctionConflict) {
		httpx.Error(w, 409, "revision_conflict", err.Error())
		return
	}
	if err != nil {
		httpx.Error(w, 500, "internal", err.Error())
		return
	}
	if h.Logs != nil {
		h.Logs.Write(f.GameID, "info", "funcs", "published", "cloud function draft published", map[string]any{"function": f.Name, "revision": f.Revision})
	}
	httpx.JSON(w, 200, f)
}

func (h *Handler) DeleteDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.ExpectedRevision < 1 {
		httpx.Error(w, 400, "bad_revision", "expected_revision is required")
		return
	}
	err := h.Functions.DeleteDraft(r.Context(), r.PathValue("id"), r.PathValue("name"), req.ExpectedRevision)
	if errors.Is(err, repo.ErrFunctionConflict) {
		httpx.Error(w, 409, "revision_conflict", err.Error())
		return
	}
	if err != nil {
		httpx.Error(w, 500, "internal", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]bool{"deleted": true})
}

// recordExecution deliberately excludes request bodies, source and result data.
func (h *Handler) recordExecution(gameID string, fn *repo.CloudFunction, start time.Time, err error) {
	if h.Logs == nil {
		return
	}
	level, status := "info", "ok"
	if err != nil {
		level, status = "error", "failed"
	}
	h.Logs.Write(gameID, level, "funcs", "invocation", "cloud function "+status, map[string]any{
		"function": fn.Name, "revision": fn.Revision, "duration_ms": time.Since(start).Milliseconds(), "status": status, "preview": fn.Revision == 0,
	})
}
