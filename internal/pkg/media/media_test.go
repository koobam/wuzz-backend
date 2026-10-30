package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{200, 10, 10, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExif puts an APP1 EXIF segment holding only an orientation right after the JPEG's start marker.
func withExif(jpg []byte, orientation uint16) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(tiff[18:20], orientation)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(out[4:6], uint16(len(seg)+2))
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func webpLossless(w, h int) []byte {
	b := make([]byte, 30)
	copy(b, "RIFF")
	copy(b[8:], "WEBPVP8L")
	b[20] = 0x2f
	binary.LittleEndian.PutUint32(b[21:25], uint32(w-1)|uint32(h-1)<<14)
	return b
}

func avifOf(w, h uint32) []byte {
	b := make([]byte, 0, 64)
	b = append(b, 0, 0, 0, 24)
	b = append(b, "ftypavif"...)
	b = append(b, 0, 0, 0, 0)
	b = append(b, "avifmif1"...)
	b = append(b, 0, 0, 0, 20)
	b = append(b, "ispe"...)
	b = append(b, 0, 0, 0, 0)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	return b
}

func TestKind(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", jpegBytes(t, 2, 2), "image/jpeg"},
		{"png", pngBytes(t, 2, 2), "image/png"},
		{"webp", webpLossless(10, 10), "image/webp"},
		{"avif", avifOf(10, 10), "image/avif"},
		{"text named .png", []byte("hello, I am not a picture"), ""},
		{"html", []byte("<html><script>alert(1)</script></html>"), ""},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), ""},
		{"gif", []byte("GIF89a....."), ""},
		{"empty", nil, ""},
		{"riff but not webp", []byte("RIFF....WAVEfmt "), ""},
		{"ftyp but heic", append([]byte{0, 0, 0, 24}, "ftypheic....mif1heic"...), ""},
	}
	for _, c := range cases {
		if got, _ := Kind(c.data); got != c.want {
			t.Errorf("%s: Kind = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestProcess(t *testing.T) {
	const max = 1 << 20
	cases := []struct {
		name   string
		data   []byte
		max    int64
		err    error
		wantCT string
		w, h   int
	}{
		{"png", pngBytes(t, 40, 30), max, nil, "image/png", 40, 30},
		{"jpeg", jpegBytes(t, 64, 48), max, nil, "image/jpeg", 64, 48},
		{"jpeg rotated by exif 6 swaps the sides", withExif(jpegBytes(t, 64, 48), 6), max, nil, "image/jpeg", 48, 64},
		{"jpeg exif 1 stays", withExif(jpegBytes(t, 64, 48), 1), max, nil, "image/jpeg", 64, 48},
		{"webp passes through", webpLossless(300, 200), max, nil, "image/webp", 300, 200},
		{"avif passes through", avifOf(640, 480), max, nil, "image/avif", 640, 480},
		{"empty", nil, max, ErrEmpty, "", 0, 0},
		{"over the limit", pngBytes(t, 200, 200), 100, ErrTooLarge, "", 0, 0},
		{"not a picture", []byte(strings.Repeat("x", 500)), max, ErrUnsupported, "", 0, 0},
		{"truncated png", pngBytes(t, 40, 30)[:30], max, ErrCorrupt, "", 0, 0},
		{"webp too wide", webpLossless(9000, 100), max, ErrDimensions, "", 0, 0},
		{"webp too many pixels", webpLossless(7000, 7000), max, ErrDimensions, "", 0, 0},
		{"avif bomb", avifOf(60000, 60000), max, ErrDimensions, "", 0, 0},
		{"avif without ispe", append([]byte{0, 0, 0, 24}, "ftypavif....avifmif1"...), max, ErrCorrupt, "", 0, 0},
	}
	for _, c := range cases {
		got, err := Process(c.data, c.max)
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
			continue
		}
		if err != nil {
			continue
		}
		if got.ContentType != c.wantCT || got.Width != c.w || got.Height != c.h {
			t.Errorf("%s: got %s %dx%d, want %s %dx%d", c.name, got.ContentType, got.Width, got.Height, c.wantCT, c.w, c.h)
		}
	}
}

func TestProcessStripsMetadata(t *testing.T) {
	in := withExif(jpegBytes(t, 32, 32), 1)
	out, err := Process(in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Data, []byte("Exif")) {
		t.Error("the stored JPEG still carries its EXIF segment")
	}
}

func TestNewKey(t *testing.T) {
	now := time.Date(2026, time.October, 25, 23, 0, 0, 0, time.FixedZone("WIB", 7*3600)) // 16:00 UTC, still October
	pattern := regexp.MustCompile(`^outlets/2026/10/[0-9a-f]{32}\.webp$`)
	seen := map[string]bool{}
	for range 200 {
		k, err := NewKey("outlets", now, "webp")
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(k) {
			t.Fatalf("key %q does not look like outlets/yyyy/mm/<32 hex>.ext", k)
		}
		if seen[k] {
			t.Fatalf("key %q came up twice", k)
		}
		seen[k] = true
	}
	for _, bad := range []struct{ folder, ext string }{{"avatars", "jpg"}, {"../outlets", "jpg"}, {"outlets/x", "jpg"}, {"", "jpg"}, {"outlets", ""}, {"outlets", "j/g"}, {"outlets", "j.g"}} {
		if _, err := NewKey(bad.folder, now, bad.ext); err == nil {
			t.Errorf("NewKey(%q, %q) should refuse", bad.folder, bad.ext)
		}
	}
	// month rolls over in UTC, not in the server's zone
	if k, _ := NewKey("outlets", time.Date(2026, 11, 1, 3, 0, 0, 0, time.FixedZone("WIB", 7*3600)), "jpg"); !strings.HasPrefix(k, "outlets/2026/10/") {
		t.Errorf("key %q: 03:00 WIB on 1 Nov is still 31 Oct in UTC", k)
	}
}

func TestValidKey(t *testing.T) {
	for key, ok := range map[string]bool{
		"outlets/2026/10/abc123.jpg": true, "a-b_c.png": true,
		"": false, "/abs.jpg": false, "../x.jpg": false, "a/../b.jpg": false, "has space.jpg": false, "UPPER.jpg": false, "q?x=1.jpg": false, strings.Repeat("a", 201): false,
	} {
		if validKey(key) != ok {
			t.Errorf("validKey(%q) = %v, want %v", key, !ok, ok)
		}
	}
}

// The AWS documentation's worked example for "GET Object" with a Range header: the signature below is the one published there
// (docs.aws.amazon.com, Signature Calculations for the Authorization Header), so signV4 is checked against the reference, not against itself.
func TestSignV4AWSExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	emptyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	signV4(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3", emptyHash, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func TestS3PutAndDelete(t *testing.T) {
	var gotPath, gotCT, gotCache, gotAuth, gotHash string
	var gotBody []byte
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			gotPath, gotCT, gotCache, gotAuth, gotHash = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Cache-Control"), r.Header.Get("Authorization"), r.Header.Get("X-Amz-Content-Sha256")
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(r.Body)
			gotBody = buf.Bytes()
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := &S3{Endpoint: srv.URL, Bucket: "wuzz-media", AccessKeyID: "id", SecretAccessKey: "secret", Region: "auto", PublicBaseURL: "https://assets.wuzz.care"}

	if err := s.Put(context.Background(), "outlets/2026/10/abc.png", "image/png", CacheControl, []byte("png-bytes")); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/wuzz-media/outlets/2026/10/abc.png" || gotCT != "image/png" || gotCache != CacheControl || string(gotBody) != "png-bytes" {
		t.Errorf("stored %q ct=%q cache=%q body=%q", gotPath, gotCT, gotCache, gotBody)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=id/") || !strings.Contains(gotAuth, "/auto/s3/aws4_request") || !strings.Contains(gotAuth, "cache-control;content-type;host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if len(gotHash) != 64 {
		t.Errorf("x-amz-content-sha256 = %q", gotHash)
	}
	if got := s.URL("outlets/2026/10/abc.png"); got != "https://assets.wuzz.care/outlets/2026/10/abc.png" {
		t.Errorf("URL = %q", got)
	}
	if err := s.Delete(context.Background(), "outlets/2026/10/abc.png"); err != nil || !deleted {
		t.Errorf("Delete: err=%v deleted=%v", err, deleted)
	}
	if err := s.Put(context.Background(), "../escape.png", "image/png", "", nil); err == nil {
		t.Error("a key that climbs out of the bucket must be refused")
	}
}

func TestS3Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>SignatureDoesNotMatch</Code></Error>"))
	}))
	defer srv.Close()
	s := &S3{Endpoint: srv.URL, Bucket: "b", AccessKeyID: "id", SecretAccessKey: "secret", Region: "auto", PublicBaseURL: "https://x"}
	err := s.Put(context.Background(), "outlets/a.png", "image/png", "", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "secret") {
		t.Errorf("err = %v: want the status, never the secret", err)
	}
}

func TestLocal(t *testing.T) {
	dir := t.TempDir()
	l := &Local{Dir: dir, PublicBaseURL: "http://localhost:8092/media"}
	key := "outlets/2026/10/abc.png"
	if err := l.Put(context.Background(), key, "image/png", "", []byte("png")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "outlets", "2026", "10", "abc.png")); err != nil || string(b) != "png" {
		t.Fatalf("file: %q %v", b, err)
	}
	h := l.Handler("/media")
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	rec := get("/media/" + key)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Cache-Control") != CacheControl || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Body.String() != "png" {
		t.Errorf("serve: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	for _, p := range []string{"/media/outlets/2026/10/", "/media/outlets", "/media/../secret.png", "/media/outlets/2026/10/missing.png", "/media/outlets/2026/10/abc.txt"} {
		if c := get(p).Code; c != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, c)
		}
	}
	if err := l.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(context.Background(), key); err != nil {
		t.Errorf("deleting a key that is gone should not fail: %v", err)
	}
	if c := get("/media/" + key).Code; c != http.StatusNotFound {
		t.Errorf("after delete: %d", c)
	}
}
