package resilience

import (
	"context"
	"testing"
	"time"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

var quoterTestPartner = platform.Partner{Name: "partner-flaky", BaseURL: "http://flaky"}

type fakeNext struct {
	calls int
	fail  bool
}

func (f *fakeNext) Quote(_ context.Context, p platform.Partner, _ any) (partner.Quote, error) {
	f.calls++
	if f.fail {
		return partner.Quote{}, &partner.Error{Partner: p.Name, Status: 503, Reason: "partner answered 503"}
	}
	return partner.Quote{Partner: p.Name, PremiumCents: 42, Currency: "BRL", Origin: "live"}, nil
}

func newTestResilientQuoter(t *testing.T, next Quoter) (*ResilientQuoter, *Cache) {
	t.Helper()

	cache, err := newCache(newFakeStore(time.Now), time.Hour)
	if err != nil {
		t.Fatalf("newCache: %v", err)
	}
	breaker, err := newBreaker([]platform.Partner{quoterTestPartner}, next, 100, time.Hour, 2)
	if err != nil {
		t.Fatalf("newBreaker: %v", err)
	}
	return NewResilientQuoter(cache, breaker), cache
}

func TestResilientQuoterServesFromCacheWithoutCallingNext(t *testing.T) {
	req := testCacheRequest("corretora-a")
	next := &fakeNext{}
	quoter, cache := newTestResilientQuoter(t, next)

	cached := partner.Quote{Partner: quoterTestPartner.Name, PremiumCents: 999, Currency: "BRL", Origin: "live"}
	if err := cache.Set(context.Background(), quoterTestPartner, req, cached); err != nil {
		t.Fatalf("Set: %v", err)
	}

	quote, err := quoter.Quote(context.Background(), quoterTestPartner, req)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if quote.Origin != "cache" {
		t.Errorf("origin %q, want cache", quote.Origin)
	}
	if quote.PremiumCents != 999 {
		t.Errorf("premium %d, want 999 (from cache)", quote.PremiumCents)
	}
	if next.calls != 0 {
		t.Errorf("next called %d times, want 0 (a cache hit must short-circuit)", next.calls)
	}
}

func TestResilientQuoterCachesASuccessfulLiveCall(t *testing.T) {
	req := testCacheRequest("corretora-a")
	next := &fakeNext{}
	quoter, _ := newTestResilientQuoter(t, next)

	first, err := quoter.Quote(context.Background(), quoterTestPartner, req)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if first.Origin != "live" {
		t.Errorf("origin %q, want live", first.Origin)
	}
	if next.calls != 1 {
		t.Fatalf("next called %d times, want 1", next.calls)
	}

	second, err := quoter.Quote(context.Background(), quoterTestPartner, req)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if second.Origin != "cache" {
		t.Errorf("origin %q, want cache on the second call", second.Origin)
	}
	if next.calls != 1 {
		t.Errorf("next called %d times, want still 1 (the second call should hit the cache)", next.calls)
	}
}

func TestResilientQuoterPropagatesAFailureWithoutCaching(t *testing.T) {
	req := testCacheRequest("corretora-a")
	next := &fakeNext{fail: true}
	quoter, _ := newTestResilientQuoter(t, next)

	if _, err := quoter.Quote(context.Background(), quoterTestPartner, req); err == nil {
		t.Fatal("expected the partner failure to propagate")
	}

	next.fail = false
	quote, err := quoter.Quote(context.Background(), quoterTestPartner, req)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if quote.Origin != "live" || next.calls != 2 {
		t.Fatalf("expected the failed call not to be cached: origin=%q calls=%d", quote.Origin, next.calls)
	}
}
