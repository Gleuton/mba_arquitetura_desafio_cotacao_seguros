package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

var testPartner = platform.Partner{Name: "partner-flaky", BaseURL: "http://flaky"}

type fakeQuoter struct {
	calls   int
	failFor int
}

func (f *fakeQuoter) Quote(_ context.Context, p platform.Partner, _ any) (partner.Quote, error) {
	f.calls++
	if f.calls <= f.failFor {
		return partner.Quote{}, &partner.Error{Partner: p.Name, Status: 503, Reason: "partner answered 503"}
	}
	return partner.Quote{Partner: p.Name, PremiumCents: 100}, nil
}

func TestBreakerOpensOnTheFifthConsecutiveFailureAndClosesAfterTwoHalfOpenSuccesses(t *testing.T) {
	const openDuration = 20 * time.Millisecond
	fake := &fakeQuoter{failFor: 5}
	b, err := newBreaker([]platform.Partner{testPartner}, fake, 5, openDuration, 2)
	if err != nil {
		t.Fatalf("newBreaker: %v", err)
	}

	for i := 1; i <= 5; i++ {
		if _, err := b.Quote(context.Background(), testPartner, nil); err == nil {
			t.Fatalf("call %d: expected the fake partner failure to surface, got nil", i)
		} else if errors.Is(err, gobreaker.ErrOpenState) {
			t.Fatalf("call %d: breaker already open before the 5th failure", i)
		}
	}
	if fake.calls != 5 {
		t.Fatalf("fake partner called %d times before tripping, want 5", fake.calls)
	}

	if _, err := b.Quote(context.Background(), testPartner, nil); !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("6th call: expected gobreaker.ErrOpenState right after tripping, got %v", err)
	}
	if fake.calls != 5 {
		t.Fatalf("fake partner called while the breaker was open: %d calls, want still 5", fake.calls)
	}

	time.Sleep(10 * openDuration)

	if _, err := b.Quote(context.Background(), testPartner, nil); err != nil {
		t.Fatalf("first half-open probe: unexpected error %v", err)
	}
	if _, err := b.Quote(context.Background(), testPartner, nil); err != nil {
		t.Fatalf("second half-open probe: unexpected error %v", err)
	}

	if _, err := b.Quote(context.Background(), testPartner, nil); err != nil {
		t.Fatalf("call after two half-open successes: breaker should be closed, got %v", err)
	}
	if fake.calls != 8 {
		t.Fatalf("fake partner called %d times, want 8 (5 failures + 2 probes + 1 closed call)", fake.calls)
	}
}

func TestBreakerReopensOnAHalfOpenFailure(t *testing.T) {
	const openDuration = 20 * time.Millisecond
	fake := &fakeQuoter{failFor: 6}
	b, err := newBreaker([]platform.Partner{testPartner}, fake, 5, openDuration, 2)
	if err != nil {
		t.Fatalf("newBreaker: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := b.Quote(context.Background(), testPartner, nil); err == nil {
			t.Fatalf("call %d: expected failure", i)
		}
	}

	time.Sleep(10 * openDuration)

	if _, err := b.Quote(context.Background(), testPartner, nil); err == nil {
		t.Fatal("half-open probe: expected the 6th fake failure to surface")
	}

	if _, err := b.Quote(context.Background(), testPartner, nil); !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected a failed half-open probe to reopen the circuit immediately, got %v", err)
	}
}

func TestBreakerBypassesAPartnerItWasNotConfiguredFor(t *testing.T) {
	fake := &fakeQuoter{}
	b, err := newBreaker([]platform.Partner{testPartner}, fake, 5, time.Hour, 2)
	if err != nil {
		t.Fatalf("newBreaker: %v", err)
	}

	unconfigured := platform.Partner{Name: "partner-unknown", BaseURL: "http://unknown"}
	if _, err := b.Quote(context.Background(), unconfigured, nil); err != nil {
		t.Fatalf("Quote for an unconfigured partner: unexpected error %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("fake quoter called %d times, want 1 (no circuit to short-circuit it)", fake.calls)
	}
}

func TestBreakerMarksTheSpanWhenShortCircuited(t *testing.T) {
	const openDuration = time.Hour
	fake := &fakeQuoter{failFor: 5}
	b, err := newBreaker([]platform.Partner{testPartner}, fake, 5, openDuration, 2)
	if err != nil {
		t.Fatalf("newBreaker: %v", err)
	}
	for i := 0; i < 5; i++ {
		_, _ = b.Quote(context.Background(), testPartner, nil)
	}

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := tp.Tracer("test").Start(context.Background(), "quote")

	if _, err := b.Quote(ctx, testPartner, nil); !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected the circuit to already be open, got %v", err)
	}
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("%d spans exported, want 1", len(spans))
	}
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "partner.circuit_breaker.short_circuited" && attr.Value.AsBool() {
			return
		}
	}
	t.Fatalf("span attributes %v do not mark the short circuit", spans[0].Attributes)
}
