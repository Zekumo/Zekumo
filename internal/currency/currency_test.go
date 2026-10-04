package currency

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGrantByNameRejectsInvalidTransactionsBeforeRepositoryAccess(t *testing.T) {
	svc := &Service{}
	tests := []struct {
		name, currency, player, key string
		amount                      int64
	}{
		{name: "missing currency", player: "p", key: "k", amount: 1},
		{name: "missing player", currency: "gold", key: "k", amount: 1},
		{name: "non-positive", currency: "gold", player: "p", key: "k", amount: 0},
		{name: "missing key", currency: "gold", player: "p", amount: 1},
		{name: "long key", currency: "gold", player: "p", key: strings.Repeat("x", maxKeyLen+1), amount: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := svc.GrantByName(context.Background(), "game", tt.currency, tt.player, tt.amount, tt.key, "")
			if !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("got %v, want ErrInvalidTransaction", err)
			}
		})
	}
}
