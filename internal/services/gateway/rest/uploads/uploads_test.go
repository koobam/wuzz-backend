package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

type fakeStore struct {
	puts   map[string][]byte
	types  map[string]string
	cache  map[string]string
	failed error
}

func newFake() *fakeStore {
	return &fakeStore{puts: map[string][]byte{}, types: map[string]string{}, cache: map[string]string{}}
}
func (f *fakeStore) Put(_ context.Context, key, ct, cc string, body []byte) error {
	if f.failed != nil {
		return f.failed
	}
	f.puts[key], f.types[key], f.cache[key] = body, ct, cc
	return nil
}
func (f *fakeStore) Delete(context.Context, string) error { return nil }
func (f *fakeStore) URL(key string) string                { return "https://assets.test/" + key }

func router(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(httpx.ErrorHandler())
	e.POST("/uploads", h.Create)
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

func post(e *gin.Engine, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/uploads", body)
	req.Header.Set("Content-Type", contentType)
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
	keyShape := regexp.MustCompile(`^(outlets|banners|vouchers)/2026/10/[0-9a-f]{32}\.png$`)
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
		{"an allowed folder", map[string]string{"folder": "outlets"}, []part{{"file", "a.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 201, ""},
		{"the banners folder", map[string]string{"folder": "banners"}, []part{{"file", "a.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 201, ""},
		{"a folder that is not allowed", map[string]string{"folder": "../etc"}, []part{{"file", "a.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 400, "folder"},
		{"no file field", nil, nil, 1 << 20, 400, "required"},
		{"the file under another field name", nil, []part{{"image", "a.png", "image/png", pngFile(t, 8, 8)}}, 1 << 20, 400, "required"},
		{"text renamed .png", nil, []part{{"file", "evil.png", "image/png", []byte("<html><script>alert(1)</script></html>")}}, 1 << 20, 415, "JPEG, PNG, WebP or AVIF"},
		{"an empty file", nil, []part{{"file", "a.png", "image/png", nil}}, 1 << 20, 400, "empty"},
		{"over the limit", nil, []part{{"file", "a.png", "image/png", append(pngFile(t, 8, 8), big...)}}, 2048, 413, "limit"},
		{"a broken png", nil, []part{{"file", "a.png", "image/png", pngFile(t, 30, 30)[:40]}}, 1 << 20, 400, "damaged"},
	}
	for _, c := range cases {
		store := newFake()
		h := &Handler{Store: store, MaxBytes: c.max, Now: func() time.Time { return time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC) }}
		body, ct := form(t, c.fields, c.parts...)
		rec := post(router(h), body, ct)
		if rec.Code != c.wantHTTP {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.wantHTTP, rec.Body.String())
			continue
		}
		var env envelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if c.wantHTTP != 201 {
			if len(store.puts) != 0 {
				t.Errorf("%s: a refused upload must store nothing, stored %d", c.name, len(store.puts))
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
		if !keyShape.MatchString(res.Key) || res.URL != "https://assets.test/"+res.Key || res.ContentType != "image/png" || res.Width == 0 || res.Height == 0 || res.Size != len(store.puts[res.Key]) {
			t.Errorf("%s: result %+v", c.name, res)
		}
		if store.types[res.Key] != "image/png" || store.cache[res.Key] != "public, max-age=31536000, immutable" {
			t.Errorf("%s: stored as %q with cache %q", c.name, store.types[res.Key], store.cache[res.Key])
		}
		if strings.Contains(res.Key, "evil") || strings.Contains(strings.ToLower(res.Key), "photo") || strings.Contains(strings.ToLower(res.Key), "pusat") {
			t.Errorf("%s: the client's file name leaked into the key %q", c.name, res.Key)
		}
	}
}

func TestCreateKeepsTheFileNameOutOfTheKeyButReportsIt(t *testing.T) {
	store := newFake()
	h := &Handler{Store: store, MaxBytes: 1 << 20}
	body, ct := form(t, nil, part{"file", "../../etc/Pusat Depan.png", "image/png", pngFile(t, 8, 8)})
	rec := post(router(h), body, ct)
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	var res Result
	_ = json.Unmarshal(env.Data, &res)
	if rec.Code != 201 || res.FileName != "Pusat Depan.png" {
		t.Errorf("status %d, file_name %q", rec.Code, res.FileName)
	}
}

func TestCreateNotConfigured(t *testing.T) {
	h, local := New(config.Media{MaxUploadBytes: 1 << 20}, nil)
	if local != nil || h.Store != nil {
		t.Fatal("with no media settings there is no store")
	}
	body, ct := form(t, nil, part{"file", "a.png", "image/png", pngFile(t, 8, 8)})
	rec := post(router(h), body, ct)
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(env.Message, "Uploads are not configured") {
		t.Errorf("status %d message %q", rec.Code, env.Message)
	}
}

func TestCreateNeedsMultipart(t *testing.T) {
	h := &Handler{Store: newFake(), MaxBytes: 1 << 20}
	rec := post(router(h), bytes.NewBufferString(`{"file":"x"}`), "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d", rec.Code)
	}
}

func TestCreateStorageDown(t *testing.T) {
	store := newFake()
	store.failed = errors.New("dial tcp: connection refused")
	h := &Handler{Store: store, MaxBytes: 1 << 20}
	body, ct := form(t, nil, part{"file", "a.png", "image/png", pngFile(t, 8, 8)})
	rec := post(router(h), body, ct)
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(env.Message, "refused") {
		t.Errorf("status %d message %q: a 503 that does not leak the storage error", rec.Code, env.Message)
	}
}

func TestNewPicksTheStore(t *testing.T) {
	s3, local := New(config.Media{Endpoint: "https://r2.example", Bucket: "b", AccessKeyID: "i", SecretAccessKey: "s", PublicBaseURL: "https://assets.wuzz.care", Region: "auto"}, nil)
	if s3.Store == nil || local != nil || s3.Store.URL("k.png") != "https://assets.wuzz.care/k.png" {
		t.Errorf("s3: %+v", s3.Store)
	}
	dev, local := New(config.Media{LocalDir: t.TempDir(), PublicBaseURL: "http://localhost:8092/media"}, nil)
	if dev.Store == nil || local == nil || dev.Store.URL("k.png") != "http://localhost:8092/media/k.png" {
		t.Errorf("local: %+v", dev.Store)
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"a.png": "a.png", "../../x/y.png": "y.png", `C:\fakepath\z.png`: "z.png", "": "picture", "..": "..", "tab\there.png": "tabhere.png", strings.Repeat("é", 200): strings.Repeat("é", 120),
	} {
		if got := cleanName(in); got != want && !(in == ".." && got == "..") {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
