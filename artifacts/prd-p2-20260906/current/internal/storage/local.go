package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Local stores objects on disk and serves them through the platform's own
// HTTP server with HMAC-signed URLs, mimicking the presigned-URL flow used
// with S3. Meant for development and small single-node deployments.
type Local struct {
	dir     string
	baseURL string // public base URL of the platform
	secret  []byte
	maxSize int64
}

func NewLocal(dir, baseURL, secret string, maxSize int64) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Local{dir: dir, baseURL: strings.TrimRight(baseURL, "/"), secret: []byte(secret), maxSize: maxSize}, nil
}

func (l *Local) PresignUpload(_ context.Context, key string) (string, error) {
	return l.signURL("PUT", key, ""), nil
}

func (l *Local) PresignDownload(_ context.Context, key, filename string) (string, error) {
	return l.signURL("GET", key, filename), nil
}

func (l *Local) Stat(_ context.Context, key string) (int64, error) {
	fi, err := os.Stat(l.path(key))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return fi.Size(), nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	err := os.Remove(l.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (l *Local) Put(_ context.Context, key string, body io.Reader, _ int64) error {
	path := l.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, body); err != nil {
		os.Remove(path)
		return err
	}
	return f.Close()
}

// Handler serves the signed upload/download endpoints; mount it at /storage.
func (l *Local) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /put", l.handlePut)
	mux.HandleFunc("GET /get", l.handleGet)
	return mux
}

func (l *Local) handlePut(w http.ResponseWriter, r *http.Request) {
	key, ok := l.verify(r, "PUT")
	if !ok {
		http.Error(w, "invalid or expired signature", http.StatusForbidden)
		return
	}
	path := l.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	f, err := os.Create(path)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(r.Body, l.maxSize+1)); err != nil {
		os.Remove(path)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	if fi, _ := f.Stat(); fi != nil && fi.Size() > l.maxSize {
		os.Remove(path)
		http.Error(w, "object too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (l *Local) handleGet(w http.ResponseWriter, r *http.Request) {
	key, ok := l.verify(r, "GET")
	if !ok {
		http.Error(w, "invalid or expired signature", http.StatusForbidden)
		return
	}
	if fn := r.URL.Query().Get("fn"); fn != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fn))
	}
	http.ServeFile(w, r, l.path(key))
}

// path maps a storage key to a file path, refusing traversal outside dir.
func (l *Local) path(key string) string {
	clean := filepath.Join(l.dir, filepath.FromSlash(key))
	abs, _ := filepath.Abs(clean)
	root, _ := filepath.Abs(l.dir)
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return filepath.Join(root, "_invalid")
	}
	return clean
}

func (l *Local) signURL(method, key, filename string) string {
	exp := time.Now().Add(PresignTTL).Unix()
	q := url.Values{}
	q.Set("key", key)
	q.Set("exp", strconv.FormatInt(exp, 10))
	if filename != "" {
		q.Set("fn", filename)
	}
	q.Set("sig", l.sign(method, key, exp))
	verb := "put"
	if method == "GET" {
		verb = "get"
	}
	return fmt.Sprintf("%s/storage/%s?%s", l.baseURL, verb, q.Encode())
}

func (l *Local) verify(r *http.Request, method string) (string, bool) {
	q := r.URL.Query()
	key := q.Get("key")
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil || key == "" || time.Now().Unix() > exp {
		return "", false
	}
	want := l.sign(method, key, exp)
	if !hmac.Equal([]byte(want), []byte(q.Get("sig"))) {
		return "", false
	}
	return key, true
}

func (l *Local) sign(method, key string, exp int64) string {
	mac := hmac.New(sha256.New, l.secret)
	fmt.Fprintf(mac, "%s\n%s\n%d", method, key, exp)
	return hex.EncodeToString(mac.Sum(nil))
}
