package resilience

import (
	"context"
	"errors"
	"time"

	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

const (
	consecutiveFailuresToOpen = 5
	openFor                   = 5 * time.Second
	halfOpenProbes            = 2
)

type Quoter interface {
	Quote(ctx context.Context, p platform.Partner, request any) (partner.Quote, error)
}

type Breaker struct {
	next        Quoter
	circuits    map[string]*gobreaker.CircuitBreaker[partner.Quote]
	state       metric.Int64Gauge
	transitions metric.Int64Counter
}

func NewBreaker(partners []platform.Partner, next Quoter) (*Breaker, error) {
	return newBreaker(partners, next, consecutiveFailuresToOpen, openFor, halfOpenProbes)
}

func newBreaker(partners []platform.Partner, next Quoter, failuresToOpen uint32, openDuration time.Duration, halfOpenRequests uint32) (*Breaker, error) {
	meter := otel.Meter("quotation-api/resilience")

	state, err := meter.Int64Gauge("partner_breaker_state",
		metric.WithDescription("estado atual do circuito por parceira (0 fechado, 1 aberto, 2 meio aberto)"))
	if err != nil {
		return nil, err
	}
	transitions, err := meter.Int64Counter("partner_breaker_transitions_total",
		metric.WithDescription("quantas vezes o circuito de uma parceira mudou de estado, e entre quais"))
	if err != nil {
		return nil, err
	}

	b := &Breaker{
		next:        next,
		circuits:    make(map[string]*gobreaker.CircuitBreaker[partner.Quote], len(partners)),
		state:       state,
		transitions: transitions,
	}

	for _, p := range partners {
		name := p.Name
		b.circuits[name] = gobreaker.NewCircuitBreaker[partner.Quote](gobreaker.Settings{
			Name:        name,
			MaxRequests: halfOpenRequests,
			Timeout:     openDuration,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= failuresToOpen
			},
			OnStateChange: func(_ string, from, to gobreaker.State) {
				attrs := metric.WithAttributes(attribute.String("partner", name))
				b.state.Record(context.Background(), stateValue(to), attrs)
				b.transitions.Add(context.Background(), 1, metric.WithAttributes(
					attribute.String("partner", name),
					attribute.String("from", from.String()),
					attribute.String("to", to.String()),
				))
			},
		})
	}

	return b, nil
}

func (b *Breaker) Quote(ctx context.Context, p platform.Partner, request any) (partner.Quote, error) {
	cb, ok := b.circuits[p.Name]
	if !ok {
		return b.next.Quote(ctx, p, request)
	}

	quote, err := cb.Execute(func() (partner.Quote, error) {
		return b.next.Quote(ctx, p, request)
	})
	if errors.Is(err, gobreaker.ErrOpenState) {
		trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("partner.circuit_breaker.short_circuited", true))
	}
	return quote, err
}

func stateValue(s gobreaker.State) int64 {
	switch s {
	case gobreaker.StateOpen:
		return 1
	case gobreaker.StateHalfOpen:
		return 2
	default:
		return 0
	}
}
