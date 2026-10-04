package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func rebase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Path, "/storage/") {
		return raw
	}
	return base + u.Path + "?" + u.RawQuery
}

func upload(uploadURL string, payload []byte) error {
	req, _ := http.NewRequest(http.MethodPut, rebase(uploadURL), bytes.NewReader(payload))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("PUT %s -> %d: %s", uploadURL, res.StatusCode, body)
	}
	return nil
}

func download(rawURL string, wantSHA []byte) error {
	res, err := http.Get(rebase(rawURL))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("GET -> %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if !bytes.Equal(sum[:], wantSHA) {
		return fmt.Errorf("downloaded bytes hash mismatch")
	}
	return nil
}
