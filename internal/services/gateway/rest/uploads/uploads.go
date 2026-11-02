// Package uploads is the back office's picture library: POST /backoffice/v1/uploads sends a picture to the media service, which
// inspects it (what it really is, how big, how many pixels), stores it under a random key and records it. The answer carries the
// picture's media id, which is what the rest of the system refers to it by, and the address it is served from.
//
// GET /backoffice/v1/media lists the library and DELETE /backoffice/v1/media/:id removes a picture. The gateway only checks what it
// cheaply can at the edge (the form, the size); everything about the bytes is the media service's.
package uploads

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	mediav1 "github.com/koobam/wuzz-backend/gen/wuzz/media/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/pkg/media"
)

// Handler serves the library routes. MaxBytes is the edge's own size check; the media service checks again.
type Handler struct {
	Media    mediav1.MediaServiceClient
	MaxBytes int64
}

// Result is a picture as the back office sees it. The first seven fields are what uploads have always answered.
type Result struct {
	URL         string `json:"url"`
	Key         string `json:"key"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	// ID is the media id: store this, not the address, where a picture is referred to.
	ID        string `json:"id"`
	Folder    string `json:"folder"`
	CreatedAt string `json:"created_at,omitempty"`
}

func result(m *mediav1.Media) Result {
	r := Result{URL: m.GetUrl(), Key: m.GetKey(), FileName: m.GetFileName(), ContentType: m.GetContentType(), Size: int(m.GetSize()),
		Width: int(m.GetWidth()), Height: int(m.GetHeight()), ID: m.GetId(), Folder: m.GetFolder()}
	if m.GetCreatedAt() != nil {
		r.CreatedAt = m.GetCreatedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	return r
}

// formOverhead is room for the multipart framing and the small text fields around the file.
const formOverhead = 64 << 10

func (h *Handler) maxBytes() int64 {
	if h.MaxBytes <= 0 {
		return 5 << 20
	}
	return h.MaxBytes
}

func (h *Handler) Create(c *gin.Context) {
	if h.Media == nil {
		httpx.Fail(c, apperr.Unavailable("Uploads are not configured"))
		return
	}
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/form-data") {
		httpx.Fail(c, apperr.Invalid("Send the picture as multipart/form-data in the field \"file\""))
		return
	}
	max := h.maxBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max+formOverhead)

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
			httpx.Fail(c, apperr.TooLarge(sizeMessage(max)))
			return
		}
		httpx.Fail(c, apperr.Invalid("A picture is required in the form field \"file\""))
		return
	}
	if fh.Size > max {
		httpx.Fail(c, apperr.TooLarge(sizeMessage(max)))
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The picture could not be read"))
		return
	}
	defer f.Close()
	// One byte more than allowed, so a file that lies about its size is still caught.
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		httpx.Fail(c, apperr.Invalid("The picture could not be read"))
		return
	}
	if int64(len(data)) > max {
		httpx.Fail(c, apperr.TooLarge(sizeMessage(max)))
		return
	}

	res, err := h.Media.Upload(c.Request.Context(), &mediav1.UploadRequest{Data: data, FileName: fh.Filename, Folder: folder})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.Created(c, result(res.GetMedia()))
}

// List is GET /backoffice/v1/media?folder=&limit=&offset=: the library, newest first.
func (h *Handler) List(c *gin.Context) {
	if h.Media == nil {
		httpx.Fail(c, apperr.Unavailable("Uploads are not configured"))
		return
	}
	res, err := h.Media.ListMedia(c.Request.Context(), &mediav1.ListMediaRequest{Folder: c.Query("folder"),
		Limit: int32(httpx.IntQuery(c, "limit", 40)), Offset: int32(httpx.IntQuery(c, "offset", 0))})
	if err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	items := make([]Result, 0, len(res.GetMedia()))
	for _, m := range res.GetMedia() {
		items = append(items, result(m))
	}
	httpx.OK(c, gin.H{"items": items, "total": res.GetTotal()})
}

// Delete is DELETE /backoffice/v1/media/:id. Whatever still shows the picture keeps a dead address, so the back office deletes only
// what nothing uses.
func (h *Handler) Delete(c *gin.Context) {
	if h.Media == nil {
		httpx.Fail(c, apperr.Unavailable("Uploads are not configured"))
		return
	}
	id := c.Param("id")
	if _, err := h.Media.DeleteMedia(c.Request.Context(), &mediav1.DeleteMediaRequest{Id: id}); err != nil {
		httpx.Fail(c, httpx.Upstream(err))
		return
	}
	httpx.OK(c, gin.H{"id": id})
}

func sizeMessage(max int64) string {
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
