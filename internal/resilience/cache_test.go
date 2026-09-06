package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

type fakeStoreItem struct {
	value     string
	expiresAt time.Time
}

type fakeStore struct {
	now   func() time.Time
	items map[string]fakeStoreItem
}

func newFakeStore(now func() time.Time) *fakeStore {
	return &fakeStore{now: now, items: map[string]fakeStoreItem{}}
}

func (f *fakeStore) Get(_ context.Context, key string) (string, error) {
	item, ok := f.items[key]
	if !ok || !f.now().Before(item.expiresAt) {
		return "", errors.New("redis: nil")
	}
	return item.value, nil
}

func (f *fakeStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	f.items[key] = fakeStoreItem{value: value, expiresAt: f.now().Add(ttl)}
	return nil
}

var cachePartner = platform.Partner{Name: "partner-flaky", BaseURL: "http://flaky"}

type cacheRequest struct {
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

func testCacheRequest(broker string) cacheRequest {
	r := cacheRequest{Broker: broker, Coverage: "comprehensive"}
	r.Driver.Document = "12345678901"
	r.Driver.BirthYear = 1988
	r.Vehicle.Plate = "ABC1D23"
	r.Vehicle.Model = "Gol 1.0"
	r.Vehicle.Year = 2020
	r.Vehicle.ValueCents = 8500000
	return r
}

func TestCacheMissesAfterTheTTLElapsesWithoutWaitingRealTime(t *testing.T) {
	now := time.Now()
	fake := newFakeStore(func() time.Time { return now })

	cache, err := newCache(fake, time.Hour)
	if err != nil {
		t.Fatalf("newCache: %v", err)
	}

	req := testCacheRequest("corretora-a")
	quote := partner.Quote{Partner: cachePartner.Name, PremiumCents: 12345, Currency: "BRL"}

	if err := cache.Set(context.Background(), cachePartner, req, quote); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got, _, hit := cache.Get(context.Background(), cachePartner, req); !hit || got.PremiumCents != quote.PremiumCents {
		t.Fatalf("expected a hit right after Set, got hit=%v quote=%+v", hit, got)
	}

	now = now.Add(time.Hour + time.Second)

	if _, _, hit := cache.Get(context.Background(), cachePartner, req); hit {
		t.Fatal("expected a miss after the TTL elapsed, got a hit")
	}
}

func TestCacheGetTreatsACorruptedEntryAsAMiss(t *testing.T) {
	fake := newFakeStore(time.Now)
	cache, err := newCache(fake, time.Hour)
	if err != nil {
		t.Fatalf("newCache: %v", err)
	}

	req := testCacheRequest("corretora-a")
	key, err := cacheKey(cachePartner, req)
	if err != nil {
		t.Fatalf("cacheKey: %v", err)
	}
	if err := fake.Set(context.Background(), key, "not json", time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, _, hit := cache.Get(context.Background(), cachePartner, req); hit {
		t.Fatal("a value that does not unmarshal into a cachedEntry was served as a hit")
	}
}

func TestCacheKeyIsolatesByTenant(t *testing.T) {
	cache, err := newCache(newFakeStore(time.Now), time.Hour)
	if err != nil {
		t.Fatalf("newCache: %v", err)
	}

	quote := partner.Quote{Partner: cachePartner.Name, PremiumCents: 500, Currency: "BRL"}
	if err := cache.Set(context.Background(), cachePartner, testCacheRequest("corretora-a"), quote); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, _, hit := cache.Get(context.Background(), cachePartner, testCacheRequest("corretora-b")); hit {
		t.Fatal("a cache entry written for corretora-a was served to corretora-b")
	}
}

func TestCacheKeyIsolatesByPartner(t *testing.T) {
	cache, err := newCache(newFakeStore(time.Now), time.Hour)
	if err != nil {
		t.Fatalf("newCache: %v", err)
	}

	req := testCacheRequest("corretora-a")
	quote := partner.Quote{Partner: cachePartner.Name, PremiumCents: 500, Currency: "BRL"}
	if err := cache.Set(context.Background(), cachePartner, req, quote); err != nil {
		t.Fatalf("Set: %v", err)
	}

	otherPartner := platform.Partner{Name: "partner-slow", BaseURL: "http://slow"}
	if _, _, hit := cache.Get(context.Background(), otherPartner, req); hit {
		t.Fatal("a cache entry written for partner-flaky was served for partner-slow")
	}
}
