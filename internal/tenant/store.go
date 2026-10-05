package tenant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const LegacyWorkspaceID = "00000000-0000-4000-8000-000000000002"

var (
	ErrNotFound      = errors.New("tenant resource not found")
	ErrLastOwner     = errors.New("workspace must keep at least one owner")
	ErrOwnerRequired = errors.New("only an owner may change owner memberships")
	ErrInvalidRole   = errors.New("invalid membership role")
	ErrAlreadyMember = errors.New("identity is already a workspace member")
	validSlug        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)
)

type Identity struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Username  string    `json:"username"`
	AccountID *string   `json:"account_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Workspace struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationName string    `json:"organization_name"`
	Role             string    `json:"role"`
	CreatedAt        time.Time `json:"created_at"`
}

type Member struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct{ DB *pgxpool.Pool }

func validRole(role string) bool {
	switch role {
	case "owner", "admin", "editor", "viewer":
		return true
	default:
		return false
	}
}

func RoleRank(role string) int {
	switch role {
	case "owner":
		return 4
	case "admin":
		return 3
	case "editor":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

// EnsureBootstrap turns the configured legacy operator into a DB identity and
// grants it ownership of the migration-created legacy workspace. It is safe to
// call on every successful configured-operator login.
func (s Store) EnsureBootstrap(ctx context.Context, username string) (*Identity, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var out Identity
	err = tx.QueryRow(ctx,
		`INSERT INTO console_identities (kind, username)
		 VALUES ('bootstrap', $1)
		 ON CONFLICT (kind, username) DO UPDATE SET username=EXCLUDED.username
		 RETURNING id, kind, username, account_id, created_at`, username,
	).Scan(&out.ID, &out.Kind, &out.Username, &out.AccountID, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO organization_memberships (organization_id, identity_id, role)
		 SELECT organization_id, $2, 'owner' FROM workspaces WHERE id=$1
		 ON CONFLICT (organization_id, identity_id) DO NOTHING`, LegacyWorkspaceID, out.ID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO workspace_memberships (workspace_id, identity_id, role)
		 VALUES ($1, $2, 'owner')
		 ON CONFLICT (workspace_id, identity_id) DO NOTHING`, LegacyWorkspaceID, out.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s Store) IdentityByAccount(ctx context.Context, accountID string) (*Identity, error) {
	var out Identity
	err := s.DB.QueryRow(ctx,
		`SELECT id, kind, username, account_id, created_at
		 FROM console_identities WHERE account_id=$1`, accountID,
	).Scan(&out.ID, &out.Kind, &out.Username, &out.AccountID, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &out, err
}

func (s Store) IdentityByID(ctx context.Context, id string) (*Identity, error) {
	var out Identity
	err := s.DB.QueryRow(ctx,
		`SELECT id, kind, username, account_id, created_at
		 FROM console_identities WHERE id=$1`, id,
	).Scan(&out.ID, &out.Kind, &out.Username, &out.AccountID, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &out, err
}

func (s Store) Workspaces(ctx context.Context, identityID string) ([]Workspace, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT w.id, w.name, w.slug, o.id, o.name, m.role, w.created_at
		 FROM workspace_memberships m
		 JOIN workspaces w ON w.id=m.workspace_id
		 JOIN organizations o ON o.id=w.organization_id
		 WHERE m.identity_id=$1 ORDER BY w.created_at, w.id`, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.OrganizationID,
			&w.OrganizationName, &w.Role, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s Store) HasAnyMembership(ctx context.Context, identityID string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM workspace_memberships WHERE identity_id=$1)`,
		identityID).Scan(&ok)
	return ok, err
}

func randomSuffix() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	}
	return hex.EncodeToString(b)
}

func normalizedSlug(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	dash := false
	for _, r := range slug {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug = strings.Trim(b.String(), "-")
	if len(slug) < 3 {
		slug = "workspace"
	}
	if len(slug) > 50 {
		slug = strings.Trim(slug[:50], "-")
	}
	return slug + "-" + randomSuffix()
}

// CreateWorkspace creates a new organization and its first workspace. The
// creator is atomically installed as the owner of both.
func (s Store) CreateWorkspace(ctx context.Context, identityID, name, slug string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if slug == "" {
		slug = normalizedSlug(name)
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if !validSlug.MatchString(slug) {
		return nil, fmt.Errorf("invalid slug")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var out Workspace
	orgSlug := slug + "-org"
	err = tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2)
		 RETURNING id, name`, orgSlug, name).Scan(&out.OrganizationID, &out.OrganizationName)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO workspaces (organization_id, slug, name) VALUES ($1,$2,$3)
		 RETURNING id, name, slug, created_at`, out.OrganizationID, slug, name,
	).Scan(&out.ID, &out.Name, &out.Slug, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	out.Role = "owner"
	if _, err = tx.Exec(ctx,
		`INSERT INTO organization_memberships (organization_id, identity_id, role) VALUES ($1,$2,'owner')`,
		out.OrganizationID, identityID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO workspace_memberships (workspace_id, identity_id, role) VALUES ($1,$2,'owner')`,
		out.ID, identityID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s Store) Members(ctx context.Context, workspaceID string) ([]Member, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT i.id, i.username, m.role, m.created_at
		 FROM workspace_memberships m JOIN console_identities i ON i.id=m.identity_id
		 WHERE m.workspace_id=$1 ORDER BY m.created_at, i.username`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddAccountMember binds an existing SSO account to the workspace. Passwords
// remain owned by the account login flow and are never generated by tenants.
func (s Store) AddAccountMember(ctx context.Context, workspaceID, accountID, username, role string) (*Member, error) {
	if !validRole(role) {
		return nil, ErrInvalidRole
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var identityID string
	err = tx.QueryRow(ctx,
		`INSERT INTO console_identities (kind, username, account_id)
		 VALUES ('account',$1,$2)
		 ON CONFLICT (account_id) DO UPDATE SET username=EXCLUDED.username
		 RETURNING id`, username, accountID).Scan(&identityID)
	if err != nil {
		return nil, err
	}
	var orgID string
	if err := tx.QueryRow(ctx, `SELECT organization_id FROM workspaces WHERE id=$1`, workspaceID).Scan(&orgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO organization_memberships (organization_id, identity_id, role)
		 VALUES ($1,$2,$3)
		 ON CONFLICT (organization_id, identity_id) DO UPDATE
		 SET role=CASE WHEN organization_memberships.role='owner' THEN 'owner' ELSE EXCLUDED.role END`,
		orgID, identityID, role)
	if err != nil {
		return nil, err
	}
	var out Member
	err = tx.QueryRow(ctx,
		`INSERT INTO workspace_memberships (workspace_id, identity_id, role)
		 VALUES ($1,$2,$3)
		 ON CONFLICT (workspace_id, identity_id) DO NOTHING
		 RETURNING identity_id, $4::text, role, created_at`,
		workspaceID, identityID, role, username,
	).Scan(&out.UserID, &out.Username, &out.Role, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAlreadyMember
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s Store) memberRole(ctx context.Context, tx pgx.Tx, workspaceID, identityID string) (string, error) {
	var role string
	err := tx.QueryRow(ctx,
		`SELECT role FROM workspace_memberships WHERE workspace_id=$1 AND identity_id=$2 FOR UPDATE`,
		workspaceID, identityID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func (s Store) ownerCount(ctx context.Context, tx pgx.Tx, workspaceID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM workspace_memberships WHERE workspace_id=$1 AND role='owner'`,
		workspaceID).Scan(&n)
	return n, err
}

func (s Store) SetMemberRole(ctx context.Context, workspaceID, identityID, newRole, actorRole string) error {
	if !validRole(newRole) {
		return ErrInvalidRole
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	oldRole, err := s.memberRole(ctx, tx, workspaceID, identityID)
	if err != nil {
		return err
	}
	if (oldRole == "owner" || newRole == "owner") && actorRole != "owner" {
		return ErrOwnerRequired
	}
	if oldRole == "owner" && newRole != "owner" {
		if n, err := s.ownerCount(ctx, tx, workspaceID); err != nil {
			return err
		} else if n <= 1 {
			return ErrLastOwner
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE workspace_memberships SET role=$3 WHERE workspace_id=$1 AND identity_id=$2`,
		workspaceID, identityID, newRole); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE organization_memberships om SET role=$3
		 FROM workspaces w WHERE w.id=$1 AND om.organization_id=w.organization_id AND om.identity_id=$2`,
		workspaceID, identityID, newRole); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) RemoveMember(ctx context.Context, workspaceID, identityID, actorRole string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	oldRole, err := s.memberRole(ctx, tx, workspaceID, identityID)
	if err != nil {
		return err
	}
	if oldRole == "owner" {
		if actorRole != "owner" {
			return ErrOwnerRequired
		}
		if n, err := s.ownerCount(ctx, tx, workspaceID); err != nil {
			return err
		} else if n <= 1 {
			return ErrLastOwner
		}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM workspace_memberships WHERE workspace_id=$1 AND identity_id=$2`,
		workspaceID, identityID); err != nil {
		return err
	}
	// Current workspace creation makes one workspace per organization. Keep
	// this safe for a future multi-workspace organization by deleting the org
	// membership only when no sibling workspace membership remains.
	if _, err := tx.Exec(ctx,
		`DELETE FROM organization_memberships om
		 USING workspaces w
		 WHERE w.id=$1 AND om.organization_id=w.organization_id AND om.identity_id=$2
		   AND NOT EXISTS (
		     SELECT 1 FROM workspace_memberships wm JOIN workspaces sibling ON sibling.id=wm.workspace_id
		     WHERE wm.identity_id=$2 AND sibling.organization_id=w.organization_id)`,
		workspaceID, identityID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
