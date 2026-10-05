package hooks

import (
	"testing"

	"zekumo/internal/repo"
)

func TestViewerWebhookSecretRedaction(t *testing.T) {
	items := []repo.Webhook{{Secret: "hook-secret"}}
	redactWebhookSecrets(items, "viewer")
	if items[0].Secret != "" {
		t.Fatal("viewer response retained webhook secret")
	}
	items[0].Secret = "hook-secret"
	redactWebhookSecrets(items, "editor")
	if items[0].Secret == "" {
		t.Fatal("editor response unexpectedly lost webhook secret")
	}
}
