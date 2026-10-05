package admin

import (
	"testing"

	"zekumo/internal/repo"
)

func TestViewerSecretRedaction(t *testing.T) {
	games := []repo.Game{{AppSecret: "app-secret"}}
	clients := []repo.OAuthClient{{ClientSecret: "oauth-secret"}}
	redactGameSecrets(games, "viewer")
	redactOAuthSecrets(clients, "viewer")
	if games[0].AppSecret != "" || clients[0].ClientSecret != "" {
		t.Fatal("viewer response retained a secret")
	}
	games[0].AppSecret = "app-secret"
	clients[0].ClientSecret = "oauth-secret"
	redactGameSecrets(games, "editor")
	redactOAuthSecrets(clients, "editor")
	if games[0].AppSecret == "" || clients[0].ClientSecret == "" {
		t.Fatal("editor response unexpectedly lost a secret")
	}
}
