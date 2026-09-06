package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"

	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/partner"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/platform"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/quotation"
	"github.com/GuilhermeOliveira591/mba_arquitetura_desafio_cotacao_seguros/internal/resilience"
)

func main() {
	cfg, err := platform.LoadConfig(platform.Environment())
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	ctx, stopListening := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopListening()

	stopTelemetry, err := platform.StartTelemetry(context.Background(), cfg.Telemetry)
	if err != nil {
		log.Fatalf("telemetry: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := redisotel.InstrumentTracing(redisClient); err != nil {
		log.Fatalf("redis tracing instrumentation: %v", err)
	}
	if err := redisotel.InstrumentMetrics(redisClient); err != nil {
		log.Fatalf("redis metrics instrumentation: %v", err)
	}

	cache, err := resilience.NewCache(redisClient, time.Duration(cfg.CacheTTLSeconds)*time.Second)
	if err != nil {
		log.Fatalf("cache: %v", err)
	}

	breaker, err := resilience.NewBreaker(cfg.Partners, partner.NewClient())
	if err != nil {
		log.Fatalf("circuit breaker: %v", err)
	}

	api := quotation.NewAPI(
		quotation.NewService(cfg.Partners, resilience.NewResilientQuoter(cache, breaker)),
		cfg.Tenants,
	)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           platform.InstrumentHandler(api.Routes(), cfg.Telemetry.ServiceName),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		log.Print("quotation-api: signal received, shutting down")
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("quotation-api: forced shutdown: %v", err)
		}
		if err := redisClient.Close(); err != nil {
			log.Printf("quotation-api: redis shutdown: %v", err)
		}
		if err := stopTelemetry(shutdown); err != nil {
			log.Printf("quotation-api: telemetry shutdown: %v", err)
		}
	}()

	log.Printf("quotation-api listening on %s | partners=%d brokers=%v",
		server.Addr, len(cfg.Partners), cfg.Tenants)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server stopped: %v", err)
	}
}
