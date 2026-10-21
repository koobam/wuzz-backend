package errtrack

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

// memTransport keeps the events the client would have sent.
type memTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *memTransport) Configure(sentry.ClientOptions) {}
func (t *memTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}
func (t *memTransport) Flush(time.Duration) bool              { return true }
func (t *memTransport) FlushWithContext(context.Context) bool { return true }
func (t *memTransport) Close()                                {}

func newClient(t *testing.T) *memTransport {
	t.Helper()
	tr := &memTransport{}
	if err := sentry.Init(sentry.ClientOptions{Dsn: "https://k@example.invalid/1", Transport: tr, Environment: "test"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return tr
}

func TestErrorsAreReportedAndStillLogged(t *testing.T) {
	tr := newClient(t)
	var out bytes.Buffer
	log := Wrap(slog.New(slog.NewTextHandler(&out, nil)))

	log.Error("could not search outlets", slog.Any("error", errors.New("connection refused")), slog.String("outlet", "7"))

	if len(tr.events) != 1 {
		t.Fatalf("got %d events, want 1", len(tr.events))
	}
	ev := tr.events[0]
	if len(ev.Exception) == 0 || ev.Exception[0].Value != "connection refused" {
		t.Errorf("exception = %+v; want the logged error", ev.Exception)
	}
	if ev.Level != sentry.LevelError {
		t.Errorf("level = %v", ev.Level)
	}
	logCtx := ev.Contexts["log"]
	if logCtx["message"] != "could not search outlets" {
		t.Errorf("log context = %+v", logCtx)
	}
	if attrs, _ := logCtx["attributes"].(map[string]any); attrs["outlet"] != "7" {
		t.Errorf("attributes = %+v; want outlet=7", logCtx["attributes"])
	}
	if !bytes.Contains(out.Bytes(), []byte("could not search outlets")) {
		t.Error("the record should still reach the wrapped logger")
	}
}

func TestAnErrorWithoutAnErrorValueUsesTheMessage(t *testing.T) {
	tr := newClient(t)
	Wrap(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))).Error("panic recovered", slog.Any("panic", "boom"))
	if len(tr.events) != 1 || tr.events[0].Exception[0].Value != "panic recovered" {
		t.Fatalf("events = %+v", tr.events)
	}
}

func TestBelowErrorIsNotReported(t *testing.T) {
	tr := newClient(t)
	log := Wrap(slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.Debug("d")
	log.Info("i")
	log.Warn("w")
	if len(tr.events) != 0 {
		t.Errorf("got %d events for debug/info/warn, want 0", len(tr.events))
	}
}

func TestAttributesFromWithAreKept(t *testing.T) {
	tr := newClient(t)
	log := Wrap(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))).With(slog.String("service", "wuzz-catalog"))
	log.Error("boom")
	attrs, _ := tr.events[0].Contexts["log"]["attributes"].(map[string]any)
	if attrs["service"] != "wuzz-catalog" {
		t.Errorf("attributes = %+v; want service=wuzz-catalog", attrs)
	}
}

func TestDisabledWithoutADSN(t *testing.T) {
	flush, err := Init(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Logging an error with no client bound must not panic.
	sentry.CurrentHub().BindClient(nil)
	Wrap(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))).Error("nobody is listening")
}

func TestInitRejectsABadDSN(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sentry.DSN = "not a dsn"
	if _, err := Init(cfg); err == nil {
		t.Error("a malformed DSN should be an error, not silently ignored")
	}
}
