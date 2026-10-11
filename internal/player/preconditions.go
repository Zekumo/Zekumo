package player

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

func saveConflict(w http.ResponseWriter) {
	httpx.Error(w, http.StatusPreconditionFailed, "save_conflict", "save changed or no longer matches the precondition; reload before retrying")
}

// The save API accepts one strong UUID ETag (or *), not ETag lists or weak
// validators. Reject malformed/ambiguous conditions rather than silently
// falling back to an unconditional write.
func saveCondition(w http.ResponseWriter, r *http.Request, allowCreate bool) (repo.SaveCondition, bool) {
	match, hasMatch := r.Header["If-Match"]
	absent, hasAbsent := r.Header["If-None-Match"]
	invalid := func() (repo.SaveCondition, bool) {
		httpx.Error(w, http.StatusBadRequest, "bad_precondition", "use one strong If-Match revision or *; PUT also supports If-None-Match: *")
		return repo.SaveCondition{}, false
	}
	if hasMatch && hasAbsent {
		return invalid()
	}
	if hasAbsent {
		if !allowCreate || len(absent) != 1 || strings.TrimSpace(absent[0]) != "*" {
			return invalid()
		}
		return repo.SaveCondition{OnlyIfAbsent: true}, true
	}
	if !hasMatch {
		return repo.SaveCondition{}, true
	}
	if len(match) != 1 {
		return invalid()
	}
	value := strings.TrimSpace(match[0])
	if value == "*" {
		return repo.SaveCondition{Revision: "*"}, true
	}
	if len(value) != 38 || value[0] != '"' || value[len(value)-1] != '"' {
		return invalid()
	}
	revision := value[1 : len(value)-1]
	parsed, err := uuid.Parse(revision)
	if err != nil || parsed.String() != revision {
		return invalid()
	}
	return repo.SaveCondition{Revision: revision}, true
}
