// Package media keeps the pictures people upload: a small Storage interface, an S3-compatible implementation (Cloudflare R2,
// Google Cloud Storage's interoperability API, MinIO) and a local-disk one for dev, and the inspection every upload goes
// through before it is stored (what the bytes really are, how big, with the metadata stripped where that is cheap).
package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CacheControl is what every stored picture is served with: a key is never reused, so a copy never goes stale.
const CacheControl = "public, max-age=31536000, immutable"

// Storage is where pictures are put. Keys look like "outlets/2026/10/<random>.webp".
type Storage interface {
	// Put stores body under key with the content type and cache header it will be served with.
	Put(ctx context.Context, key, contentType, cacheControl string, body []byte) error
	// Delete removes the key; deleting a key that is already gone is not an error.
	Delete(ctx context.Context, key string) error
	// URL is the address a browser reads the key from.
	URL(key string) string
}

// Folders is the allow-list of places an upload can go (a request names one; anything else is refused).
var Folders = []string{"outlets", "banners", "vouchers"}

// DefaultFolder is where an upload goes when the request does not say.
const DefaultFolder = "outlets"

// ValidFolder reports whether folder is on the allow-list.
func ValidFolder(folder string) bool {
	for _, f := range Folders {
		if f == folder {
			return true
		}
	}
	return false
}

// NewKey makes the key of a new object: "<folder>/<yyyy>/<mm>/<32 random hex>.<ext>". The random part comes from crypto/rand, so a key
// cannot be guessed from another, and nothing the client sent (its file name above all) is part of it.
func NewKey(folder string, now time.Time, ext string) (string, error) {
	if !ValidFolder(folder) {
		return "", fmt.Errorf("media: folder %q is not allowed", folder)
	}
	if ext == "" || strings.ContainsAny(ext, "/.\\") {
		return "", errors.New("media: bad extension")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("media: random key: %w", err)
	}
	now = now.UTC()
	return fmt.Sprintf("%s/%04d/%02d/%s.%s", folder, now.Year(), int(now.Month()), hex.EncodeToString(raw[:]), ext), nil
}

// validKey is what NewKey can produce: safe to put in a path and a URL.
func validKey(key string) bool {
	if key == "" || len(key) > 200 || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '/', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
