package chat

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
	"zekumo/internal/safego"
)

const maxMessageLen = 500

// Service filters and persists chat messages; delivery happens in the realtime hub.
type Service struct {
	Repo   repo.Chat
	filter *wordFilter
	saves  chan repo.ChatMessage
}

func NewService(chatRepo repo.Chat, bannedWordsFile string) *Service {
	s := &Service{
		Repo:   chatRepo,
		filter: loadFilter(bannedWordsFile),
		saves:  make(chan repo.ChatMessage, 2048),
	}
	for i := range 4 {
		safego.Go(fmt.Sprintf("chat.saver[%d]", i), s.saveWorker)
	}
	return s
}

// Prepare validates and censors an outgoing message. Returns the cleaned text.
func (s *Service) Prepare(content string) (string, bool) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > maxMessageLen*4 { // *4: UTF-8 bytes vs runes
		return "", false
	}
	return s.filter.censor(content), true
}

// Save persists asynchronously; chat delivery must not wait on Postgres.
// Messages go through a bounded queue rather than a goroutine each, so a
// chat flood cannot spawn unbounded writers and drain the DB pool.
func (s *Service) Save(m repo.ChatMessage) {
	select {
	case s.saves <- m:
	default:
		log.Printf("chat: save queue full, dropping message in %s", m.Channel)
	}
}

func (s *Service) saveWorker() {
	for m := range s.saves {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := s.Repo.Save(ctx, &m); err != nil {
			log.Printf("chat: save message failed: %v", err)
		}
		cancel()
	}
}

type wordFilter struct{ words []string }

func loadFilter(path string) *wordFilter {
	f := &wordFilter{}
	if path == "" {
		return f
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("chat: cannot read banned words file %s: %v", path, err)
		return f
	}
	for _, line := range strings.Split(string(data), "\n") {
		if w := strings.TrimSpace(line); w != "" && !strings.HasPrefix(w, "#") {
			f.words = append(f.words, w)
		}
	}
	log.Printf("chat: loaded %d banned words", len(f.words))
	return f
}

func (f *wordFilter) censor(s string) string {
	for _, w := range f.words {
		if strings.Contains(s, w) {
			s = strings.ReplaceAll(s, w, strings.Repeat("*", len([]rune(w))))
		}
	}
	return s
}

type Handler struct{ Svc *Service }

// History handles GET /v1/chat/history?channel=&before_id=&limit= (newest first).
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_channel", "channel query parameter is required")
		return
	}
	beforeID, _ := strconv.ParseInt(r.URL.Query().Get("before_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	msgs, err := h.Svc.Repo.History(r.Context(), claims.GameID, channel, beforeID, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": msgs})
}
