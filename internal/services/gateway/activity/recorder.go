package activity

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/koobam/wuzz-backend/internal/pkg/observability"
)

// Sink is where entries are stored.
type Sink interface {
	Insert(ctx context.Context, entries []Entry) error
}

var (
	written = register(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "wuzz_activity_written_total", Help: "Activity log entries stored.",
	}))
	lost = register(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wuzz_activity_lost_total", Help: "Activity log entries that could not be stored, by reason (queue_full, write_failed, closed).",
	}, []string{"reason"}))
)

func register[C prometheus.Collector](c C) C {
	observability.Registry.MustRegister(c)
	return c
}

const (
	queueSize     = 1024
	batchSize     = 100
	flushEvery    = 200 * time.Millisecond
	writeTimeout  = 5 * time.Second
	writeAttempts = 3
)

// Recorder takes entries from requests and stores them in the background, so the log can never slow a request or fail it. It is not
// silent either: an entry that cannot be stored (the queue is full, the database stays down through every retry) is logged at ERROR
// with its full content and counted in wuzz_activity_lost_total, so it can be put back by hand and an alert can fire on the counter.
type Recorder struct {
	sink  Sink
	log   *slog.Logger
	queue chan Entry
	stop  chan struct{}
	done  chan struct{}

	mu     sync.Mutex
	closed bool
	// retryDelays is how long to wait before each retry; tests shorten it.
	retryDelays []time.Duration
}

// NewRecorder starts the background writer. Close it on shutdown to flush what is queued.
func NewRecorder(sink Sink, log *slog.Logger) *Recorder {
	r := &Recorder{
		sink: sink, log: log,
		queue:       make(chan Entry, queueSize),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		retryDelays: []time.Duration{200 * time.Millisecond, time.Second, 3 * time.Second},
	}
	go r.run()
	return r
}

// Record queues e and returns at once.
func (r *Recorder) Record(e Entry) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		r.lose(e, "closed", nil)
		return
	}
	select {
	case r.queue <- e:
	default:
		r.lose(e, "queue_full", nil)
	}
}

// Close stops taking entries, writes what is queued, and returns when done or when ctx ends.
func (r *Recorder) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.stop)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	batch := make([]Entry, 0, batchSize)
	flush := func() {
		if len(batch) > 0 {
			r.write(batch)
			batch = make([]Entry, 0, batchSize)
		}
	}
	for {
		select {
		case e := <-r.queue:
			batch = append(batch, e)
			if len(batch) >= batchSize {
				flush()
			}
		case <-tick.C:
			flush()
		case <-r.stop:
			for {
				select {
				case e := <-r.queue:
					batch = append(batch, e)
					if len(batch) >= batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *Recorder) write(batch []Entry) {
	var err error
	for attempt := 0; attempt < writeAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(r.retryDelays[attempt-1])
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err = r.sink.Insert(ctx, batch)
		cancel()
		if err == nil {
			written.Add(float64(len(batch)))
			return
		}
		r.log.Warn("activity log write failed, will retry", slog.Int("entries", len(batch)), slog.Int("attempt", attempt+1), slog.Any("error", err))
	}
	for _, e := range batch {
		r.lose(e, "write_failed", err)
	}
}

func (r *Recorder) lose(e Entry, reason string, err error) {
	lost.WithLabelValues(reason).Inc()
	body, _ := json.Marshal(e)
	attrs := []any{slog.String("reason", reason), slog.String("entry", string(body))}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}
	r.log.Error("ACTIVITY LOG ENTRY LOST", attrs...)
}
