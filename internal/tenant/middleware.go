package tenant

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
)

const WorkspaceHeader = "X-Zekumo-Workspace-ID"

type Scope struct {
	WorkspaceID    string `json:"workspace_id"`
	OrganizationID string `json:"organization_id"`
	IdentityID     string `json:"identity_id"`
	Role           string `json:"role"`
}

type scopeKey struct{}

func FromContext(ctx context.Context) *Scope {
	s, _ := ctx.Value(scopeKey{}).(*Scope)
	return s
}

type Resource int

const (
	ResourceNone Resource = iota
	ResourceWorkspace
	ResourceGame
	ResourcePlayer
	ResourceAccount
	ResourceOAuthClient
	ResourceWebhook
	ResourceAchievement
	ResourceAnnouncement
	ResourceCurrency
	ResourceExport
	ResourceArtifact
	ResourceGameCurrency
	ResourcePlayerCurrency
)

// Require resolves tenant membership on every request, so revocation and role
// changes invalidate already-issued console tokens immediately.
func (s Store) Require(minRole string, resource Resource, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFrom(r.Context())
		if claims == nil || claims.Role != auth.RoleAdmin {
			httpx.Error(w, http.StatusUnauthorized, "invalid_token", "admin session is required")
			return
		}
		if _, err := uuid.Parse(claims.Subject); err != nil {
			httpx.Error(w, http.StatusUnauthorized, "legacy_admin_token", "sign in again to select a workspace")
			return
		}
		workspaceID := r.Header.Get(WorkspaceHeader)
		if workspaceID == "" {
			httpx.Error(w, http.StatusBadRequest, "missing_workspace", WorkspaceHeader+" is required")
			return
		}
		if _, err := uuid.Parse(workspaceID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_workspace", "workspace id is invalid")
			return
		}
		var scope Scope
		scope.WorkspaceID = workspaceID
		scope.IdentityID = claims.Subject
		err := s.DB.QueryRow(r.Context(),
			`SELECT w.organization_id, m.role
			 FROM workspace_memberships m JOIN workspaces w ON w.id=m.workspace_id
			 WHERE m.workspace_id=$1 AND m.identity_id=$2`, workspaceID, claims.Subject,
		).Scan(&scope.OrganizationID, &scope.Role)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusForbidden, "workspace_forbidden", "workspace is not available to this user")
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_workspace", "workspace id is invalid")
			return
		}
		if RoleRank(scope.Role) < RoleRank(minRole) {
			httpx.Error(w, http.StatusForbidden, "insufficient_role", "membership role cannot perform this action")
			return
		}
		ok, gameID, err := s.resourceBelongs(r, resource, workspaceID)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_resource", "resource id is invalid")
			return
		}
		if !ok {
			// Deliberately avoid revealing whether an ID exists in another tenant.
			httpx.Error(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		if gameID != "" {
			httpx.SetCtxGameID(r.Context(), gameID)
		}
		httpx.SetCtxWorkspaceID(r.Context(), workspaceID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), scopeKey{}, &scope)))
	})
}

func (s Store) resourceBelongs(r *http.Request, resource Resource, workspaceID string) (bool, string, error) {
	if resource == ResourceNone {
		return true, "", nil
	}
	var query, id string
	switch resource {
	case ResourceWorkspace:
		return r.PathValue("workspace_id") == workspaceID, "", nil
	case ResourceGame:
		query, id = `SELECT id FROM games WHERE id=$1 AND workspace_id=$2`, r.PathValue("id")
	case ResourcePlayer:
		query, id = `SELECT g.id FROM players p JOIN games g ON g.id=p.game_id WHERE p.id=$1 AND g.workspace_id=$2`, r.PathValue("pid")
	case ResourceAccount:
		query, id = `SELECT g.id FROM players p JOIN games g ON g.id=p.game_id WHERE p.account_id=$1 AND g.workspace_id=$2 LIMIT 1`, r.PathValue("id")
	case ResourceOAuthClient:
		query, id = `SELECT '' FROM oauth_clients WHERE client_id=$1 AND workspace_id=$2`, r.PathValue("client_id")
	case ResourceWebhook:
		query, id = `SELECT g.id FROM webhooks x JOIN games g ON g.id=x.game_id WHERE x.id=$1 AND g.workspace_id=$2`, r.PathValue("wid")
	case ResourceAchievement:
		query, id = `SELECT g.id FROM achievement_defs x JOIN games g ON g.id=x.game_id WHERE x.id=$1 AND g.workspace_id=$2`, r.PathValue("aid")
	case ResourceAnnouncement:
		query, id = `SELECT g.id FROM announcements x JOIN games g ON g.id=x.game_id WHERE x.id=$1 AND g.workspace_id=$2`, r.PathValue("aid")
	case ResourceCurrency:
		query, id = `SELECT g.id FROM currencies x JOIN games g ON g.id=x.game_id WHERE x.id=$1 AND g.workspace_id=$2`, r.PathValue("cid")
	case ResourceExport:
		query, id = `SELECT g.id FROM export_jobs x JOIN games g ON g.id=x.game_id WHERE x.id=$1 AND g.workspace_id=$2`, r.PathValue("job_id")
	case ResourceArtifact:
		var gameID string
		err := s.DB.QueryRow(r.Context(),
			`SELECT g.id FROM artifacts a JOIN releases rel ON rel.id=a.release_id JOIN games g ON g.id=rel.game_id
			 WHERE a.id=$1 AND g.id=$2 AND g.workspace_id=$3`,
			r.PathValue("aid"), r.PathValue("id"), workspaceID).Scan(&gameID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return err == nil, gameID, err
	case ResourceGameCurrency:
		var gameID string
		err := s.DB.QueryRow(r.Context(),
			`SELECT g.id FROM games g JOIN currencies c ON c.game_id=g.id
			 WHERE g.id=$1 AND c.id=$2 AND g.workspace_id=$3`,
			r.PathValue("id"), r.PathValue("cid"), workspaceID).Scan(&gameID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return err == nil, gameID, err
	case ResourcePlayerCurrency:
		var gameID string
		err := s.DB.QueryRow(r.Context(),
			`SELECT g.id FROM players p JOIN games g ON g.id=p.game_id JOIN currencies c ON c.game_id=g.id
			 WHERE p.id=$1 AND c.id=$2 AND g.workspace_id=$3`,
			r.PathValue("pid"), r.PathValue("cid"), workspaceID).Scan(&gameID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return err == nil, gameID, err
	default:
		return false, "", nil
	}
	var gameID string
	err := s.DB.QueryRow(r.Context(), query, id, workspaceID).Scan(&gameID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	return err == nil, gameID, err
}
