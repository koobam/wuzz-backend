package media

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Local keeps pictures on this machine's disk, for dev only: the gateway serves the directory at /media (Handler) so the browser
// can show what was uploaded without any cloud account. Config refuses it outside dev.
type Local struct {
	Dir           string
	PublicBaseURL string // no trailing slash: http://localhost:8092/media
}

func (l *Local) URL(key string) string { return l.PublicBaseURL + "/" + key }

func (l *Local) path(key string) (string, error) {
	if !validKey(key) {
		return "", fmt.Errorf("media: bad key %q", key)
	}
	return filepath.Join(l.Dir, filepath.FromSlash(key)), nil
}

func (l *Local) Put(_ context.Context, key, _, _ string, body []byte) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("media: %w", err)
	}
	// Write to a temporary name and rename, so a half-written file is never served.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("media: %w", err)
	}
	return os.Rename(tmp, p)
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media: %w", err)
	}
	return nil
}

var contentTypes = map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".avif": "image/avif"}

// Handler serves the directory read-only under a prefix (mount it at /media/*): GET and HEAD only, only the four picture types,
// never listing a directory, with the same immutable cache header the bucket would send.
func (l *Local) Handler(prefix string) http.Handler {
	root := http.Dir(l.Dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, prefix)
		ct, ok := contentTypes[strings.ToLower(filepath.Ext(name))]
		if !ok || !validKey(strings.TrimPrefix(name, "/")) {
			http.NotFound(w, r)
			return
		}
		f, err := root.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", CacheControl)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "", st.ModTime(), f)
	})
}
