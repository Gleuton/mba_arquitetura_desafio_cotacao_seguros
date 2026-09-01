# Histórico de execuções do `make reproduce`

Arquivo de acompanhamento, não é a entrega final de evidências. Cada execução é colada aqui na
íntegra, com data e comando, para permitir a comparação completa entre o "antes" e o "depois" quando
o PoC estiver pronto. A entrega formal de evidências (`docs/evidencias/`, tabela do enunciado) é feita
depois, a partir do que estiver registrado aqui.

## 2026-08-30 — antes (sem circuit breaker, cache ou fallback)

Comando: `make down && make reproduce`

```
baseline — 10 requests, 1 in flight, 13:59:52 UTC to 14:00:11 UTC
  success        5 of 10 (50%)
  latency        p50 1.89s   p95 2.03s   p99 2.03s   max 2.03s
  throughput     0.5 req/s in 18.73s
  failures       HTTP 502 from partner-flaky: 5

load — 200 requests, 50 in flight, 14:00:11 UTC to 14:00:36 UTC
  success        120 of 200 (60%)
  latency        p50 6.11s   p95 8.00s   p99 8.03s   max 8.05s
  throughput     8.1 req/s in 24.79s
  failures       HTTP 502 from partner-flaky: 80

baseline → load
  p95 latency    2.03s → 8.00s   3.9x
  max latency    2.03s → 8.05s   4.0x
  success        50% → 60%
  throughput     0.5 → 8.1 req/s
```

## 2026-09-01 — antes, execução oficial (sem circuit breaker, cache ou fallback)

Repetição do `make down && make reproduce` imediatamente antes de tocar no código (working tree limpa
em `feat/solution`), para servir como a evidência formal do "antes" em `docs/evidencias/`. Números
batem com a execução de 2026-08-30 dentro da margem de jitter das parceiras (± 200ms na `partner-slow`,
± 50ms na `partner-flaky`), confirmando que o cenário é determinístico entre execuções.

Comando: `make down && make reproduce`

```
baseline — 10 requests, 1 in flight, 23:28:18 UTC to 23:28:37 UTC
  success        5 of 10 (50%)
  latency        p50 1.89s   p95 2.03s   p99 2.03s   max 2.03s
  throughput     0.5 req/s in 18.73s
  failures       HTTP 502 from partner-flaky: 5

load — 200 requests, 50 in flight, 23:28:37 UTC to 23:29:01 UTC
  success        120 of 200 (60%)
  latency        p50 6.11s   p95 8.01s   p99 8.03s   max 8.05s
  throughput     8.1 req/s in 24.79s
  failures       HTTP 502 from partner-flaky: 80

baseline → load
  p95 latency    2.03s → 8.01s   3.9x
  max latency    2.03s → 8.05s   4.0x
  success        50% → 60%
  throughput     0.5 → 8.1 req/s
```

Janela de carga no Prometheus/Jaeger: `23:28:18 UTC` a `23:29:01 UTC` em 2026-09-01.
