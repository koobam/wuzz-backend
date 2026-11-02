package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/config"
	pgplat "github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/media/domain"
)

// The store against a real database. Set MEDIA_TEST_DSN to a migrated wuzz_media database to run it; it is skipped otherwise.
func TestStore(t *testing.T) {
	dsn := os.Getenv("MEDIA_TEST_DSN")
	if dsn == "" {
		t.Skip("MEDIA_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgplat.New(ctx, config.Postgres{DSN: dsn, MaxConns: 2, MinConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := NewStore(pool)

	stamp := time.Now().UnixNano()
	insert := func(folder string, n int) domain.Media {
		t.Helper()
		key := fmt.Sprintf("%s/2026/11/test%d%d.webp", folder, stamp, n)
		m, err := s.Insert(ctx, domain.Media{Key: key, URL: "https://assets.test/" + key, Folder: folder, FileName: "a.webp",
			ContentType: "image/webp", Size: 1234, Width: 64, Height: 64, UploadedBy: "admin-1"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM media WHERE object_key = $1`, key) })
		return m
	}
	a, b := insert("services", 1), insert("banners", 2)
	if a.ID == "" || a.CreatedAt.IsZero() || a.Width != 64 {
		t.Fatalf("insert: %+v", a)
	}

	got, err := s.Get(ctx, a.ID)
	if err != nil || got.Key != a.Key {
		t.Fatalf("get: %+v %v", got, err)
	}
	both, err := s.BatchGet(ctx, []string{a.ID, b.ID, "0b0b0b0b-0000-4000-8000-000000000000"})
	if err != nil || len(both) != 2 {
		t.Fatalf("batch: %d %v", len(both), err)
	}
	page, total, err := s.List(ctx, domain.ListQuery{Folder: "services", Limit: 50})
	if err != nil || total < 1 {
		t.Fatalf("list: %d %v", total, err)
	}
	for _, m := range page {
		if m.Folder != "services" {
			t.Fatalf("the folder filter let %q through", m.Folder)
		}
	}

	gone, err := s.Delete(ctx, a.ID)
	if err != nil || gone.Key != a.Key {
		t.Fatalf("delete: %+v %v", gone, err)
	}
	var ae *apperr.Error
	if _, err := s.Get(ctx, a.ID); !errors.As(err, &ae) || ae.Kind != apperr.KindNotFound {
		t.Fatalf("a deleted picture is not found, got %v", err)
	}
	if _, err := s.Delete(ctx, a.ID); !errors.As(err, &ae) || ae.Kind != apperr.KindNotFound {
		t.Fatalf("deleting twice is not found, got %v", err)
	}
}
