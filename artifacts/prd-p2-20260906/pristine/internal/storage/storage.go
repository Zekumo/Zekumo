// Package storage abstracts artifact blob storage. Uploads and downloads go
// directly between the client and storage via presigned URLs; the server only
// brokers metadata.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("object not found")

// PresignTTL is how long generated upload/download URLs stay valid.
const PresignTTL = 15 * time.Minute

type Storage interface {
	// PresignUpload returns a URL the client PUTs the file to.
	PresignUpload(ctx context.Context, key string) (string, error)
	// PresignDownload returns a URL serving the object as an attachment
	// named filename.
	PresignDownload(ctx context.Context, key, filename string) (string, error)
	// Stat returns the stored object's size, or ErrNotFound.
	Stat(ctx context.Context, key string) (int64, error)
	Delete(ctx context.Context, key string) error
	// Put writes an object directly from the server (no presigning).
	Put(ctx context.Context, key string, body io.Reader, size int64) error
}
