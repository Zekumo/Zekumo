package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// gameIDHolder lets an outer middleware (access logging) observe the game a
// request belonged to, even though auth resolves it in an inner layer:
// the outer layer plants a holder, the inner layer fills it.
type gameIDHolder struct{ v string }

type holderKey struct{}

type workspaceIDHolder struct{ v string }
type workspaceHolderKey struct{}

func ContextWithGameIDSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, holderKey{}, &gameIDHolder{})
}

func SetCtxGameID(ctx context.Context, gameID string) {
	if h, ok := ctx.Value(holderKey{}).(*gameIDHolder); ok {
		h.v = gameID
	}
}

func CtxGameID(ctx context.Context) string {
	if h, ok := ctx.Value(holderKey{}).(*gameIDHolder); ok {
		return h.v
	}
	return ""
}

// ContextWithWorkspaceIDSlot mirrors the game-id slot for tenant-aware admin
// middleware and access logging.
func ContextWithWorkspaceIDSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, workspaceHolderKey{}, &workspaceIDHolder{})
}

func SetCtxWorkspaceID(ctx context.Context, workspaceID string) {
	if h, ok := ctx.Value(workspaceHolderKey{}).(*workspaceIDHolder); ok {
		h.v = workspaceID
	}
}

func CtxWorkspaceID(ctx context.Context) string {
	if h, ok := ctx.Value(workspaceHolderKey{}).(*workspaceIDHolder); ok {
		return h.v
	}
	return ""
}

const maxBodySize = 1 << 20 // 1MB

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			Error(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1MB")
		} else {
			Error(w, http.StatusBadRequest, "bad_json", "invalid JSON body: "+err.Error())
		}
		return err
	}
	return nil
}
