package activity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

type memSink struct {
	mu      sync.Mutex
	entries []Entry
	fail    int // fail this many Insert calls first
	calls   int
}

func (m *memSink) Insert(_ context.Context, es []Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.fail > 0 {
		m.fail--
		return errors.New("db down")
	}
	m.entries = append(m.entries, es...)
	return nil
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRecorder(sink Sink) *Recorder {
	r := NewRecorder(sink, quietLog())
	r.retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	return r
}

type call struct {
	name, method, path string // path as requested, under the audience prefix
	route              string // the registered pattern, under the audience prefix
	body               string
	status             int
	answer             string // the "data" the handler answers with
	signedIn           bool
	fails              bool // the handler fails through httpx.Fail, so the engine's ErrorHandler writes the answer after the middleware

	wantLogged         bool
	action, entity, id string
	outcome            string
	wantParams         map[string]any
	wantActorID        string
	wantChanges        string // JSON the entry's changes must equal; "" means none
	mustNotContain     []string
}

func TestRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []call{
		{name: "create a service", method: "POST", path: "/services", route: "/services", body: `{"name":"Cuci Kiloan","price":7000}`, status: 201, answer: `{"id":12,"name":"Cuci Kiloan"}`, signedIn: true,
			wantLogged: true, action: "create", entity: "service", id: "12", outcome: "ok", wantParams: map[string]any{"name": "Cuci Kiloan"}, wantActorID: "u1", wantChanges: `{"name":"Cuci Kiloan","price":7000}`},
		{name: "an upload is logged by its key and file, never its bytes", method: "POST", path: "/uploads", route: "/uploads", body: "--b\r\nbinary-picture-bytes\r\n--b--", status: 201,
			answer: `{"url":"https://assets.test/outlets/2026/10/ab.png","key":"outlets/2026/10/ab.png","file_name":"pusat.png","content_type":"image/png","size":1234,"width":10,"height":10}`, signedIn: true,
			wantLogged: true, action: "create", entity: "media", id: "outlets/2026/10/ab.png", outcome: "ok", wantActorID: "u1",
			wantParams: map[string]any{"file_name": "pusat.png", "content_type": "image/png", "size": float64(1234)}, mustNotContain: []string{"binary-picture-bytes"}},
		{name: "update takes the id from the path", method: "PATCH", path: "/outlets/7", route: "/outlets/:id", body: `{"name":"Blok M"}`, status: 200, answer: `{"id":7,"name":"Blok M"}`, signedIn: true,
			wantLogged: true, action: "update", entity: "outlet", id: "7", outcome: "ok", wantParams: map[string]any{"name": "Blok M"}, wantActorID: "u1", wantChanges: `{"name":"Blok M"}`},
		{name: "delete", method: "DELETE", path: "/variant-groups/3", route: "/variant-groups/:id", status: 200, signedIn: true,
			wantLogged: true, action: "delete", entity: "variant_group", id: "3", outcome: "ok", wantParams: map[string]any{}, wantActorID: "u1"},
		{name: "a bom is named by owner and id", method: "PUT", path: "/boms/service/12", route: "/boms/:owner_type/:owner_id", body: `{"lines":[{"material_id":1,"qty":"0.5000"}]}`, status: 200, signedIn: true,
			wantLogged: true, action: "update", entity: "bom", id: "service:12", outcome: "ok", wantParams: map[string]any{}, wantActorID: "u1", wantChanges: `{"lines":[{"material_id":1,"qty":"0.5000"}]}`},
		{name: "stock waste is about the material", method: "POST", path: "/stock/waste", route: "/stock/waste", body: `{"outlet_id":3,"material_id":9,"qty":"10.5","reason":"spilled","idempotency_key":"k1"}`, status: 201, signedIn: true,
			wantLogged: true, action: "waste", entity: "stock", id: "9", outcome: "ok", wantParams: map[string]any{"outlet_id": float64(3), "qty": "10.5", "reason": "spilled"}, wantActorID: "u1",
			wantChanges: `{"idempotency_key":"k1","material_id":9,"outlet_id":3,"qty":"10.5","reason":"spilled"}`},
		{name: "an order status keeps the code from the answer", method: "PATCH", path: "/orders/abc/status", route: "/orders/:id/status", body: `{"status":"ready"}`, status: 200, answer: `{"id":"abc","code":"TRX-25102026-K7M2","status":"ready"}`, signedIn: true,
			wantLogged: true, action: "status", entity: "order", id: "abc", outcome: "ok", wantParams: map[string]any{"code": "TRX-25102026-K7M2", "status": "ready"}, wantActorID: "u1", wantChanges: `{"status":"ready"}`},
		{name: "a refused request is logged as failed", method: "PUT", path: "/materials/5", route: "/materials/:id", body: `{"name":"x"}`, status: 400, signedIn: true,
			wantLogged: true, action: "update", entity: "material", id: "5", outcome: "failed", wantParams: map[string]any{"name": "x"}, wantActorID: "u1", wantChanges: `{"name":"x"}`},
		{name: "a failure written later by ErrorHandler is still a failure", method: "POST", path: "/stock/receipts", route: "/stock/receipts", body: `{"outlet_id":1,"material_id":4,"qty":"1"}`, status: 400, signedIn: true, fails: true,
			wantLogged: true, action: "receive", entity: "stock", id: "4", outcome: "failed", wantParams: map[string]any{"outlet_id": float64(1), "qty": "1"}, wantActorID: "u1",
			wantChanges: `{"material_id":4,"outlet_id":1,"qty":"1"}`},
		{name: "a forbidden one too", method: "POST", path: "/stock/adjustments", route: "/stock/adjustments", body: `{"outlet_id":1,"material_id":2,"counted_qty":"3","note":"n"}`, status: 403, signedIn: true,
			wantLogged: true, action: "adjust", entity: "stock", id: "2", outcome: "failed", wantParams: map[string]any{"outlet_id": float64(1), "counted_qty": "3"}, wantActorID: "u1",
			wantChanges: `{"counted_qty":"3","material_id":2,"note":"n","outlet_id":1}`},
		{name: "secrets never reach the log", method: "POST", path: "/materials", route: "/materials", body: `{"name":"x","api_token":"abc","nested":{"password":"p","otp_code":"123456","pin":"1"},"shipping":"ok"}`, status: 201, answer: `{"id":1}`, signedIn: true,
			wantLogged: true, action: "create", entity: "material", id: "1", outcome: "ok", wantParams: map[string]any{"name": "x"}, wantActorID: "u1",
			wantChanges: `{"api_token":"[redacted]","name":"x","nested":{"otp_code":"[redacted]","password":"[redacted]","pin":"[redacted]"},"shipping":"ok"}`, mustNotContain: []string{"abc", "123456"}},
		{name: "sign-in is named by the answer and keeps no body", method: "POST", path: "/auth/verify-otp", route: "/auth/verify-otp", body: `{"phone":"0812","otp_code":"000000"}`, status: 200,
			answer:     `{"user":{"id":"u9","name":"Admin","phone":"0812","roles":["admin"]},"access_token":"tok","refresh_token":"ref"}`,
			wantLogged: true, action: "sign_in", entity: "session", id: "", outcome: "ok", wantParams: map[string]any{}, wantActorID: "u9", mustNotContain: []string{"tok", "ref", "0812", "000000"}},
		{name: "a failed sign-in is kept without a name", method: "POST", path: "/auth/verify-otp", route: "/auth/verify-otp", body: `{"phone":"0812","otp_code":"111111"}`, status: 401,
			wantLogged: true, action: "sign_in", entity: "session", outcome: "failed", wantParams: map[string]any{}, wantActorID: "", mustNotContain: []string{"0812", "111111"}},
		{name: "a refused mutation by nobody is not logged", method: "POST", path: "/services", route: "/services", body: `{"name":"x"}`, status: 401, signedIn: false, wantLogged: false},
		{name: "a read is not logged", method: "GET", path: "/services", route: "/services", status: 200, signedIn: true, wantLogged: false},
		{name: "a route outside the table is not logged", method: "POST", path: "/presence/ping", route: "/presence/ping", status: 200, signedIn: true, wantLogged: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &memSink{}
			rec := newRecorder(sink)
			engine := gin.New()
			engine.Use(httpx.ErrorHandler())
			group := engine.Group("/backoffice/v1")
			group.Use(Record(rec, nil))
			group.Use(func(c *gin.Context) { // stands in for Authenticate
				if tc.signedIn {
					c.Request = c.Request.WithContext(authn.WithIdentity(c.Request.Context(), authn.Identity{UserID: "u1", Name: "Ani", Roles: []string{"admin"}}))
				}
				c.Next()
			})
			group.Handle(tc.method, tc.route, func(c *gin.Context) {
				// The handler must still be able to read the body the middleware looked at.
				if tc.body != "" && tc.name != "sign-in is named by the answer and keeps no body" {
					got, _ := io.ReadAll(c.Request.Body)
					if string(got) != tc.body {
						t.Errorf("handler read %q, want %q", got, tc.body)
					}
				}
				if tc.fails {
					httpx.Fail(c, apperr.Invalid("nope"))
					return
				}
				data := json.RawMessage("null")
				if tc.answer != "" {
					data = json.RawMessage(tc.answer)
				}
				c.JSON(tc.status, gin.H{"success": tc.status < 400, "message": "m", "data": data})
			})

			var reqBody io.Reader
			if tc.body != "" {
				reqBody = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, "/backoffice/v1"+tc.path, reqBody)
			req.Header.Set("X-Forwarded-For", "10.0.0.1")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if err := rec.Close(context.Background()); err != nil {
				t.Fatal(err)
			}

			if !tc.wantLogged {
				if len(sink.entries) != 0 {
					t.Fatalf("logged %d entries, want none: %+v", len(sink.entries), sink.entries)
				}
				return
			}
			if len(sink.entries) != 1 {
				t.Fatalf("logged %d entries, want 1", len(sink.entries))
			}
			e := sink.entries[0]
			if e.Audience != "backoffice" || e.Action != tc.action || e.EntityType != tc.entity || e.EntityID != tc.id || e.Outcome != tc.outcome || e.Status != tc.status {
				t.Errorf("entry = audience %q action %q entity %q/%q outcome %q status %d", e.Audience, e.Action, e.EntityType, e.EntityID, e.Outcome, e.Status)
			}
			if e.Actor.ID != tc.wantActorID {
				t.Errorf("actor = %q, want %q", e.Actor.ID, tc.wantActorID)
			}
			if wantKey := "activity." + tc.entity + "." + tc.action; e.Summary.Key != wantKey {
				t.Errorf("summary key = %q, want %q", e.Summary.Key, wantKey)
			}
			gotParams, _ := json.Marshal(e.Summary.Params)
			wantParams, _ := json.Marshal(tc.wantParams)
			if string(gotParams) != string(wantParams) {
				t.Errorf("summary params = %s, want %s", gotParams, wantParams)
			}
			if tc.wantChanges == "" {
				if len(e.Changes) != 0 {
					t.Errorf("changes = %s, want none", e.Changes)
				}
			} else if string(e.Changes) != tc.wantChanges {
				t.Errorf("changes = %s, want %s", e.Changes, tc.wantChanges)
			}
			whole, _ := json.Marshal(e)
			for _, secret := range tc.mustNotContain {
				if strings.Contains(string(whole), secret) {
					t.Errorf("the entry contains %q: %s", secret, whole)
				}
			}
		})
	}
}

func TestRedactCutsLongStringsAndKeepsNumbersExact(t *testing.T) {
	long := strings.Repeat("a", 1000)
	got := string(redact([]byte(`{"note":"` + long + `","price":12345678901234567890.5,"list":["` + long + `"]}`)))
	if strings.Contains(got, long) {
		t.Error("a long string was kept whole")
	}
	if !strings.Contains(got, "12345678901234567890.5") {
		t.Errorf("a number lost digits: %s", got)
	}
	if redact([]byte(`not json`)) != nil || redact(nil) != nil {
		t.Error("a body that is not JSON must give nothing")
	}
}

func TestRecorderRetriesThenGivesUpLoudly(t *testing.T) {
	// Fails twice, then works: nothing lost.
	flaky := &memSink{fail: 2}
	r := newRecorder(flaky)
	r.Record(Entry{Action: "create"})
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(flaky.entries) != 1 || flaky.calls != 3 {
		t.Errorf("stored %d entries in %d calls, want 1 in 3", len(flaky.entries), flaky.calls)
	}

	// Always fails: the entry is logged at ERROR with its content, and counted.
	var logged strings.Builder
	down := &memSink{fail: 1000}
	r2 := NewRecorder(down, slog.New(slog.NewTextHandler(&logged, nil)))
	r2.retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	r2.Record(Entry{Action: "delete", EntityType: "material", EntityID: "77"})
	if err := r2.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logged.String(), "ACTIVITY LOG ENTRY LOST") || !strings.Contains(logged.String(), "material") || !strings.Contains(logged.String(), "77") {
		t.Errorf("a lost entry must be logged with its content, got: %s", logged.String())
	}
}

func TestRecorderNeverBlocksAndLogsWhenFull(t *testing.T) {
	var logged strings.Builder
	block := make(chan struct{})
	r := NewRecorder(blockingSink(block), slog.New(slog.NewTextHandler(&logged, nil)))
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueSize*3; i++ {
			r.Record(Entry{Action: "create"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked the request when the queue was full")
	}
	if !strings.Contains(logged.String(), "queue_full") {
		t.Error("an entry dropped for a full queue must be logged")
	}
	close(block)
	_ = r.Close(context.Background())
}

type blockingSink chan struct{}

func (b blockingSink) Insert(context.Context, []Entry) error { <-b; return nil }

func TestFilterBuildsParameterisedSQL(t *testing.T) {
	cases := []struct {
		name     string
		f        Filter
		wantSQL  string
		wantArgs []any
	}{
		{"nothing", Filter{}, "", nil},
		{"entity", Filter{EntityType: "service", EntityID: "12"}, " WHERE entity_type = $1 AND entity_id = $2", []any{"service", "12"}},
		{"actor action outcome", Filter{ActorID: "u1", Action: "delete", Outcome: "failed"}, " WHERE actor_id = $1 AND action = $2 AND outcome = $3", []any{"u1", "delete", "failed"}},
		{"days are Jakarta days", Filter{From: "2026-10-01", To: "2026-10-25"},
			" WHERE created_at >= ($1::date)::timestamp AT TIME ZONE 'Asia/Jakarta' AND created_at < (($2::date) + 1)::timestamp AT TIME ZONE 'Asia/Jakarta'", []any{"2026-10-01", "2026-10-25"}},
		{"search escapes wildcards", Filter{Query: " 50%_off "}, " WHERE (actor_name ILIKE $1 OR entity_id ILIKE $1 OR summary_params::text ILIKE $1)", []any{`%50\%\_off%`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, args := c.f.where()
			if sql != c.wantSQL {
				t.Errorf("sql = %q\nwant  %q", sql, c.wantSQL)
			}
			if len(args) != len(c.wantArgs) {
				t.Fatalf("args = %v, want %v", args, c.wantArgs)
			}
			for i := range args {
				if args[i] != c.wantArgs[i] {
					t.Errorf("arg %d = %v, want %v", i, args[i], c.wantArgs[i])
				}
			}
		})
	}
}

func TestOrderByOnlyAllowsKnownColumns(t *testing.T) {
	cases := map[string]Filter{
		" ORDER BY created_at DESC, id DESC": {},
		" ORDER BY actor_name ASC, id ASC":   {SortBy: "actor_name"},
		" ORDER BY action DESC, id DESC":     {SortBy: "action", SortDesc: true},
		// Anything else, including an injection attempt, falls back to newest first.
		" ORDER BY created_at DESC, id DESC ": {SortBy: "id; DROP TABLE activity_log"},
	}
	for want, f := range cases {
		if got := f.orderBy(); strings.TrimSpace(got) != strings.TrimSpace(want) {
			t.Errorf("orderBy(%+v) = %q, want %q", f, got, want)
		}
	}
}

type fakeReader struct {
	got   Filter
	items []Entry
}

func (f *fakeReader) List(_ context.Context, fl Filter) ([]Entry, int, error) {
	f.got = fl
	return f.items, len(f.items), nil
}

func TestListEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reader := &fakeReader{items: []Entry{{ID: 1, Action: "create", EntityType: "service", EntityID: "12", Actor: Actor{ID: "u1", Name: "Ani", Roles: []string{"admin"}}, Summary: Summary{Key: "activity.service.create", Params: map[string]any{"name": "Cuci"}}, Outcome: "ok", Status: 201}}}
	engine := gin.New()
	engine.Use(httpx.ErrorHandler())
	engine.GET("/activity", (&Handler{Log: reader}).List)

	get := func(q string) (int, map[string]any) {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/activity"+q, nil))
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}

	code, body := get("?entity_type=service&entity_id=12&sort_by=action&sort_dir=asc&limit=500&offset=-4&from=2026-10-01&to=2026-10-25&q=cuci")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	data := body["data"].(map[string]any)
	if data["total"].(float64) != 1 || len(data["items"].([]any)) != 1 {
		t.Errorf("data = %v", data)
	}
	want := Filter{EntityType: "service", EntityID: "12", SortBy: "action", SortDesc: false, Limit: defaultLimit, Offset: 0, From: "2026-10-01", To: "2026-10-25", Query: "cuci"}
	if reader.got != want {
		t.Errorf("filter = %+v\nwant     %+v", reader.got, want)
	}

	if code, _ := get("?from=yesterday"); code != 400 {
		t.Errorf("a bad day gave %d, want 400", code)
	}
}
