package zekumo

import (
	"context"
	"encoding/json"
	"testing"
)

const revisionFixture = "12345678-1234-4234-8234-123456789abc"

func TestDataSetUsesRawValueAndListReturnsEntries(t *testing.T) {
	c, got := serve(t, 200, `{"key":"save","revision":"`+revisionFixture+`","value":{"level":3}}`)
	_, err := c.Data.Set(context.Background(), "save", map[string]int{"level": 3})
	if err != nil {
		t.Fatal(err)
	}
	if got.body != `{"level":3}` {
		t.Fatalf("save wrapped incorrectly: %s", got.body)
	}
	_, err = c.Data.Set(context.Background(), "save", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.body != "null" {
		t.Fatalf("null save body=%q", got.body)
	}
	listed, _ := serve(t, 200, `{"entries":[{"key":"save","revision":"`+revisionFixture+`"}]}`)
	entries, err := listed.Data.List(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Revision != revisionFixture {
		t.Fatalf("list=%+v %v", entries, err)
	}
}

func TestDataConditionalRequestHeaders(t *testing.T) {
	c, got := serve(t, 200, `{"key":"save","revision":"`+revisionFixture+`","value":null}`)
	c.SetToken("player-token")
	entry, err := c.Data.GetEntry(context.Background(), "a/b")
	if err != nil || entry.Revision != revisionFixture || string(entry.Value) != "null" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	if got.rawPath != "/v1/player/data/a%2Fb" {
		t.Fatalf("path=%s", got.rawPath)
	}
	_, err = c.Data.CompareAndSet(context.Background(), "save", json.RawMessage(`{"level":5}`), revisionFixture)
	if err != nil {
		t.Fatal(err)
	}
	if got.hdr.Get("If-Match") != `"`+revisionFixture+`"` || got.body != `{"level":5}` || got.auth != "Bearer player-token" {
		t.Fatalf("conditional request=%+v", got)
	}
	_, err = c.Data.Create(context.Background(), "save", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.hdr.Get("If-None-Match") != "*" || got.body != "null" {
		t.Fatalf("create=%+v", got)
	}
	if err = c.Data.DeleteIfRevision(context.Background(), "save", revisionFixture); err != nil {
		t.Fatal(err)
	}
	if got.method != "DELETE" || got.hdr.Get("If-Match") != `"`+revisionFixture+`"` {
		t.Fatalf("delete=%+v", got)
	}
	_, err = c.Data.CompareAndSet(context.Background(), "save", true, "*")
	if err != nil || got.hdr.Get("If-Match") != "*" {
		t.Fatalf("exists condition=%+v %v", got, err)
	}
}

func TestDataConflictsAreTypedAndNeverRetried(t *testing.T) {
	c, _ := serve(t, 412, `{"error":{"code":"save_conflict","message":"reload"}}`)
	_, err := c.Data.CompareAndSet(context.Background(), "save", true, revisionFixture)
	if !IsCode(err, "save_conflict") {
		t.Fatalf("conflict=%v", err)
	}
	for _, revision := range []string{"", `"` + revisionFixture + `"`, "bad", "x\r\nheader: injected"} {
		if _, err = c.Data.CompareAndSet(context.Background(), "save", true, revision); err == nil {
			t.Fatalf("invalid revision accepted: %q", revision)
		}
	}
}
