package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	RolePlayer  = "player"
	RoleAdmin   = "admin"
	RoleAccount = "account" // platform-wide SSO account (通行证)
	RoleOAuth   = "oauth"   // third-party access token for an account
)

type Claims struct {
	jwt.RegisteredClaims
	GameID   string `json:"gid,omitempty"`
	Role     string `json:"role"`
	Nickname string `json:"nick,omitempty"`
	Client   string `json:"cli,omitempty"`   // OAuth client_id
	Scope    string `json:"scope,omitempty"` // OAuth scope
}

type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenIssuer(secret string, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), ttl: ttl}
}

func (t *TokenIssuer) issue(claims Claims) (string, error) {
	return t.issueWithTTL(claims, t.ttl)
}

func (t *TokenIssuer) issueWithTTL(claims Claims, ttl time.Duration) (string, error) {
	now := time.Now()
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// IssueOAuth mints a short-lived third-party access token for an account.
func (t *TokenIssuer) IssueOAuth(accountID, clientID, scope string, ttl time.Duration) (string, error) {
	return t.issueWithTTL(Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: accountID},
		Role:             RoleOAuth,
		Client:           clientID,
		Scope:            scope,
	}, ttl)
}

func (t *TokenIssuer) IssuePlayer(playerID, gameID, nickname string) (string, error) {
	return t.issue(Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: playerID},
		GameID:           gameID,
		Role:             RolePlayer,
		Nickname:         nickname,
	})
}

func (t *TokenIssuer) IssueAccount(accountID, nickname string) (string, error) {
	return t.issue(Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: accountID},
		Role:             RoleAccount,
		Nickname:         nickname,
	})
}

func (t *TokenIssuer) IssueAdmin(identityID, username string) (string, error) {
	return t.issue(Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: identityID},
		Role:             RoleAdmin,
		Nickname:         username,
	})
}

func (t *TokenIssuer) Parse(token string) (*Claims, error) {
	var claims Claims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return t.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return &claims, nil
}
