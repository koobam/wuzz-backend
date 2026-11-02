// Package app holds media's use cases. Upload is the only one that does more than read or write a row: it inspects the bytes,
// stores the object and records it, and takes the object back out if the record cannot be written.
package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/media"
	"github.com/koobam/wuzz-backend/internal/services/media/domain"
)

// Repository is what the use cases need of the store.
type Repository interface {
	Insert(ctx context.Context, m domain.Media) (domain.Media, error)
	Get(ctx context.Context, id string) (domain.Media, error)
	BatchGet(ctx context.Context, ids []string) ([]domain.Media, error)
	List(ctx context.Context, q domain.ListQuery) ([]domain.Media, int64, error)
	// Delete removes the row and returns what it was, so the object can be removed after it.
	Delete(ctx context.Context, id string) (domain.Media, error)
}

// App is media's use cases.
type App struct {
	repo     Repository
	store    media.Storage
	maxBytes int64
	now      func() time.Time
	log      *slog.Logger
}

// New builds the use cases. store nil means uploads are not configured: Upload answers unavailable, reads still work.
func New(repo Repository, store media.Storage, maxBytes int64, log *slog.Logger) *App {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	if log == nil {
		log = slog.Default()
	}
	return &App{repo: repo, store: store, maxBytes: maxBytes, now: time.Now, log: log}
}

// MaxBytes is the most an upload may weigh.
func (a *App) MaxBytes() int64 { return a.maxBytes }

// Upload is one picture to store.
type Upload struct {
	Data       []byte
	FileName   string
	Folder     string
	UploadedBy string
}

// Upload inspects the bytes (what they really are, how big, how many pixels; metadata stripped where cheap), stores them under a
// random key and records them. Nothing the client named decides where or as what the picture is kept.
func (a *App) Upload(ctx context.Context, in Upload) (domain.Media, error) {
	if a.store == nil {
		return domain.Media{}, apperr.Unavailable("Uploads are not configured")
	}
	folder := strings.TrimSpace(in.Folder)
	if folder == "" {
		folder = media.DefaultFolder
	}
	if !media.ValidFolder(folder) {
		return domain.Media{}, apperr.Invalid("The folder must be one of: " + strings.Join(media.Folders, ", "))
	}
	img, err := media.Process(in.Data, a.maxBytes)
	if err != nil {
		return domain.Media{}, ProcessError(err, a.maxBytes)
	}
	key, err := media.NewKey(folder, a.now(), img.Ext)
	if err != nil {
		return domain.Media{}, apperr.Internal("The picture could not be stored").WithCause(err)
	}
	if err := a.store.Put(ctx, key, img.ContentType, media.CacheControl, img.Data); err != nil {
		a.log.Error("media: storing the picture failed", slog.String("key", key), slog.Any("error", err))
		return domain.Media{}, apperr.Unavailable("The picture could not be stored, try again").WithCause(err)
	}
	m, err := a.repo.Insert(ctx, domain.Media{
		Key: key, URL: a.store.URL(key), Folder: folder, FileName: CleanName(in.FileName), ContentType: img.ContentType,
		Size: int64(len(img.Data)), Width: img.Width, Height: img.Height, UploadedBy: in.UploadedBy,
	})
	if err != nil {
		// No row, so nothing can ever point at the object: take it back out rather than leave it orphaned in the bucket.
		if derr := a.store.Delete(context.WithoutCancel(ctx), key); derr != nil {
			a.log.Error("media: removing an unrecorded picture failed", slog.String("key", key), slog.Any("error", derr))
		}
		return domain.Media{}, err
	}
	return m, nil
}

// Get is one picture.
func (a *App) Get(ctx context.Context, id string) (domain.Media, error) {
	id, err := domain.ParseID(id)
	if err != nil {
		return domain.Media{}, err
	}
	return a.repo.Get(ctx, id)
}

// BatchGet is several pictures; unknown ids are left out.
func (a *App) BatchGet(ctx context.Context, ids []string) ([]domain.Media, error) {
	ids, err := domain.ParseIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return a.repo.BatchGet(ctx, ids)
}

// List is a page of the library, newest first.
func (a *App) List(ctx context.Context, q domain.ListQuery) ([]domain.Media, int64, error) {
	if err := q.Normalize(); err != nil {
		return nil, 0, err
	}
	return a.repo.List(ctx, q)
}

// Delete removes the record, then the object. The record goes first: a row without an object would be a broken picture anyone
// could still pick, while an object without a row is only storage nobody can reach (and is logged).
func (a *App) Delete(ctx context.Context, id string) error {
	id, err := domain.ParseID(id)
	if err != nil {
		return err
	}
	m, err := a.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if a.store != nil {
		if err := a.store.Delete(context.WithoutCancel(ctx), m.Key); err != nil {
			a.log.Error("media: removing a deleted picture's object failed", slog.String("key", m.Key), slog.Any("error", err))
		}
	}
	return nil
}

// ProcessError is what an upload that failed inspection tells the person who sent it.
func ProcessError(err error, max int64) *apperr.Error {
	switch {
	case errors.Is(err, media.ErrEmpty):
		return apperr.Invalid("The file is empty")
	case errors.Is(err, media.ErrTooLarge):
		return apperr.TooLarge(SizeMessage(max))
	case errors.Is(err, media.ErrUnsupported):
		return apperr.Unsupported("Only JPEG, PNG, WebP or AVIF pictures can be uploaded")
	case errors.Is(err, media.ErrDimensions):
		return apperr.Invalid("The picture is too large in pixels: at most 8000 pixels on a side and 25 megapixels")
	default:
		return apperr.Invalid("The picture is damaged or not a valid image")
	}
}

// SizeMessage names the upload limit in whole megabytes.
func SizeMessage(max int64) string {
	if max%(1<<20) == 0 {
		return "The picture is over the " + itoa(max>>20) + " MB limit"
	}
	return "The picture is over the size limit"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// CleanName is the file name as people see it: the base name only, without control characters, at most 120 characters. It is
// never used to build a key or a path.
func CleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == "/" || name == "" {
		return "picture"
	}
	for utf8.RuneCountInString(name) > 120 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}
