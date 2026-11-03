package flightkafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/env"
)

// Config for collector → Kafka historical feed (application publish, not Debezium).
type Config struct {
	Enabled     bool
	Brokers     []string
	Topic       string
	RetentionMS int64
}

func LoadConfig() Config {
	retention := env.GetInt64("KAFKA_RETENTION_MS", 0)
	if retention <= 0 {
		hours := env.GetInt("KAFKA_RETENTION_HOURS", 168) // 1 week
		if hours <= 0 {
			hours = 168
		}
		retention = int64(hours) * int64(time.Hour/time.Millisecond)
	}
	brokersCSV := strings.TrimSpace(env.GetString("KAFKA_BOOTSTRAP", env.GetString("KAFKA_BROKERS", "")))
	var brokers []string
	for _, p := range strings.Split(brokersCSV, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			brokers = append(brokers, p)
		}
	}
	topic := strings.TrimSpace(env.GetString("KAFKA_TOPIC", "flights-historical"))
	enabled := envTruthy("KAFKA_ENABLED") && len(brokers) > 0 && topic != ""
	return Config{
		Enabled:     enabled,
		Brokers:     brokers,
		Topic:       topic,
		RetentionMS: retention,
	}
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(strings.ToLower(env.GetString(key, "")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// Publisher writes collected flight offers to Kafka when KAFKA_ENABLED is on.
type Publisher struct {
	enabled bool
	topic   string
	w       *kafka.Writer
	log     *slog.Logger
}

func New(cfg Config, log *slog.Logger) (*Publisher, error) {
	if log == nil {
		log = slog.Default()
	}
	if !cfg.Enabled {
		log.Info("flight kafka historical publish off")
		return &Publisher{enabled: false, log: log}, nil
	}
	if err := ensureTopic(cfg); err != nil {
		log.Warn("flight kafka ensure topic", "topic", cfg.Topic, "err", err)
	}
	w := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireOne,
		Async:        false,
		BatchTimeout: 50 * time.Millisecond,
		MaxAttempts:  3,
	}
	log.Info("flight kafka historical publish on",
		"topic", cfg.Topic,
		"brokers", cfg.Brokers,
		"retention_ms", cfg.RetentionMS,
	)
	return &Publisher{enabled: true, topic: cfg.Topic, w: w, log: log}, nil
}

func (p *Publisher) Enabled() bool {
	return p != nil && p.enabled
}

func (p *Publisher) Close() error {
	if p == nil || p.w == nil {
		return nil
	}
	return p.w.Close()
}

// PublishFlights sends each offer as JSON. Postgres remains source of truth on failure.
func (p *Publisher) PublishFlights(ctx context.Context, flights []flight.Flight) {
	if !p.Enabled() || len(flights) == 0 {
		return
	}
	msgs := make([]kafka.Message, 0, len(flights))
	now := time.Now().UTC()
	for _, f := range flights {
		body, err := json.Marshal(f)
		if err != nil {
			p.log.Warn("flight kafka marshal", "err", err)
			continue
		}
		key := f.FlightNumberNorm
		if key == "" {
			key = f.FlightNumber
		}
		key = fmt.Sprintf("%s|%s|%s|%s", key, f.Origin, f.Destination, f.FlightDate.UTC().Format("2006-01-02"))
		msgs = append(msgs, kafka.Message{
			Key:   []byte(key),
			Value: body,
			Time:  now,
		})
	}
	if len(msgs) == 0 {
		return
	}
	if err := p.w.WriteMessages(ctx, msgs...); err != nil {
		p.log.Warn("flight kafka publish", "topic", p.topic, "count", len(msgs), "err", err)
		return
	}
	p.log.Info("flight kafka published", "topic", p.topic, "count", len(msgs))
}

func ensureTopic(cfg Config) error {
	if len(cfg.Brokers) == 0 || cfg.Topic == "" {
		return fmt.Errorf("missing brokers or topic")
	}
	conn, err := kafka.Dial("tcp", cfg.Brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return err
	}
	controllerAddr := net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))
	admin, err := kafka.Dial("tcp", controllerAddr)
	if err != nil {
		return err
	}
	defer admin.Close()

	err = admin.CreateTopics(kafka.TopicConfig{
		Topic:             cfg.Topic,
		NumPartitions:     1,
		ReplicationFactor: 1,
		ConfigEntries: []kafka.ConfigEntry{
			{ConfigName: "retention.ms", ConfigValue: strconv.FormatInt(cfg.RetentionMS, 10)},
		},
	})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}
	return nil
}
