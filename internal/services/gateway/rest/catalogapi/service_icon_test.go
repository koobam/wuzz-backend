package catalogapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	mediav1 "github.com/koobam/wuzz-backend/gen/wuzz/media/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
)

type fakeMedia struct {
	mediav1.MediaServiceClient
	known map[string]string
	calls int
}

func (f *fakeMedia) GetMedia(_ context.Context, in *mediav1.GetMediaRequest, _ ...grpc.CallOption) (*mediav1.GetMediaResponse, error) {
	f.calls++
	url, ok := f.known[in.GetId()]
	if !ok {
		return nil, apperr.NotFound("Media not found").GRPCStatus().Err()
	}
	return &mediav1.GetMediaResponse{Media: &mediav1.Media{Id: in.GetId(), Url: url}}, nil
}

func TestServiceIconResolvesTheAddress(t *testing.T) {
	const id = "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	media := &fakeMedia{known: map[string]string{id: "https://assets.test/services/2026/11/k.webp"}}
	h := &Handler{Media: media}
	gin.SetMode(gin.TestMode)
	ctx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		return c, w
	}

	c, _ := ctx()
	gotID, gotURL, ok := h.serviceIcon(c, " "+id+" ")
	if !ok || gotID != id || gotURL != "https://assets.test/services/2026/11/k.webp" {
		t.Fatalf("got %q %q ok=%v", gotID, gotURL, ok)
	}

	c, _ = ctx()
	if gotID, gotURL, ok := h.serviceIcon(c, ""); !ok || gotID != "" || gotURL != "" {
		t.Fatalf("empty must be no picture, got %q %q ok=%v", gotID, gotURL, ok)
	}
	if media.calls != 1 {
		t.Fatalf("no picture must not call the media service, calls=%d", media.calls)
	}

	c, _ = ctx()
	if _, _, ok := h.serviceIcon(c, "0b0b0b0b-0000-4000-8000-000000000000"); ok {
		t.Fatal("an unknown media id must be refused")
	}
	if e := apperr.From(c.Errors.Last().Err); e.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("an unknown media id is a 400, got %d", e.HTTPStatus())
	}

	c, _ = ctx()
	if _, _, ok := (&Handler{}).serviceIcon(c, id); ok {
		t.Fatal("without a media client a picture cannot be resolved")
	}
}
