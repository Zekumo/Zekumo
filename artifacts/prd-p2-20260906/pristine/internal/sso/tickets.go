package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const ticketTTL = 5 * time.Minute

var ErrBadTicket = errors.New("ticket is invalid, expired or already used")

// Tickets issues one-time, game-scoped login tickets backed by Redis.
// A ticket proves "this platform account wants to enter this game" and is
// consumed on first redemption.
type Tickets struct{ RDB *redis.Client }

type ticketPayload struct {
	AccountID string `json:"account_id"`
	GameID    string `json:"game_id"`
}

func (t Tickets) Issue(ctx context.Context, accountID, gameID string) (string, error) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	ticket := "st_" + hex.EncodeToString(b)
	payload, _ := json.Marshal(ticketPayload{AccountID: accountID, GameID: gameID})
	if err := t.RDB.Set(ctx, "sso:ticket:"+ticket, payload, ticketTTL).Err(); err != nil {
		return "", err
	}
	return ticket, nil
}

// Redeem consumes the ticket and returns who it was issued to, for which game.
func (t Tickets) Redeem(ctx context.Context, ticket string) (accountID, gameID string, err error) {
	raw, err := t.RDB.GetDel(ctx, "sso:ticket:"+ticket).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", ErrBadTicket
	}
	if err != nil {
		return "", "", err
	}
	var p ticketPayload
	if json.Unmarshal([]byte(raw), &p) != nil {
		return "", "", ErrBadTicket
	}
	return p.AccountID, p.GameID, nil
}
