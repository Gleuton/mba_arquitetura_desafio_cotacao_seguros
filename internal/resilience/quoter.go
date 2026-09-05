package resilience

import (
	"context"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
)

type ResilientQuoter struct {
	cache   *Cache
	breaker *Breaker
}

func NewResilientQuoter(cache *Cache, breaker *Breaker) *ResilientQuoter {
	return &ResilientQuoter{cache: cache, breaker: breaker}
}

func (q *ResilientQuoter) Quote(ctx context.Context, p platform.Partner, request any) (partner.Quote, error) {
	if quote, age, hit := q.cache.Get(ctx, p, request); hit {
		quote.Origin = "cache"
		quote.AgeSeconds = int64(age.Seconds())
		return quote, nil
	}

	quote, err := q.breaker.Quote(ctx, p, request)
	if err != nil {
		return partner.Quote{}, err
	}

	_ = q.cache.Set(ctx, p, request, quote)
	return quote, nil
}
