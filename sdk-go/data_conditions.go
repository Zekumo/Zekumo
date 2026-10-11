package zekumo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

var saveRevisionPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// GetEntry returns the raw JSON value and its opaque revision. The revision
// changes on every write, including unconditional Set and delete/recreate.
func (d *DataAPI) GetEntry(ctx context.Context, key string) (*Entry, error) {
	var out Entry
	err := d.c.get(ctx, "/v1/player/data/"+esc(key), &out)
	return &out, err
}

// CompareAndSet writes only if revision still matches. "*" requires any
// existing save. A conflict is *Error with Code "save_conflict" (HTTP 412).
// Reload and merge explicitly; retries are never automatic.
func (d *DataAPI) CompareAndSet(ctx context.Context, key string, value any, revision string) (*Entry, error) {
	header, err := saveMatchHeader(revision)
	if err != nil {
		return nil, err
	}
	return d.writeConditionally(ctx, key, value, map[string]string{"If-Match": header})
}

// Create writes a new slot only if absent, returning save_conflict otherwise.
func (d *DataAPI) Create(ctx context.Context, key string, value any) (*Entry, error) {
	return d.writeConditionally(ctx, key, value, map[string]string{"If-None-Match": "*"})
}

func (d *DataAPI) writeConditionally(ctx context.Context, key string, value any, headers map[string]string) (*Entry, error) {
	if value == nil {
		value = json.RawMessage("null")
	}
	var out Entry
	err := d.c.doWithHeaders(ctx, http.MethodPut, "/v1/player/data/"+esc(key), value, &out, true, headers)
	return &out, err
}

// DeleteIfRevision removes a save only if the last observed revision matches.
func (d *DataAPI) DeleteIfRevision(ctx context.Context, key, revision string) error {
	header, err := saveMatchHeader(revision)
	if err != nil {
		return err
	}
	return d.c.doWithHeaders(ctx, http.MethodDelete, "/v1/player/data/"+esc(key), nil, nil, true, map[string]string{"If-Match": header})
}

func saveMatchHeader(revision string) (string, error) {
	if revision == "*" {
		return "*", nil
	}
	if !saveRevisionPattern.MatchString(revision) {
		return "", errors.New("zekumo: revision must be the save's UUID revision or *")
	}
	return `"` + revision + `"`, nil
}
