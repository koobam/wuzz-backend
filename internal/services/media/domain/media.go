// Package domain holds media's rules: what a stored picture is, and which ids and pages a caller may ask for.
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/media"
)

// Media is one stored picture.
type Media struct {
	// ID is the public id (a UUID); the table's own key never leaves the service.
	ID          string
	Key         string
	URL         string
	Folder      string
	FileName    string
	ContentType string
	Size        int64
	Width       int
	Height      int
	UploadedBy  string
	CreatedAt   time.Time
}

// MaxBatch is the most ids one BatchGetMedia may ask for.
const MaxBatch = 200

// ListQuery is a page of the library.
type ListQuery struct {
	Folder string
	Limit  int
	Offset int
}

// DefaultPage and MaxPage bound a page of the library.
const (
	DefaultPage = 40
	MaxPage     = 200
)

// Normalize checks the folder and bounds the page.
func (q *ListQuery) Normalize() error {
	q.Folder = strings.TrimSpace(q.Folder)
	if q.Folder != "" && !media.ValidFolder(q.Folder) {
		return apperr.Invalid("The folder must be one of: " + strings.Join(media.Folders, ", "))
	}
	if q.Limit <= 0 {
		q.Limit = DefaultPage
	}
	if q.Limit > MaxPage {
		q.Limit = MaxPage
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return nil
}

// ParseID checks that id is a media id, so a malformed one is refused as invalid instead of reaching the database.
func ParseID(id string) (string, error) {
	u, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", apperr.Invalid("The media id is invalid")
	}
	return u.String(), nil
}

// ParseIDs checks every id and drops repeats, keeping the first order.
func ParseIDs(ids []string) ([]string, error) {
	if len(ids) > MaxBatch {
		return nil, apperr.Invalid("At most 200 media ids can be read at once")
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id, err := ParseID(raw)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}
