// Package uploads is POST /backoffice/v1/uploads: a picture goes in as multipart form data and comes back as the address it is now
// served from. The bytes are inspected before anything is stored (what they really are, how big, how many pixels), the key is random,
// and nothing the client named (its file name above all) decides where or as what the picture is kept.
package uploads

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/pkg/media"
)

// Handler serves the upload route. Store nil means uploads are not configured: the route answers 503 and nothing else.
type Handler struct {
	Store    media.Storage
	MaxBytes int64
	Now      func() time.Time
	Log      *slog.Logger
}

// New builds the handler for a media configuration. It returns the local-disk store too (nil otherwise) so the gateway can serve it.
func New(cfg config.Media, log *slog.Logger) (*Handler, *media.Local) {
	h := &Handler{MaxBytes: cfg.MaxUploadBytes, Log: log}
	if h.MaxBytes <= 0 {
		h.MaxBytes = 5 << 20
	}
	switch {
	case cfg.LocalDir != "":
		local := &media.Local{Dir: cfg.LocalDir, PublicBaseURL: cfg.PublicBaseURL}
		h.Store = local
		return h, local
	case cfg.Configured():
		h.Store = &media.S3{
			Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, AccessKeyID: cfg.AccessKeyID, SecretAccessKey: cfg.SecretAccessKey,
			Region: cfg.Region, PublicBaseURL: cfg.PublicBaseURL,
		}
	}
	return h, nil
}

// Result is what a successful upload answers.
type Result struct {
	URL         string `json:"url"`
	Key         string `json:"key"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// formOverhead is room for the multipart framing and the small text fields around the file.
const formOverhead = 64 << 10

func (h *Handler) Create(c *gin.Context) {
	if h.Store == nil {
		httpx.Fail(c, apperr.Unavailable("Uploads are not configured"))
		return
	}
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/form-data") {
		httpx.Fail(c, apperr.Invalid("Send the picture as multipart/form-data in the field \"file\""))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.MaxBytes+formOverhead)

	folder := strings.TrimSpace(c.PostForm("folder"))
	if folder == "" {
		folder = media.DefaultFolder
	}
	if !media.ValidFolder(folder) {
		httpx.Fail(c, apperr.Invalid("The folder must be one of: "+strings.Join(media.Folders, ", ")))
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.Fail(c, apperr.TooLarge(sizeMessage(h.MaxBytes)))
			return
		}
		httpx.Fail(c, apperr.Invalid("A picture is required in the form field \"file\""))
		return
	}
	if fh.Size > h.MaxBytes {
		httpx.Fail(c, apperr.TooLarge(sizeMessage(h.MaxBytes)))
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The picture could not be read"))
		return
	}
	defer f.Close()
	// One byte more than allowed, so a file that lies about its size is still caught.
	data, err := io.ReadAll(io.LimitReader(f, h.MaxBytes+1))
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The picture could not be read"))
		return
	}

	img, err := media.Process(data, h.MaxBytes)
	if err != nil {
		httpx.Fail(c, processError(err, h.MaxBytes))
		return
	}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	key, err := media.NewKey(folder, now(), img.Ext)
	if err != nil {
		httpx.Fail(c, apperr.Internal("The picture could not be stored").WithCause(err))
		return
	}
	if err := h.Store.Put(c.Request.Context(), key, img.ContentType, media.CacheControl, img.Data); err != nil {
		if h.Log != nil {
			h.Log.Error("upload: storing the picture failed", slog.String("key", key), slog.Any("error", err))
		}
		httpx.Fail(c, apperr.Unavailable("The picture could not be stored, try again").WithCause(err))
		return
	}
	httpx.Created(c, Result{
		URL: h.Store.URL(key), Key: key, FileName: cleanName(fh.Filename), ContentType: img.ContentType,
		Size: len(img.Data), Width: img.Width, Height: img.Height,
	})
}

func processError(err error, max int64) *apperr.Error {
	switch {
	case errors.Is(err, media.ErrEmpty):
		return apperr.Invalid("The file is empty")
	case errors.Is(err, media.ErrTooLarge):
		return apperr.TooLarge(sizeMessage(max))
	case errors.Is(err, media.ErrUnsupported):
		return apperr.Unsupported("Only JPEG, PNG, WebP or AVIF pictures can be uploaded")
	case errors.Is(err, media.ErrDimensions):
		return apperr.Invalid("The picture is too large in pixels: at most 8000 pixels on a side and 25 megapixels")
	default:
		return apperr.Invalid("The picture is damaged or not a valid image")
	}
}

func sizeMessage(max int64) string {
	mb := float64(max) / (1 << 20)
	if mb == float64(int64(mb)) {
		return "The picture is over the " + itoa(int64(mb)) + " MB limit"
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

// cleanName is the file name as people see it in the log: the base name only, without control characters, at most 120 characters. It
// is never used to build a key or a path.
func cleanName(name string) string {
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
