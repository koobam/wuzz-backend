package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/services/media/domain"
)

type fakeStore struct {
	objects map[string][]byte
	putErr  error
	deleted []string
}

func (f *fakeStore) Put(_ context.Context, key, _, _ string, body []byte) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.objects[key] = body
	return nil
}
func (f *fakeStore) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeStore) URL(key string) string { return "https://assets.test/" + key }

type fakeRepo struct {
	rows      map[string]domain.Media
	insertErr error
}

func (r *fakeRepo) Insert(_ context.Context, m domain.Media) (domain.Media, error) {
	if r.insertErr != nil {
		return domain.Media{}, r.insertErr
	}
	m.ID = "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	r.rows[m.ID] = m
	return m, nil
}
func (r *fakeRepo) Get(_ context.Context, id string) (domain.Media, error) {
	m, ok := r.rows[id]
	if !ok {
		return m, apperr.NotFound("Media not found")
	}
	return m, nil
}
func (r *fakeRepo) BatchGet(context.Context, []string) ([]domain.Media, error) { return nil, nil }
func (r *fakeRepo) List(context.Context, domain.ListQuery) ([]domain.Media, int64, error) {
	return nil, 0, nil
}
func (r *fakeRepo) Delete(_ context.Context, id string) (domain.Media, error) {
	m, ok := r.rows[id]
	if !ok {
		return m, apperr.NotFound("Media not found")
	}
	delete(r.rows, id)
	return m, nil
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newApp() (*App, *fakeRepo, *fakeStore) {
	repo := &fakeRepo{rows: map[string]domain.Media{}}
	store := &fakeStore{objects: map[string][]byte{}}
	return New(repo, store, 1<<20, nil), repo, store
}

func TestUploadStoresAndRecords(t *testing.T) {
	a, repo, store := newApp()
	m, err := a.Upload(context.Background(), Upload{Data: pngBytes(t), FileName: "../../etc/kiloan.png", Folder: "services", UploadedBy: "admin-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Key, "services/") || store.objects[m.Key] == nil {
		t.Fatalf("the object must be stored under the folder, got key %q", m.Key)
	}
	if m.URL != "https://assets.test/"+m.Key || m.FileName != "kiloan.png" || m.Width != 4 || m.Height != 3 {
		t.Fatalf("unexpected record %+v", m)
	}
	if _, ok := repo.rows[m.ID]; !ok {
		t.Fatal("the upload must be recorded")
	}
}

func TestUploadTakesTheObjectBackWhenTheRecordFails(t *testing.T) {
	a, repo, store := newApp()
	repo.insertErr = errors.New("db down")
	if _, err := a.Upload(context.Background(), Upload{Data: pngBytes(t), Folder: "banners"}); err == nil {
		t.Fatal("expected the insert error")
	}
	if len(store.objects) != 0 || len(store.deleted) != 1 {
		t.Fatalf("the stored object must be removed again, objects=%d deleted=%v", len(store.objects), store.deleted)
	}
}

func TestUploadRefuses(t *testing.T) {
	a, _, store := newApp()
	if _, err := a.Upload(context.Background(), Upload{Data: pngBytes(t), Folder: "secrets"}); !isKind(err, apperr.KindInvalid) {
		t.Fatalf("an unknown folder is invalid, got %v", err)
	}
	if _, err := a.Upload(context.Background(), Upload{Data: []byte("not a picture at all")}); err == nil {
		t.Fatal("bytes that are not a picture must be refused")
	}
	if len(store.objects) != 0 {
		t.Fatal("nothing refused may be stored")
	}
	none := New(&fakeRepo{rows: map[string]domain.Media{}}, nil, 0, nil)
	if _, err := none.Upload(context.Background(), Upload{Data: pngBytes(t)}); !isKind(err, apperr.KindUnavailable) {
		t.Fatalf("no storage means unavailable, got %v", err)
	}
}

func TestDeleteRemovesRowThenObject(t *testing.T) {
	a, repo, store := newApp()
	m, err := a.Upload(context.Background(), Upload{Data: pngBytes(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(context.Background(), strings.ToUpper(m.ID)); err != nil {
		t.Fatal(err)
	}
	if len(repo.rows) != 0 || len(store.objects) != 0 {
		t.Fatal("both the row and the object must be gone")
	}
	if err := a.Delete(context.Background(), "nope"); !isKind(err, apperr.KindInvalid) {
		t.Fatalf("a malformed id is invalid, got %v", err)
	}
}

func isKind(err error, kind apperr.Kind) bool {
	var ae *apperr.Error
	return errors.As(err, &ae) && ae.Kind == kind
}
