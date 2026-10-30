package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	pgplat "github.com/koobam/wuzz-backend/internal/platform/postgres"
	"github.com/koobam/wuzz-backend/internal/services/user/domain"
)

// These need a user database with the migrations applied: set USER_TEST_DSN. They are skipped otherwise.
func testPool(t *testing.T) *pgplat.Pool {
	t.Helper()
	dsn := os.Getenv("USER_TEST_DSN")
	if dsn == "" {
		t.Skip("USER_TEST_DSN not set")
	}
	pool, err := pgplat.New(context.Background(), config.Postgres{DSN: dsn, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// phone makes a unique valid phone number for a test row.
func phone(n int) string { return fmt.Sprintf("6299%d%03d", time.Now().UnixNano()%100000000, n) }

func TestAccountListByOutletStatusQueryAndSort(t *testing.T) {
	pool, ctx := testPool(t), context.Background()
	acc := NewAccounts(pool)
	tag := fmt.Sprintf("Zq%d", time.Now().UnixNano()%1000000)
	rows := []struct {
		name   string
		outlet any
		status string
	}{
		{tag + " Cici", 9101, "active"},
		{tag + " Ali", 9101, "inactive"},
		{tag + " Budi", 9102, "active"},
		{tag + " Dewi", nil, "active"},
	}
	var ids []int64
	for i, r := range rows {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO staff (name, phone, role, outlet_id, status) VALUES ($1, $2, 'front', $3, $4) RETURNING id`,
			r.name, phone(i), r.outlet, r.status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM staff WHERE id = ANY($1)`, ids) })
	// One admin on outlet 9101 too, to show the two tables are separate.
	var adminID int64
	if err := pool.QueryRow(ctx, `INSERT INTO admins (name, phone, role, outlet_id) VALUES ($1, $2, 'support', 9101) RETURNING id`, tag+" Admin", phone(99)).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM admins WHERE id = $1`, adminID) })

	list := func(kind string, f domain.ListFilter) ([]string, int64) {
		t.Helper()
		f.Query, f.Limit = tag+f.Query, 50
		if f.SortBy == "" {
			f.SortBy, f.SortDir = "id", "desc"
		}
		got, total, err := acc.List(ctx, kind, f)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, a := range got {
			names = append(names, strings.TrimPrefix(a.Name, tag+" "))
		}
		return names, total
	}
	join := func(s []string) string { return strings.Join(s, ",") }

	// Today's default: newest first.
	if got, total := list(domain.KindStaff, domain.ListFilter{}); join(got) != "Dewi,Budi,Ali,Cici" || total != 4 {
		t.Errorf("default = %v (total %d); want newest first", got, total)
	}
	// No outlet (0) is no filter; the filter keeps only that outlet's, and the total follows it.
	if got, total := list(domain.KindStaff, domain.ListFilter{OutletID: 9101}); total != 2 || len(got) != 2 {
		t.Errorf("outlet 9101 = %v (total %d)", got, total)
	}
	if got, total := list(domain.KindStaff, domain.ListFilter{OutletID: 9102}); join(got) != "Budi" || total != 1 {
		t.Errorf("outlet 9102 = %v (total %d)", got, total)
	}
	if got, total := list(domain.KindStaff, domain.ListFilter{OutletID: 987654}); len(got) != 0 || total != 0 {
		t.Errorf("an outlet with nobody = %v (total %d)", got, total)
	}
	// Combined with the status and the search text.
	if got, total := list(domain.KindStaff, domain.ListFilter{OutletID: 9101, Status: "active"}); join(got) != "Cici" || total != 1 {
		t.Errorf("outlet + status = %v (total %d)", got, total)
	}
	if got, total := list(domain.KindStaff, domain.ListFilter{OutletID: 9101, Query: " ali"}); join(got) != "Ali" || total != 1 {
		t.Errorf("outlet + query = %v (total %d)", got, total)
	}
	// Admins are their own table.
	if got, total := list(domain.KindAdmin, domain.ListFilter{OutletID: 9101}); join(got) != "Admin" || total != 1 {
		t.Errorf("admins on 9101 = %v (total %d)", got, total)
	}
	if got, total := list(domain.KindAdmin, domain.ListFilter{OutletID: 9102}); len(got) != 0 || total != 0 {
		t.Errorf("admins on 9102 = %v (total %d)", got, total)
	}
	// Sorting, ignoring case, with the direction and a page.
	if got, _ := list(domain.KindStaff, domain.ListFilter{SortBy: "name", SortDir: "asc"}); join(got) != "Ali,Budi,Cici,Dewi" {
		t.Errorf("name asc = %v", got)
	}
	if got, _ := list(domain.KindStaff, domain.ListFilter{SortBy: "name", SortDir: "desc"}); join(got) != "Dewi,Cici,Budi,Ali" {
		t.Errorf("name desc = %v", got)
	}
	if got, _ := list(domain.KindStaff, domain.ListFilter{SortBy: "id", SortDir: "asc"}); join(got) != "Cici,Ali,Budi,Dewi" {
		t.Errorf("id asc = %v", got)
	}
	if got, _ := list(domain.KindStaff, domain.ListFilter{SortBy: "created_at", SortDir: "asc"}); len(got) != 4 {
		t.Errorf("created_at asc = %v", got)
	}
	page, total, err := acc.List(ctx, domain.KindStaff, domain.ListFilter{Query: tag, SortBy: "name", SortDir: "asc", Limit: 2, Offset: 2})
	if err != nil || total != 4 || len(page) != 2 || !strings.HasSuffix(page[0].Name, "Cici") {
		t.Errorf("second page = %v (total %d), %v", page, total, err)
	}
}

func TestUserListSort(t *testing.T) {
	pool, ctx := testPool(t), context.Background()
	users := NewUsers(pool)
	tag := fmt.Sprintf("zq%d", time.Now().UnixNano()%100000)
	var ids []int64
	for i, u := range []struct {
		name   string
		points int64
	}{{"Cici", 50}, {"Ali", 900}, {"Budi", 50}} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, name, phone, points) VALUES ($1, $2, $3, $4) RETURNING id`,
			fmt.Sprintf("%s%d", tag, i), tag+" "+u.name, phone(i), u.points).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, ids) })
	order := func(by, dir string) string {
		t.Helper()
		got, total, err := users.List(ctx, domain.ListFilter{Query: tag, SortBy: by, SortDir: dir, Limit: 50})
		if err != nil || total != 3 {
			t.Fatalf("%s %s: total %d, %v", by, dir, total, err)
		}
		var n []string
		for _, u := range got {
			n = append(n, strings.TrimPrefix(u.Name, tag+" "))
		}
		return strings.Join(n, ",")
	}
	if got := order("id", "desc"); got != "Budi,Ali,Cici" {
		t.Errorf("id desc = %s", got)
	}
	if got := order("name", "asc"); got != "Ali,Budi,Cici" {
		t.Errorf("name asc = %s", got)
	}
	if got := order("points", "desc"); got != "Ali,Cici,Budi" { // 900, then the tie of 50s broken by id: Cici (older) first
		t.Errorf("points desc = %s", got)
	}
	if got := order("points", "asc"); got != "Cici,Budi,Ali" {
		t.Errorf("points asc = %s", got)
	}
	if got := order("created_at", "asc"); got != "Cici,Ali,Budi" {
		t.Errorf("created_at asc = %s", got)
	}
}
