package mailbox

import (
	"reflect"
	"testing"
	"time"
)

func TestNormalizeRecipients(t *testing.T) {
	a := "00000000-0000-0000-0000-000000000001"
	b := "00000000-0000-0000-0000-000000000002"
	got, err := normalizeRecipients([]string{" " + a + " ", b, a})
	if err != nil {
		t.Fatalf("normalizeRecipients: %v", err)
	}
	want := []string{a, b}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := normalizeRecipients([]string{a, "  "}); err == nil {
		t.Fatal("expected empty recipient to be rejected")
	}
	if _, err := normalizeRecipients([]string{"not-a-uuid"}); err == nil {
		t.Fatal("expected malformed recipient to be rejected")
	}
}

func TestExpiryDuration(t *testing.T) {
	if got, err := expiryDuration(0); err != nil || got != defaultExpiresIn {
		t.Fatalf("default expiry = %v, %v", got, err)
	}
	if got, err := expiryDuration(60); err != nil || got != time.Minute {
		t.Fatalf("minute expiry = %v, %v", got, err)
	}
	if _, err := expiryDuration(-1); err == nil {
		t.Fatal("expected negative expiry to be rejected")
	}
	if _, err := expiryDuration(int64(maxExpiresIn/time.Second) + 1); err == nil {
		t.Fatal("expected overlong expiry to be rejected")
	}
}
