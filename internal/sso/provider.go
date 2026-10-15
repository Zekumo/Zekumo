package sso

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"zekumo/internal/auth"
	"zekumo/internal/repo"
)

// Provider implements auth.Provider for platform accounts. Two credential
// shapes are accepted:
//   - ticket: issued by /sso/api/tickets or the hosted authorize page (true SSO);
//   - username+password: the account's own credentials entered in-game.
//
// Either way the player row linked to the account is returned, created on
// first login to a game.
type Provider struct {
	Accounts repo.Accounts
	Players  repo.Players
	Tickets  Tickets
	Attempts auth.AttemptLimiter // optional; shared with every other account login path
}

func (Provider) Name() string { return "sso" }

func (p Provider) Authenticate(ctx context.Context, gameID string, creds auth.Credentials) (*repo.Player, error) {
	accountID, err := p.resolveAccount(ctx, gameID, creds)
	if err != nil {
		return nil, err
	}
	account, err := p.Accounts.ByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	_ = p.Accounts.TouchLogin(ctx, account.ID)

	player, err := p.Players.ByAccount(ctx, gameID, account.ID)
	if errors.Is(err, repo.ErrNotFound) {
		player, err = p.Players.CreateForAccount(ctx, gameID, account.ID, account.Nickname)
		if repo.IsUniqueViolation(err) { // concurrent first login for this account
			return p.Players.ByAccount(ctx, gameID, account.ID)
		}
	}
	return player, err
}

// resolveAccount turns the presented credentials into a verified account id.
func (p Provider) resolveAccount(ctx context.Context, gameID string, creds auth.Credentials) (string, error) {
	if creds.Ticket != "" {
		accountID, ticketGameID, err := p.Tickets.Redeem(ctx, creds.Ticket)
		if err != nil {
			return "", err
		}
		if ticketGameID != gameID {
			return "", errors.New("ticket was issued for a different game")
		}
		return accountID, nil
	}
	identity := auth.AccountIdentity(creds.Username)
	if p.Attempts != nil && p.Attempts.Locked(ctx, identity) {
		return "", auth.ErrLocked
	}
	account, err := p.Accounts.ByUsername(ctx, strings.TrimSpace(creds.Username))
	if errors.Is(err, repo.ErrNotFound) {
		auth.BurnPasswordCheck(creds.Password)
		return "", p.fail(ctx, identity)
	}
	if err != nil {
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(creds.Password)) != nil {
		return "", p.fail(ctx, identity)
	}
	if p.Attempts != nil {
		p.Attempts.Succeed(ctx, identity)
	}
	return account.ID, nil
}

// fail records a wrong password and reports which error to return.
func (p Provider) fail(ctx context.Context, identity string) error {
	if p.Attempts != nil && p.Attempts.Fail(ctx, identity) {
		return auth.ErrLocked
	}
	return auth.ErrBadCredentials
}
