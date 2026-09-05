package resilience

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

type store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

type redisStore struct{ client *redis.Client }

func (r redisStore) Get(ctx context.Context, key string) (string, error) {
	return r.client.Get(ctx, key).Result()
}

func (r redisStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}

type Cache struct {
	store  store
	ttl    time.Duration
	result metric.Int64Counter
}

func NewCache(client *redis.Client, ttl time.Duration) (*Cache, error) {
	return newCache(redisStore{client}, ttl)
}

func newCache(s store, ttl time.Duration) (*Cache, error) {
	meter := otel.Meter("quotation-api/resilience")
	result, err := meter.Int64Counter("partner_cache_result_total",
		metric.WithDescription("acertos e erros do cache de cotacao, por parceira"))
	if err != nil {
		return nil, err
	}
	return &Cache{store: s, ttl: ttl, result: result}, nil
}

type cachedEntry struct {
	Quote    partner.Quote `json:"quote"`
	CachedAt time.Time     `json:"cached_at"`
}

func (c *Cache) Get(ctx context.Context, p platform.Partner, request any) (partner.Quote, time.Duration, bool) {
	key, err := cacheKey(p, request)
	if err != nil {
		return partner.Quote{}, 0, false
	}

	raw, err := c.store.Get(ctx, key)
	if err != nil {
		c.record(ctx, p.Name, "miss")
		return partner.Quote{}, 0, false
	}

	var entry cachedEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		c.record(ctx, p.Name, "miss")
		return partner.Quote{}, 0, false
	}

	c.record(ctx, p.Name, "hit")
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("quotation.cache_hit", true))
	return entry.Quote, time.Since(entry.CachedAt), true
}

func (c *Cache) Set(ctx context.Context, p platform.Partner, request any, quote partner.Quote) error {
	key, err := cacheKey(p, request)
	if err != nil {
		return err
	}

	raw, err := json.Marshal(cachedEntry{Quote: quote, CachedAt: time.Now()})
	if err != nil {
		return err
	}

	return c.store.Set(ctx, key, string(raw), c.ttl)
}

func (c *Cache) record(ctx context.Context, partnerName, result string) {
	c.result.Add(ctx, 1, metric.WithAttributes(
		attribute.String("partner", partnerName),
		attribute.String("result", result),
	))
}

type keyFields struct {
	Broker string `json:"broker"`
	Driver struct {
		Document  string `json:"document"`
		BirthYear int    `json:"birth_year"`
	} `json:"driver"`
	Vehicle struct {
		Plate      string `json:"plate"`
		Model      string `json:"model"`
		Year       int    `json:"year"`
		ValueCents int64  `json:"value_cents"`
	} `json:"vehicle"`
	Coverage string `json:"coverage"`
}

func cacheKey(p platform.Partner, request any) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	var fields keyFields
	if err := json.Unmarshal(body, &fields); err != nil {
		return "", err
	}

	risk := strings.Join([]string{
		fields.Driver.Document,
		strconv.Itoa(fields.Driver.BirthYear),
		fields.Vehicle.Plate,
		fields.Vehicle.Model,
		strconv.Itoa(fields.Vehicle.Year),
		strconv.FormatInt(fields.Vehicle.ValueCents, 10),
		fields.Coverage,
	}, "|")
	hash := sha256.Sum256([]byte(risk))

	return fmt.Sprintf("quote:v1:%s:%s:%s", fields.Broker, p.Name, hex.EncodeToString(hash[:])[:16]), nil
}
