package updates

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/Masterminds/semver/v3"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
	"minicloud/internal/storage"
)

var (
	channelRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	sha256Re  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Handler struct {
	Games           repo.Games
	Releases        repo.Releases
	Artifacts       repo.Artifacts
	Storage         storage.Storage
	MaxArtifactSize int64
	Events          Emitter // optional webhook bus
}

// Emitter publishes platform events; nil disables emission.
type Emitter interface {
	Emit(gameID, event string, data any)
}

// --- Public API (no auth: game clients poll this) ---

// CheckUpdate handles
// GET /v1/apps/{app_id}/updates/check?version=&platform=&arch=&channel=&device_id=.
func (h *Handler) CheckUpdate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	currentVersion := q.Get("version")
	platform := q.Get("platform")
	arch := q.Get("arch")
	if arch == "" {
		arch = "any"
	}
	channel := q.Get("channel")
	if channel == "" {
		channel = "stable"
	}
	if currentVersion == "" || !ValidPlatforms[platform] {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "version and a valid platform are required")
		return
	}

	game, err := h.Games.ByAppID(r.Context(), r.PathValue("app_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_app", "no game with this app_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	releases, err := h.Releases.Published(r.Context(), game.ID, channel)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	resp := map[string]any{"update_available": false}
	release := pickRelease(releases, q.Get("device_id"))
	if release != nil && isNewer(release.Version, currentVersion) {
		artifacts, err := h.Artifacts.ByRelease(r.Context(), release.ID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		if artifact := pickArtifact(artifacts, platform, arch); artifact != nil {
			url, err := h.Storage.PresignDownload(r.Context(), artifact.StorageKey, artifact.Filename)
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
			resp = map[string]any{
				"update_available": true,
				"mandatory":        isMandatory(release, currentVersion),
				"release": map[string]any{
					"version":      release.Version,
					"changelog":    release.Changelog,
					"published_at": release.PublishedAt,
				},
				"artifact": map[string]any{
					"url":      url,
					"filename": artifact.Filename,
					"size":     artifact.Size,
					"sha256":   artifact.SHA256,
				},
			}
		}
	}

	// Stats are best-effort; never fail the check over them.
	statsCtx := context.WithoutCancel(r.Context())
	updated := resp["update_available"].(bool)
	go func() {
		ctx, cancel := context.WithTimeout(statsCtx, 5*time.Second)
		defer cancel()
		_ = h.Releases.RecordCheck(ctx, game.ID, channel, currentVersion, updated)
	}()

	httpx.JSON(w, http.StatusOK, resp)
}

// PublicReleases handles GET /v1/apps/{app_id}/releases — the published
// version history end users may see.
func (h *Handler) PublicReleases(w http.ResponseWriter, r *http.Request) {
	game, err := h.Games.ByAppID(r.Context(), r.PathValue("app_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_app", "no game with this app_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	releases, err := h.Releases.ListByGame(r.Context(), game.ID, true)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"releases": releases})
}

// --- Admin API (admin token; game addressed by console id) ---

// ListReleases handles GET /admin/api/games/{id}/releases.
func (h *Handler) ListReleases(w http.ResponseWriter, r *http.Request) {
	releases, err := h.Releases.ListByGame(r.Context(), r.PathValue("id"), false)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"releases": releases})
}

// CreateRelease handles POST /admin/api/games/{id}/releases.
func (h *Handler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version             string `json:"version"`
		Channel             string `json:"channel"`
		Changelog           string `json:"changelog"`
		Mandatory           bool   `json:"mandatory"`
		MinSupportedVersion string `json:"min_supported_version"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	version, err := semver.NewVersion(req.Version)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_version", "version must be valid semver")
		return
	}
	if req.Channel == "" {
		req.Channel = "stable"
	}
	if !channelRe.MatchString(req.Channel) {
		httpx.Error(w, http.StatusBadRequest, "bad_channel", "channel must be lowercase letters, digits or hyphens")
		return
	}
	rel := &repo.Release{
		GameID: r.PathValue("id"), Channel: req.Channel, Version: version.String(),
		Changelog: req.Changelog, Mandatory: req.Mandatory, MinSupportedVersion: req.MinSupportedVersion,
	}
	err = h.Releases.Create(r.Context(), rel)
	if errors.Is(err, repo.ErrConflict) {
		httpx.Error(w, http.StatusConflict, "version_exists", "this version already exists on this channel")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, rel)
}

// releaseFromPath resolves {id}/{version}?channel= to a release.
func (h *Handler) releaseFromPath(w http.ResponseWriter, r *http.Request) *repo.Release {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "stable"
	}
	rel, err := h.Releases.ByVersion(r.Context(), r.PathValue("id"), channel, r.PathValue("version"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no such release on this channel")
		return nil
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return nil
	}
	return rel
}

// CreateArtifact handles POST /admin/api/games/{id}/releases/{version}/artifacts:
// registers the file's metadata and answers with a presigned upload URL.
func (h *Handler) CreateArtifact(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	if rel.Status != "draft" {
		httpx.Error(w, http.StatusConflict, "not_draft", "artifacts can only be added to draft releases")
		return
	}
	var req struct {
		Platform string `json:"platform"`
		Arch     string `json:"arch"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Arch == "" {
		req.Arch = "any"
	}
	switch {
	case !ValidPlatforms[req.Platform] || !ValidArchs[req.Arch]:
		httpx.Error(w, http.StatusBadRequest, "bad_target", "invalid platform or arch")
		return
	case req.Filename == "" || req.Filename != path.Base(req.Filename):
		httpx.Error(w, http.StatusBadRequest, "bad_filename", "invalid filename")
		return
	case req.Size <= 0 || req.Size > h.MaxArtifactSize:
		httpx.Error(w, http.StatusBadRequest, "bad_size", fmt.Sprintf("size must be 1..%d bytes", h.MaxArtifactSize))
		return
	case !sha256Re.MatchString(req.SHA256):
		httpx.Error(w, http.StatusBadRequest, "bad_sha256", "sha256 must be 64 lowercase hex chars")
		return
	}
	art := &repo.Artifact{
		ReleaseID: rel.ID, Platform: req.Platform, Arch: req.Arch,
		Filename: req.Filename, Size: req.Size, SHA256: req.SHA256,
		StorageKey: fmt.Sprintf("releases/%s/%d/%s-%s/%s", rel.GameID, rel.ID, req.Platform, req.Arch, req.Filename),
	}
	if err := h.Artifacts.Create(r.Context(), art); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	uploadURL, err := h.Storage.PresignUpload(r.Context(), art.StorageKey)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"artifact": art, "upload_url": uploadURL})
}

// CompleteArtifact handles POST .../artifacts/{aid}/complete: verifies the
// object landed in storage with the declared size, then marks it ready.
func (h *Handler) CompleteArtifact(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("aid"), 10, 64)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_id", "invalid artifact id")
		return
	}
	art, err := h.Artifacts.ByID(r.Context(), id)
	if err != nil || art.ReleaseID != rel.ID {
		httpx.Error(w, http.StatusNotFound, "not_found", "no such artifact on this release")
		return
	}
	size, err := h.Storage.Stat(r.Context(), art.StorageKey)
	if err != nil {
		httpx.Error(w, http.StatusConflict, "not_uploaded", "object not uploaded yet")
		return
	}
	if size != art.Size {
		httpx.Error(w, http.StatusConflict, "size_mismatch",
			fmt.Sprintf("uploaded size %d does not match declared size %d", size, art.Size))
		return
	}
	if err := h.Artifacts.MarkReady(r.Context(), id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	art.Status = "ready"
	httpx.JSON(w, http.StatusOK, art)
}

// ListArtifacts handles GET /admin/api/games/{id}/releases/{version}/artifacts.
func (h *Handler) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	artifacts, err := h.Artifacts.ByRelease(r.Context(), rel.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
}

// Publish handles POST .../releases/{version}/publish.
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	ready, err := h.Artifacts.CountReady(r.Context(), rel.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if ready == 0 {
		httpx.Error(w, http.StatusConflict, "no_artifacts", "release has no ready artifacts")
		return
	}
	if err := h.Releases.SetStatus(r.Context(), rel.ID, []string{"draft"}, "published"); err != nil {
		httpx.Error(w, http.StatusConflict, "not_draft", "only draft releases can be published")
		return
	}
	rel.Status = "published"
	if h.Events != nil {
		h.Events.Emit(rel.GameID, "release.published", map[string]any{
			"version": rel.Version, "channel": rel.Channel, "mandatory": rel.Mandatory,
		})
	}
	httpx.JSON(w, http.StatusOK, rel)
}

// Rollout handles POST .../releases/{version}/rollout with {percent}.
func (h *Handler) Rollout(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	var req struct {
		Percent int `json:"percent"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Percent < 0 || req.Percent > 100 {
		httpx.Error(w, http.StatusBadRequest, "bad_percent", "percent must be 0..100")
		return
	}
	if err := h.Releases.SetRollout(r.Context(), rel.ID, req.Percent); err != nil {
		httpx.Error(w, http.StatusConflict, "not_published", "only published releases can be rolled out")
		return
	}
	rel.RolloutPercent = req.Percent
	httpx.JSON(w, http.StatusOK, rel)
}

// Revoke handles POST .../releases/{version}/revoke — emergency takedown;
// update checks fall back to the previous published release.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	if err := h.Releases.SetStatus(r.Context(), rel.ID, []string{"published", "deprecated"}, "revoked"); err != nil {
		httpx.Error(w, http.StatusConflict, "not_published", "only published or deprecated releases can be revoked")
		return
	}
	rel.Status = "revoked"
	httpx.JSON(w, http.StatusOK, rel)
}

// DeleteRelease handles DELETE .../releases/{version} — drafts only; storage
// objects are cleaned up best-effort.
func (h *Handler) DeleteRelease(w http.ResponseWriter, r *http.Request) {
	rel := h.releaseFromPath(w, r)
	if rel == nil {
		return
	}
	artifacts, _ := h.Artifacts.ByRelease(r.Context(), rel.ID)
	if err := h.Releases.DeleteDraft(r.Context(), rel.ID); err != nil {
		httpx.Error(w, http.StatusConflict, "not_draft", "only draft releases can be deleted")
		return
	}
	for _, a := range artifacts {
		_ = h.Storage.Delete(r.Context(), a.StorageKey)
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
