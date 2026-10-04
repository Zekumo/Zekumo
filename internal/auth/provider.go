package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"zekumo/internal/repo"
)

var (
	ErrBadCredentials = errors.New("bad credentials")
	ErrUserExists     = errors.New("user already exists")
)

// Credentials carries every field any provider may need; each provider reads its own.
type Credentials struct {
	Provider string `json:"provider"`
	DeviceID string `json:"device_id,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Ticket   string `json:"ticket,omitempty"` // one-time SSO ticket
}

// Provider authenticates a player within a game. New login channels (WeChat,
// Apple, ...) implement this interface and register themselves — nothing else changes.
type Provider interface {
	Name() string
	Authenticate(ctx context.Context, gameID string, creds Credentials) (*repo.Player, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range providers {
		r.providers[p.Name()] = p
	}
	return r
}

func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown auth provider %q", name)
	}
	return p, nil
}

// GuestProvider logs a player in by device id, creating the account on first sight.
type GuestProvider struct{ Players repo.Players }

func (GuestProvider) Name() string { return "guest" }

func (p GuestProvider) Authenticate(ctx context.Context, gameID string, creds Credentials) (*repo.Player, error) {
	deviceID := strings.TrimSpace(creds.DeviceID)
	if deviceID == "" {
		return nil, errors.New("device_id is required")
	}
	player, err := p.Players.Find(ctx, gameID, "guest", deviceID)
	if errors.Is(err, repo.ErrNotFound) {
		nickname := creds.Nickname
		if nickname == "" {
			nickname = "游客" + deviceID[:min(6, len(deviceID))]
		}
		player, err = p.Players.Create(ctx, gameID, "guest", deviceID, "", nickname)
		if repo.IsUniqueViolation(err) { // concurrent first login: the other request won
			return p.Players.Find(ctx, gameID, "guest", deviceID)
		}
	}
	return player, err
}

// dummyHash keeps password checks constant-time when the user does not exist,
// so response timing cannot reveal whether a username is registered.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("zekumo-timing-pad"), bcrypt.DefaultCost)

// BurnPasswordCheck spends one bcrypt comparison on a nonexistent user's
// password attempt, equalizing timing with the user-exists path.
func BurnPasswordCheck(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

// PasswordProvider verifies username/password accounts created via Register.
type PasswordProvider struct{ Players repo.Players }

func (PasswordProvider) Name() string { return "password" }

func (p PasswordProvider) Authenticate(ctx context.Context, gameID string, creds Credentials) (*repo.Player, error) {
	player, err := p.Players.Find(ctx, gameID, "password", strings.TrimSpace(creds.Username))
	if errors.Is(err, repo.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(creds.Password))
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(player.PasswordHash), []byte(creds.Password)) != nil {
		return nil, ErrBadCredentials
	}
	return player, nil
}

func (p PasswordProvider) Register(ctx context.Context, gameID string, creds Credentials) (*repo.Player, error) {
	username := strings.TrimSpace(creds.Username)
	if len(username) < 3 {
		return nil, errors.New("username must be at least 3 characters")
	}
	if len(creds.Password) < 6 {
		return nil, errors.New("password must be at least 6 characters")
	}
	if _, err := p.Players.Find(ctx, gameID, "password", username); err == nil {
		return nil, ErrUserExists
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(creds.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	nickname := creds.Nickname
	if nickname == "" {
		nickname = username
	}
	player, err := p.Players.Create(ctx, gameID, "password", username, string(hash), nickname)
	if repo.IsUniqueViolation(err) { // concurrent register with the same name
		return nil, ErrUserExists
	}
	return player, err
}
