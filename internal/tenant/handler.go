package tenant

import (
	"errors"
	"net/http"
	"strings"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

type Handler struct {
	Store    Store
	Accounts repo.Accounts
}

func (h *Handler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	items, err := h.Store.Workspaces(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"workspaces": items})
}

func (h *Handler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	ok, err := h.Store.HasAnyMembership(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !ok {
		httpx.Error(w, http.StatusForbidden, "workspace_forbidden", "an existing membership is required")
		return
	}
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "name is required")
		return
	}
	workspace, err := h.Store.CreateWorkspace(r.Context(), claims.Subject, req.Name, req.Slug)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "workspace_create_failed", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"workspace": workspace})
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	members, err := h.Store.Members(r.Context(), r.PathValue("workspace_id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	scope := FromContext(r.Context())
	var req struct {
		AccountUsername string `json:"account_username"`
		Role            string `json:"role"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	account, err := h.Accounts.ByUsername(r.Context(), strings.TrimSpace(req.AccountUsername))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "account_not_found", "no Zekumo account has that username")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if req.Role == "owner" && scope.Role != "owner" {
		httpx.Error(w, http.StatusForbidden, "owner_required", "only an owner may add an owner")
		return
	}
	member, err := h.Store.AddAccountMember(r.Context(), scope.WorkspaceID, account.ID, account.Username, req.Role)
	if errors.Is(err, ErrInvalidRole) {
		httpx.Error(w, http.StatusBadRequest, "invalid_role", "role must be owner, admin, editor, or viewer")
		return
	}
	if errors.Is(err, ErrAlreadyMember) {
		httpx.Error(w, http.StatusConflict, "already_member", "account is already a workspace member")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"member": member})
}

func (h *Handler) UpdateMember(w http.ResponseWriter, r *http.Request) {
	scope := FromContext(r.Context())
	var req struct {
		Role string `json:"role"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	err := h.Store.SetMemberRole(r.Context(), scope.WorkspaceID, r.PathValue("user_id"), req.Role, scope.Role)
	h.memberWriteResult(w, err, map[string]bool{"updated": true})
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	scope := FromContext(r.Context())
	err := h.Store.RemoveMember(r.Context(), scope.WorkspaceID, r.PathValue("user_id"), scope.Role)
	h.memberWriteResult(w, err, map[string]bool{"removed": true})
}

func (h *Handler) memberWriteResult(w http.ResponseWriter, err error, result any) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "member not found")
	case errors.Is(err, ErrInvalidRole):
		httpx.Error(w, http.StatusBadRequest, "invalid_role", "role must be owner, admin, editor, or viewer")
	case errors.Is(err, ErrOwnerRequired):
		httpx.Error(w, http.StatusForbidden, "owner_required", err.Error())
	case errors.Is(err, ErrLastOwner):
		httpx.Error(w, http.StatusConflict, "last_owner", err.Error())
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
	default:
		httpx.JSON(w, http.StatusOK, result)
	}
}
