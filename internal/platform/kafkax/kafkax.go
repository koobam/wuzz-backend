// Package kafkax is the Kafka edge: the producer the outbox relay drains into.
package kafkax

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
)

// Topics resolves logical topic names ("user.events") to physical ones
// ("wuzz.user.events"), so one cluster can host several environments.
type Topics struct{ prefix string }

func NewTopics(cfg config.Kafka) Topics { return Topics{prefix: cfg.TopicPrefix} }

func (t Topics) Name(logical string) string {
	if t.prefix == "" {
		return logical
	}
	return t.prefix + "." + logical
}

// createTopicsTimeout bounds the broker-side wait, not the client's. A cluster
// mid-election can take a while to answer, and failing boot faster than the
// cluster can respond turns a slow start into a crash loop.
const createTopicsTimeout = 15 * time.Second

type Producer struct {
	client *kgo.Client
	topics Topics
}

func NewProducer(cfg config.Kafka) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		// Idempotent production is franz-go's default; with AllISRAcks a broker
		// failure cannot silently drop an event the outbox has marked sent.
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka: producer: %w", err)
	}
	return &Producer{client: client, topics: NewTopics(cfg)}, nil
}

func (p *Producer) Close() { p.client.Close() }

// EnsureTopics creates the topics this process produces to, if they are not
// there already. Partition count is decided here rather than inherited from a
// broker's auto-create default: ordering holds only within a partition, records
// are keyed by aggregate id, and the number can be raised later but never
// lowered. Idempotent, so every replica calls it at boot.
func (p *Producer) EnsureTopics(ctx context.Context, cfg config.Kafka, logical ...string) error {
	req := kmsg.NewPtrCreateTopicsRequest()
	req.TimeoutMillis = int32(createTopicsTimeout.Milliseconds())
	for _, name := range logical {
		topic := kmsg.NewCreateTopicsRequestTopic()
		topic.Topic = p.topics.Name(name)
		topic.NumPartitions = cfg.TopicPartitions
		topic.ReplicationFactor = cfg.TopicReplication
		req.Topics = append(req.Topics, topic)
	}

	res, err := req.RequestWith(ctx, p.client)
	if err != nil {
		return fmt.Errorf("kafka: create topics: %w", err)
	}
	for _, topic := range res.Topics {
		switch err := kerr.ErrorForCode(topic.ErrorCode); {
		case err == nil, errors.Is(err, kerr.TopicAlreadyExists):
			// Another replica won the race, or it was provisioned elsewhere.
		default:
			return fmt.Errorf("kafka: create topic %s: %w", topic.Topic, err)
		}
	}
	return nil
}

// Publish sends one already-encoded event. It satisfies outbox.Publisher.
//
// Synchronous on purpose: the relay marks a row published only after the broker
// acknowledges it, so a crash mid-flight replays rather than loses.
func (p *Producer) Publish(ctx context.Context, topic, key string, payload []byte) error {
	name := p.topics.Name(topic)
	record := &kgo.Record{Topic: name, Key: []byte(key), Value: payload}
	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		observability.KafkaMessages.WithLabelValues(name, "produce", "error").Inc()
		return fmt.Errorf("kafka: produce to %s: %w", name, err)
	}
	observability.KafkaMessages.WithLabelValues(name, "produce", "ok").Inc()
	return nil
}

func (p *Producer) Ping(ctx context.Context) error { return p.client.Ping(ctx) }
