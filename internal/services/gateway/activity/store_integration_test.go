package activity

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStoreAgainstPostgres needs the gateway database migrated: set ACTIVITY_TEST_DSN, for example
// postgres://wuzz:wuzz@localhost:5433/wuzz_gateway?sslmode=disable. Skipped otherwise. It leaves its rows behind (the log is append-only).
func TestStoreAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("ACTIVITY_TEST_DSN")
	if dsn == "" {
		t.Skip("ACTIVITY_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &Store{Pool: pool}

	marker := "it-" + time.Now().Format("150405.000000")
	now := time.Now().UTC()
	entries := []Entry{
		{CreatedAt: now.Add(-2 * time.Second), Audience: "backoffice", Actor: Actor{ID: marker, Name: "Ani", Roles: []string{"admin"}}, Action: "create", EntityType: "service", EntityID: "12",
			Summary: Summary{Key: "activity.service.create", Params: map[string]any{"name": "Cuci Kiloan"}}, Changes: []byte(`{"name":"Cuci Kiloan"}`), Outcome: "ok", Status: 201, RequestID: "r1", IP: "10.0.0.1"},
		{CreatedAt: now.Add(-1 * time.Second), Audience: "pos", Actor: Actor{ID: marker, Name: "Ani"}, Action: "delete", EntityType: "service", EntityID: "12",
			Summary: Summary{Key: "activity.service.delete"}, Outcome: "failed", Status: 403},
	}
	if err := store.Insert(ctx, entries); err != nil {
		t.Fatal(err)
	}

	items, total, err := store.List(ctx, Filter{ActorID: marker, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 || items[0].Action != "delete" || items[1].Action != "create" {
		t.Fatalf("newest first: total %d items %+v", total, items)
	}
	if string(items[1].Changes) != `{"name": "Cuci Kiloan"}` && string(items[1].Changes) != `{"name":"Cuci Kiloan"}` {
		t.Errorf("changes = %s", items[1].Changes)
	}
	if items[1].Summary.Params["name"] != "Cuci Kiloan" || items[0].Changes != nil || items[0].Actor.Roles == nil {
		t.Errorf("round trip lost data: %+v", items)
	}

	for name, f := range map[string]Filter{
		"by outcome":  {ActorID: marker, Outcome: "failed"},
		"by action":   {ActorID: marker, Action: "create"},
		"by audience": {ActorID: marker, Audience: "pos"},
		"by search":   {ActorID: marker, Query: "kiloan"},
		"by day":      {ActorID: marker, From: now.In(jakartaLoc(t)).Format("2006-01-02"), To: now.In(jakartaLoc(t)).Format("2006-01-02")},
	} {
		f.Limit = 10
		if _, n, err := store.List(ctx, f); err != nil || n < 1 {
			t.Errorf("%s: total %d err %v", name, n, err)
		}
	}
	if _, n, _ := store.List(ctx, Filter{ActorID: marker, Query: "no such thing", Limit: 10}); n != 0 {
		t.Errorf("a search that matches nothing found %d", n)
	}
	page, _, _ := store.List(ctx, Filter{ActorID: marker, SortBy: "action", Limit: 1, Offset: 1})
	if len(page) != 1 {
		t.Errorf("paging gave %d rows", len(page))
	}

	// Append-only: the database itself refuses a change.
	if _, err := pool.Exec(ctx, "UPDATE activity_log SET action = 'x' WHERE actor_id = $1", marker); err == nil {
		t.Error("UPDATE succeeded on an append-only table")
	}
	if _, err := pool.Exec(ctx, "DELETE FROM activity_log WHERE actor_id = $1", marker); err == nil {
		t.Error("DELETE succeeded on an append-only table")
	}
}

func jakartaLoc(t *testing.T) *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skip("no tzdata")
	}
	return loc
}
