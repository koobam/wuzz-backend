package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	mediav1 "github.com/koobam/wuzz-backend/gen/wuzz/media/v1"
	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
	"github.com/koobam/wuzz-backend/internal/services/media/app"
	"github.com/koobam/wuzz-backend/internal/services/media/domain"
)

// fakeMedia is the media service in-process: the real use cases over an in-memory repository and store, with errors turned into
// gRPC statuses the way the service's interceptor does, so the gateway is tested against what it really receives.
type fakeMedia struct {
	mediav1.MediaServiceClient
	app     *app.App
	store   *memStore
	uploads int
}

type memStore struct{ objects map[string][]byte }

func (m *memStore) Put(_ context.Context, key, _, _ string, body []byte) error {
	m.objects[key] = body
	return nil
}
func (m *memStore) Delete(_ context.Context, key string) error { delete(m.objects, key); return nil }
func (m *memStore) URL(key string) string                      { return "https://assets.test/" + key }

type memRepo struct{ rows map[string]domain.Media }

func (r *memRepo) Insert(_ context.Context, m domain.Media) (domain.Media, error) {
	m.ID = "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	r.rows[m.ID] = m
	return m, nil
}
func (r *memRepo) Get(_ context.Context, id string) (domain.Media, error) {
	if m, ok := r.rows[id]; ok {
		return m, nil
	}
	return domain.Media{}, apperr.NotFound("Media not found")
}
func (r *memRepo) BatchGet(context.Context, []string) ([]domain.Media, error) { return nil, nil }
func (r *memRepo) List(context.Context, domain.ListQuery) ([]domain.Media, int64, error) {
	out := make([]domain.Media, 0, len(r.rows))
	for _, m := range r.rows {
		out = append(out, m)
	}
	return out, int64(len(out)), nil
}
func (r *memRepo) Delete(ctx context.Context, id string) (domain.Media, error) {
	m, err := r.Get(ctx, id)
	if err == nil {
		delete(r.rows, id)
	}
	return m, err
}

func newFakeMedia(max int64) *fakeMedia {
	store := &memStore{objects: map[string][]byte{}}
	return &fakeMedia{app: app.New(&memRepo{rows: map[string]domain.Media{}}, store, max, nil), store: store}
}

func asStatus(err error) error { return apperr.From(err).GRPCStatus().Err() }

func proto(m domain.Media) *mediav1.Media {
	return &mediav1.Media{Id: m.ID, Key: m.Key, Url: m.URL, Folder: m.Folder, FileName: m.FileName, ContentType: m.ContentType, Size: m.Size,
		Width: int32(m.Width), Height: int32(m.Height), CreatedAt: timestamppb.New(m.CreatedAt)}
}

func (f *fakeMedia) Upload(ctx context.Context, in *mediav1.UploadRequest, _ ...grpc.CallOption) (*mediav1.UploadResponse, error) {
	f.uploads++
	m, err := f.app.Upload(ctx, app.Upload{Data: in.GetData(), FileName: in.GetFileName(), Folder: in.GetFolder()})
	if err != nil {
		return nil, asStatus(err)
	}
	return &mediav1.UploadResponse{Media: proto(m)}, nil
}
func (f *fakeMedia) ListMedia(ctx context.Context, in *mediav1.ListMediaRequest, _ ...grpc.CallOption) (*mediav1.ListMediaResponse, error) {
	list, total, err := f.app.List(ctx, domain.ListQuery{Folder: in.GetFolder(), Limit: int(in.GetLimit()), Offset: int(in.GetOffset())})
	if err != nil {
		return nil, asStatus(err)
	}
	out := &mediav1.ListMediaResponse{Total: total}
	for _, m := range list {
		out.Media = append(out.Media, proto(m))
	}
	return out, nil
}
func (f *fakeMedia) DeleteMedia(ctx context.Context, in *mediav1.DeleteMediaRequest, _ ...grpc.CallOption) (*mediav1.DeleteMediaResponse, error) {
	if err := f.app.Delete(ctx, in.GetId()); err != nil {
		return nil, asStatus(err)
	}
	return &mediav1.DeleteMediaResponse{}, nil
}

func router(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(httpx.ErrorHandler())
	e.POST("/uploads", h.Create)
	e.GET("/media", h.List)
	e.DELETE("/media/:id", h.Delete)
	return e
}

func pngFile(t *testing.T, w, hgt int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, hgt))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type part struct {
	field, filename, contentType string
	data                         []byte
}

func form(t *testing.T, fields map[string]string, parts ...part) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for _, p := range parts {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", `form-data; name="`+p.field+`"; filename="`+p.filename+`"`)
		hdr.Set("Content-Type", p.contentType)
		pw, err := w.CreatePart(hdr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write(p.data)
	}
	_ = w.Close()
	return &body, w.FormDataContentType()
}

func do(e *gin.Engine, method, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestCreate(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 3000)
	keyShape := regexp.MustCompile(`^(outlets|banners|vouchers|services)/\d{4}/\d{2}/[0-9a-f]{32}\.png$`)
	cases := []struct {
		name     string
		fields   map[string]string
		parts    []part
		max      int64
		wantHTTP int
		wantMsg  string
	}{
		{"a png", nil, []part{{"file", "Pusat Depan.PNG", "image/jpeg", pngFile(t, 20, 10)}}, 1 << 20, 201, ""},
		{"the declared type and the extension do not matter", nil, []part{{"file", "photo.exe", "application/x-msdownload", pngFile(t, 8, 8)}}, 1 << 20, 201, ""},
		{"the banners folder", map[string]string{"folder": "banners"}, []part{{"file", "a.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 201, ""},
		{"the services folder", map[string]string{"folder": "services"}, []part{{"file", "kiloan.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 201, ""},
		{"a folder that is not allowed", map[string]string{"folder": "../etc"}, []part{{"file", "a.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 400, "folder"},
		{"no file field", nil, nil, 1 << 20, 400, "required"},
		{"text renamed .png", nil, []part{{"file", "evil.png", "image/png", []byte("<html><script>alert(1)</script></html>")}}, 1 << 20, 415, "JPEG, PNG, WebP or AVIF"},
		{"an empty file", nil, []part{{"file", "a.png", "image/png", nil}}, 1 << 20, 400, "empty"},
		{"over the limit", nil, []part{{"file", "a.png", "image/png", append(pngFile(t, 8, 8), big...)}}, 2048, 413, "limit"},
		{"a broken png", nil, []part{{"file", "a.png", "image/png", pngFile(t, 30, 30)[:40]}}, 1 << 20, 400, "damaged"},
	}
	for _, c := range cases {
		fm := newFakeMedia(c.max)
		h := &Handler{Media: fm, MaxBytes: c.max}
		body, ct := form(t, c.fields, c.parts...)
		rec := do(router(h), http.MethodPost, "/uploads", body, ct)
		if rec.Code != c.wantHTTP {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.wantHTTP, rec.Body.String())
			continue
		}
		var env envelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if c.wantHTTP != 201 {
			if len(fm.store.objects) != 0 {
				t.Errorf("%s: a refused upload must store nothing, stored %d", c.name, len(fm.store.objects))
			}
			if !strings.Contains(strings.ToLower(env.Message), strings.ToLower(c.wantMsg)) {
				t.Errorf("%s: message %q should mention %q", c.name, env.Message, c.wantMsg)
			}
			continue
		}
		var res Result
		if err := json.Unmarshal(env.Data, &res); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.ID == "" || !keyShape.MatchString(res.Key) || res.URL != "https://assets.test/"+res.Key || res.ContentType != "image/png" ||
			res.Width == 0 || res.Height == 0 || res.Size != len(fm.store.objects[res.Key]) {
			t.Errorf("%s: result %+v", c.name, res)
		}
		if strings.Contains(res.Key, "evil") || strings.Contains(strings.ToLower(res.Key), "photo") || strings.Contains(strings.ToLower(res.Key), "pusat") {
			t.Errorf("%s: the client's file name leaked into the key %q", c.name, res.Key)
		}
	}
}

func TestCreateRefusesAtTheEdgeWithoutCallingTheService(t *testing.T) {
	fm := newFakeMedia(2048)
	h := &Handler{Media: fm, MaxBytes: 2048}
	body, ct := form(t, nil, part{"file", "a.png", "image/png", bytes.Repeat([]byte("x"), 5000)})
	if rec := do(router(h), http.MethodPost, "/uploads", body, ct); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := do(router(h), http.MethodPost, "/uploads", bytes.NewBufferString(`{"file":"x"}`), "application/json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if fm.uploads != 0 {
		t.Fatalf("what the edge refuses must not reach the media service, got %d calls", fm.uploads)
	}
}

func TestCreateReportsTheFileName(t *testing.T) {
	h := &Handler{Media: newFakeMedia(1 << 20), MaxBytes: 1 << 20}
	body, ct := form(t, nil, part{"file", "../../etc/Pusat Depan.png", "image/png", pngFile(t, 8, 8)})
	rec := do(router(h), http.MethodPost, "/uploads", body, ct)
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	var res Result
	_ = json.Unmarshal(env.Data, &res)
	if rec.Code != 201 || res.FileName != "Pusat Depan.png" {
		t.Errorf("status %d, file_name %q", rec.Code, res.FileName)
	}
}

func TestNotConfigured(t *testing.T) {
	h := &Handler{MaxBytes: 1 << 20}
	body, ct := form(t, nil, part{"file", "a.png", "image/png", pngFile(t, 8, 8)})
	rec := do(router(h), http.MethodPost, "/uploads", body, ct)
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(env.Message, "Uploads are not configured") {
		t.Errorf("status %d message %q", rec.Code, env.Message)
	}
}

func TestListAndDelete(t *testing.T) {
	fm := newFakeMedia(1 << 20)
	h := &Handler{Media: fm, MaxBytes: 1 << 20}
	body, ct := form(t, map[string]string{"folder": "services"}, part{"file", "kiloan.png", "image/png", pngFile(t, 8, 8)})
	if rec := do(router(h), http.MethodPost, "/uploads", body, ct); rec.Code != 201 {
		t.Fatalf("upload status %d", rec.Code)
	}
	rec := do(router(h), http.MethodGet, "/media?folder=services", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) || !strings.Contains(rec.Body.String(), "kiloan.png") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(router(h), http.MethodGet, "/media?folder=secrets", nil, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown folder is a 400, got %d", rec.Code)
	}
	if rec := do(router(h), http.MethodDelete, "/media/6f9619ff-8b86-d011-b42d-00c04fc964ff", nil, ""); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(fm.store.objects) != 0 {
		t.Fatal("the object must be gone after the delete")
	}
	if rec := do(router(h), http.MethodDelete, "/media/6f9619ff-8b86-d011-b42d-00c04fc964ff", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a second delete is a 404, got %d", rec.Code)
	}
}
