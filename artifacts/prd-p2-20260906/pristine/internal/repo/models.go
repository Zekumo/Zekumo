package repo

import (
	"encoding/json"
	"time"
)

type Game struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	AppSecret string    `json:"app_secret,omitempty"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// SSORedirectURLs is a newline-separated list of allowed redirect URL
	// prefixes for the hosted SSO authorize page.
	SSORedirectURLs string `json:"sso_redirect_urls"`

	// FuncHTTPAllowlist is a newline-separated list of hosts (exact, or
	// *.suffix) that this game's cloud functions may fetch.
	FuncHTTPAllowlist string `json:"func_http_allowlist"`
}

type Player struct {
	ID          string          `json:"id"`
	GameID      string          `json:"game_id"`
	Provider    string          `json:"provider"`
	Identifier  string          `json:"identifier"`
	Nickname    string          `json:"nickname"`
	Profile     json.RawMessage `json:"profile"`
	Banned      bool            `json:"banned"`
	AccountID   *string         `json:"account_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	LastLoginAt time.Time       `json:"last_login_at"`

	PasswordHash string `json:"-"`
}

// Account is a platform-wide identity (MiniCloud 通行证).
type Account struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Nickname    string    `json:"nickname"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`

	PasswordHash string `json:"-"`
}

// LinkedPlayer is a per-game player attached to an account.
type LinkedPlayer struct {
	GameID   string `json:"game_id"`
	GameName string `json:"game_name"`
	PlayerID string `json:"player_id"`
	Nickname string `json:"nickname"`
}

type DataEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type DialogueScript struct {
	ID        string          `json:"id"`
	GameID    string          `json:"game_id"`
	ScriptKey string          `json:"script_key"`
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content,omitempty"`
	Version   int             `json:"version"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type Release struct {
	ID                  int64      `json:"id"`
	GameID              string     `json:"game_id"`
	Channel             string     `json:"channel"`
	Version             string     `json:"version"`
	Changelog           string     `json:"changelog"`
	Status              string     `json:"status"`
	Mandatory           bool       `json:"mandatory"`
	MinSupportedVersion string     `json:"min_supported_version,omitempty"`
	RolloutPercent      int        `json:"rollout_percent"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type Artifact struct {
	ID         int64     `json:"id"`
	ReleaseID  int64     `json:"release_id"`
	Platform   string    `json:"platform"`
	Arch       string    `json:"arch"`
	Filename   string    `json:"filename"`
	Size       int64     `json:"size"`
	StorageKey string    `json:"-"`
	SHA256     string    `json:"sha256"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type ChatMessage struct {
	ID         int64     `json:"id"`
	GameID     string    `json:"game_id"`
	Channel    string    `json:"channel"`
	SenderID   string    `json:"sender_id"`
	SenderName string    `json:"sender_name"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// FriendRequest is a pending or resolved friend connection request.
type FriendRequest struct {
	ID        string    `json:"id"`
	GameID    string    `json:"game_id"`
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Status    string    `json:"status"` // pending / accepted / declined
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Denormalised for responses.
	FromNickname string `json:"from_nickname,omitempty"`
	ToNickname   string `json:"to_nickname,omitempty"`
}

// Friend is a resolved friend entry returned in the friend list.
type Friend struct {
	PlayerID  string    `json:"player_id"`
	Nickname  string    `json:"nickname"`
	Online    bool      `json:"online"`
	FriendSince time.Time `json:"friend_since"`
}

// AchievementDef is the game-level definition of an achievement.
type AchievementDef struct {
	ID          string    `json:"id"`
	GameID      string    `json:"game_id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IconURL     string    `json:"icon_url"`
	Rarity      string    `json:"rarity"`   // common / rare / epic / legendary
	Hidden      bool      `json:"hidden"`
	Type        string    `json:"type"`     // instant / progress
	Target      int       `json:"target"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}

// AchievementUnlock records a player's unlock state for one achievement.
type AchievementUnlock struct {
	AchievementID string     `json:"achievement_id"`
	PlayerID      string     `json:"player_id"`
	Progress      int        `json:"progress"`
	UnlockedAt    *time.Time `json:"unlocked_at,omitempty"`
}

type Currency struct {
	ID          string    `json:"id"`
	GameID      string    `json:"game_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	IconURL     string    `json:"icon_url"`
	CreatedAt   time.Time `json:"created_at"`
}

type CurrencyBalance struct {
	CurrencyID  string `json:"currency_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	IconURL     string `json:"icon_url"`
	Balance     int64  `json:"balance"`
}

type LedgerEntry struct {
	ID             int64     `json:"id"`
	CurrencyID     string    `json:"currency_id"`
	PlayerID       string    `json:"player_id"`
	Amount         int64     `json:"amount"`
	BalanceAfter   int64     `json:"balance_after"`
	Kind           string    `json:"kind"`
	IdempotencyKey string    `json:"idempotency_key"`
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

type MailReward struct {
	CurrencyID string `json:"currency_id"`
	Amount     int64  `json:"amount"`
}

type MailMessage struct {
	ID        string          `json:"id"`
	GameID    string          `json:"game_id"`
	Broadcast bool            `json:"broadcast"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Rewards   json.RawMessage `json:"rewards"`
	ExpiresAt time.Time       `json:"expires_at"`
	CreatedAt time.Time       `json:"created_at"`
}

type MailboxItem struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Rewards   json.RawMessage `json:"rewards"`
	ExpiresAt time.Time       `json:"expires_at"`
	CreatedAt time.Time       `json:"created_at"`
	ReadAt    *time.Time      `json:"read_at,omitempty"`
	ClaimedAt *time.Time      `json:"claimed_at,omitempty"`
}

type MailAdminItem struct {
	MailMessage
	Recipients int `json:"recipients"`
	ReadCount  int `json:"read_count"`
	Claimed    int `json:"claimed"`
}

type PlayerBan struct {
	ID        string     `json:"id"`
	GameID    string     `json:"game_id"`
	PlayerID  string     `json:"player_id"`
	Reason    string     `json:"reason"`
	Operator  string     `json:"operator"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LiftedAt  *time.Time `json:"lifted_at,omitempty"`
	LiftedBy  string     `json:"lifted_by,omitempty"`

	Nickname string `json:"nickname,omitempty"`
}

// Announcement is a server-wide message published by the game operator.
type Announcement struct {
	ID         string     `json:"id"`
	GameID     string     `json:"game_id"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Importance string     `json:"importance"` // info / warning / critical
	Platform   string     `json:"platform"`   // empty = all
	Active     bool       `json:"active"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
