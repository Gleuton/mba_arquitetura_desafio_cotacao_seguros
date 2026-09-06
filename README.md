# Prumo Cota: README do processo

Este é o README do processo da entrega, exigido pelo enunciado do desafio. O enunciado original
está em [`docs/enunciado.md`](docs/enunciado.md).

## 1. Links

- SAD (Solution Architecture Document): [`docs/sad.md`](docs/sad.md).
- Evidências: [`docs/evidencias/`](docs/evidencias/), com as 7 linhas exigidas:

| Evidência                  | Comando / consulta                                                                                                              | O que mostra                                                                                                   | Arquivo                                                                                                                  |
|----------------------------|---------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------|
| Relatório antes/depois     | `make down && make reproduce`                                                                                                   | sucesso 50%→100% (baseline) e 60%→100% (carga); p95 de carga 8,01s→196ms                                       | [`historico-reproduce.md`](docs/evidencias/historico-reproduce.md)                                                       |
| Saída do `make test`       | `make test`                                                                                                                     | 82 testes, 0 falhas, incluindo os que já vinham antes da Entrega 2                                             | [`make-test-antes.md`](docs/evidencias/make-test-antes.md), [`make-test-depois.md`](docs/evidencias/make-test-depois.md) |
| Trace com breaker aberto   | export JSON do Jaeger                                                                                                           | span `POST /quotes` com `partner.circuit_breaker.short_circuited=true`, sem span de saída para `partner-flaky` | [`trace-breaker-aberto.json`](docs/evidencias/trace-breaker-aberto.json)                                                 |
| Trace servido de cache     | export JSON do Jaeger                                                                                                           | span `POST /quotes` com `quotation.cache_hit=true`, sem nenhum span de saída HTTP a parceira                   | [`trace-cache-hit.json`](docs/evidencias/trace-cache-hit.json)                                                           |
| Estado do breaker no tempo | `partner_breaker_state{partner="partner-flaky"}`, janela 12:19:15-12:21:05 UTC (2026-09-06)                                     | degrau aberto(1) → meio aberto(2) → fechado(0)                                                                 | [`breaker-state-depois.png`](docs/evidencias/breaker-state-depois.png)                                                   |
| Hit rate do cache          | `sum(partner_cache_result_total{result="hit"}) / sum(partner_cache_result_total)`, janela 12:17:08-12:17:25 UTC (2026-09-06)    | sobe de 0% para 91%                                                                                            | [`hit-rate-depois.png`](docs/evidencias/hit-rate-depois.png)                                                             |
| p95 do `POST /quotes`      | `histogram_quantile(0.95, sum(rate(http_server_request_duration_seconds_bucket{http_route="/quotes"}[5m])) by (le))`, janela 1h | antes: sobe de ≈2s para ≈9-10s sob carga; depois: achatado em ≈0,23s                                           | [`p95-antes.png`](docs/evidencias/p95-antes.png), [`p95-depois.png`](docs/evidencias/p95-depois.png)                     |

## 2. Como subir o ambiente e reproduzir esta versão

Requer Docker com Compose v2 e Go 1.25 (para os alvos que rodam fora de container).

```bash
make down && make reproduce
```

Sobe os oito serviços do compose (`quotation-api`, as três parceiras, Redis, Collector, Jaeger,
Prometheus), espera todos ficarem saudáveis e roda a carga padrão (10 requisições de baseline,
depois 200 com 50 em voo). O relatório sai no terminal; é o mesmo formato colado em
`docs/evidencias/historico-reproduce.md`.

`docs/evidencias/p95-depois.png` foi capturado numa execução isolada deste comando (só a carga
padrão, sem nenhum tráfego extra depois): qualquer requisição adicional na mesma janela de 1h
"suja" a consulta com `rate([5m])`, porque ela mistura o efeito de qualquer outro teste na mesma
métrica agregada (`http_server_request_duration_seconds_bucket{http_route="/quotes"}`). Por isso o
resultado é uma linha baixa e achatada (≈0,23 s), sem o degrau do `p95-antes.png`: a carga inteira
agora é dominada por acerto de cache, e não há mais uma cauda lenta grande o bastante para puxar o
percentil agregado para cima.

Para conferir os mecanismos manualmente:

- Jaeger (`http://localhost:16686`, serviço `quotation-api`): um trace com o atributo
  `partner.circuit_breaker.short_circuited=true` no span `POST /quotes` não tem span de saída para
  a parceira curto-circuitada; um trace com `quotation.cache_hit=true` não tem nenhum span de saída
  HTTP para nenhuma parceira.
- Prometheus (`http://localhost:9090`): `partner_breaker_state{partner="partner-flaky"}` mostra o
  estado atual do circuito (0 fechado, 1 aberto, 2 meio aberto); a razão
  `sum(partner_cache_result_total{result="hit"}) / sum(partner_cache_result_total)` mostra o hit
  rate agregado.
- `make test` roda a suíte inteira (82 testes nesta entrega, todos verdes); `make smoke` prova, sem
  Docker, que os defaults do cenário abririam um circuit breaker com o limiar de 5 falhas
  consecutivas.

A carga padrão do `make reproduce` sozinha não é suficiente para abrir o circuito da `partner-flaky`
nem para desenhar uma curva de hit rate com vários pontos (ela é rápida e pequena demais para isso,
o que é justamente o problema que o cache resolve). Para observar essas duas transições sob demanda,
sem alterar nenhum perfil de parceira, use o gerador de carga com um tenant e uma contagem de
cotações distintas maiores, o suficiente para acumular chamadas reais até a rajada determinística de
falhas (sequências 49 a 57) da `partner-flaky`:

```bash
make load ARGS="-tenant corretora-b -distinct 20 -concurrency 1 -baseline 0"
```

## 3. O que foi implementado e o que ficou como proposta

**Implementado, com evidência e teste:**

- Timeout de 2000 ms na chamada à parceira (`internal/partner/client.go`), contando como falha para
  o circuit breaker.
- Circuit breaker por parceira (`internal/resilience/breaker.go`, `sony/gobreaker` v2): 5 falhas
  consecutivas abrem, 5 s aberto, 2 sucessos no meio aberto fecham, uma falha no meio aberto reabre.
  O estado aberto de fato não chama a parceira (confirmado em
  `docs/evidencias/trace-breaker-aberto.json`, sem span de saída para `partner-flaky`).
- Cache por parceira em Redis (`internal/resilience/cache.go`), chave
  `quote:v1:{tenant_id}:{partner_name}:{sha256(document|birth_year|plate|model|year|value_cents|coverage)[:16]}`,
  TTL de 1 hora, isolada por corretora (testado em
  `internal/resilience/cache_test.go`).
- `ResilientQuoter` (`internal/resilience/quoter.go`): orquestra cache, depois breaker, na mesma
  interface `Quoter` que o `Service` já consumia, sem mudar a lógica de agregação.
- Fallback e resposta parcial (`internal/quotation/service.go`, `request.go`, `handler.go`):
  `Response` ganhou `missing_partners` e `degraded`; uma parceira ausente não derruba mais a
  requisição inteira; sem piso mínimo, mesmo com as três parceiras fora a resposta é `200` com
  `quotes: []` e as três nomeadas em `missing_partners`.
- As três métricas de negócio e as duas marcações de trace exigidas (nomes na seção 4 abaixo).

**Proposto no SAD, não implementado nesta entrega** (seção 4 do SAD, "Limites conhecidos"):

- Limite de chamadas simultâneas por parceira.
- Circuito compartilhado entre réplicas (hoje vive na memória do processo).
- Invalidação ativa do cache além do TTL.
- Isolamento de capacidade entre corretoras (o circuito é por parceira, não por par
  parceira/corretora).
- Paralelização da agregação e auditoria real de verdade: citadas como bônus opcional no enunciado,
  fora do escopo obrigatório desta entrega.

## 4. Métricas e atributos criados

| Nome                                                         | Tipo     | Onde                             |
|--------------------------------------------------------------|----------|----------------------------------|
| `partner_breaker_state`                                      | gauge    | `internal/resilience/breaker.go` |
| `partner_breaker_transitions_total`                          | contador | `internal/resilience/breaker.go` |
| `partner_cache_result_total`                                 | contador | `internal/resilience/cache.go`   |
| `partner.circuit_breaker.short_circuited` (atributo de span) | booleano | `internal/resilience/breaker.go` |
| `quotation.cache_hit` (atributo de span)                     | booleano | `internal/resilience/cache.go`   |

A latência por parceira reaproveita a métrica genérica já existente
`http_client_request_duration_seconds`, rotulada por `server_address` (seção 6 do SAD).

## 5. O que seria diferente com mais tempo

- Mediria o hit rate e o p95 sob um tráfego mais realista (mais de cinco cotações distintas, chegada
  não sincronizada), em vez de só a carga padrão do `make reproduce`, que é o que o enunciado limita
  para manter a comparação antes/depois justa entre alunos.
- Implementaria o par de itens de bônus (paralelização da agregação, auditoria real), planejados
  como próximo passo depois desta entrega.
- Investigaria dar ao circuit breaker uma reafirmação periódica do estado atual (hoje o gauge
  `partner_breaker_state` só emite no momento da transição, então não existe amostra do estado
  fechado inicial antes da primeira falha, só depois da primeira transição).

## O coração da solução

O que esta entrega protege é a chamada da `quotation-api` às três seguradoras parceiras, hoje o
único ponto sem nenhuma defesa do sistema. O que a protege são três mecanismos que atuam em
sequência antes de qualquer chamada de rede sair: primeiro o cache (evita a consulta inteiramente
quando já existe uma cotação recente da mesma corretora para o mesmo risco), depois o circuit
breaker por parceira (evita insistir numa parceira que já provou, nas últimas chamadas, que não vai
responder), e só depois o cliente HTTP com timeout de 2000 ms (evita esperar indefinidamente por uma
parceira lenta ou travada). Quando, mesmo assim, uma parceira não responde, o fallback garante que a
corretora recebe o que as outras duas conseguiram entregar, em vez de um erro genérico que descarta
tudo. O custo dessa proteção é a complexidade de mais um componente (`internal/resilience/`), mais
uma dependência de infraestrutura (o Redis, que já subia ocioso no compose) e um contrato de
resposta mais rico, que qualquer cliente da API precisa aprender a interpretar (`missing_partners`,
`degraded`, `origin`, `age_seconds` por cotação).
