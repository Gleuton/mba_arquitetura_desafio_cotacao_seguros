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

## 2026-09-06 — depois (circuit breaker, cache e fallback implementados)

Repetição do `make down && make reproduce`, mesma carga padrão (10 de baseline, 200 com 50 em voo),
agora com os sete commits da Entrega 2 aplicados. Ambiente subido do zero, sem nenhuma entrada de
cache herdada de execução anterior.

Comando: `make down && make reproduce`

```
baseline — 10 requests, 1 in flight, 12:17:01 UTC to 12:17:11 UTC
  success        10 of 10 (100%)
  latency        p50 195ms   p95 2.03s   p99 2.03s   max 2.03s
  throughput     1.0 req/s in 9.97s

load — 200 requests, 50 in flight, 12:17:11 UTC to 12:17:11 UTC
  success        200 of 200 (100%)
  latency        p50 2ms   p95 196ms   p99 207ms   max 218ms
  throughput     879.3 req/s in 227ms

baseline → load
  p95 latency    2.03s → 196ms   0.1x
  max latency    2.03s → 218ms   0.1x
  success        100% → 100%
  throughput     1.0 → 879.3 req/s
```

Janela de carga no Prometheus/Jaeger: `12:17:01 UTC` a `12:17:11 UTC` em 2026-09-06.

### Comparação direta com o "antes" (2026-09-01, mesma carga)

| Métrica            | Antes            | Depois            | Variação                                    |
|--------------------|------------------|-------------------|---------------------------------------------|
| Sucesso (baseline) | 50% (5 de 10)    | 100% (10 de 10)   | +50 p.p. (o fallback fecha os 502)          |
| Sucesso (carga)    | 60% (120 de 200) | 100% (200 de 200) | +40 p.p.                                    |
| p95 (baseline)     | 2,03 s           | 2,03 s            | igual (primeira chamada ainda é live, miss) |
| p95 (carga)        | 8,01 s           | 196 ms            | ≈41x mais rápido (cache aquecido)           |
| Vazão (carga)      | 8,1 req/s        | 879,3 req/s       | ≈108x                                       |

O p95 da carga bate folgadamente o alvo de RNF-01 (≤ 4 s): o resultado real (196 ms) é melhor que a
projeção da seção 3 do SAD (≈3,9 s), porque a projeção assumia toda chamada como live (miss de
cache); com a carga padrão repetindo cinco cotações, a maior parte da fase de carga é servida do
cache, sem tocar nenhuma parceira. RNF-02 (≥ 98% de disponibilidade) também bate: 100% em vez do
alvo, porque o fallback (RF-05) fecha os poucos casos em que `partner-flaky` falharia sozinha.

### Nota sobre o degrau do circuit breaker e a curva de hit rate

A carga padrão (5 cotações distintas) não é suficiente para produzir, sozinha, uma rajada de falhas
na `partner-flaky` nem para gerar volume visível de mudança de estado do breaker: ela é rápida
e pequena demais para isso, o que é o ponto do "buraco" de cache que está sendo fechado. Para
capturar a transição fechado → aberto → meio aberto → fechado e a curva de hit rate subindo, rodei
tráfego adicional com `make load` (`-tenant corretora-b -distinct 20 -concurrency 1`, depois
`-distinct 5` para fechar o circuito). Isso não altera nenhum perfil de parceira no
`docker-compose.yml` (`PARTNER_SEED`, `PARTNER_FAILURE_RATE`, latências e degradação continuam os
defaults do cenário). Só usa um tenant e uma quantidade de cotações distintas diferentes para forçar
chamadas reais em vez de acerto de cache, o suficiente para alcançar a rajada determinística de falhas
da `partner-flaky` (sequências 49 a 57). Os números de p95/sucesso/vazão que sustentam a comparação
acima vêm exclusivamente da carga padrão registrada logo acima; o tráfego extra serviu só para as
evidências de trace e gráfico descritas no README do processo.
