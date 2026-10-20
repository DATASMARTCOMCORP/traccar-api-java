# Ingestão por SQS

Plano completo: `Arquitetura de ingestão de posições - TrackHub e Traccar` (vault de arquitetura).

```
dispositivo ──► nginx ──► publicador (processo próprio) ──SendMessage──► SQS
                                                                          │
                     Traccar (SqsPositionConsumer) ◄──ReceiveMessage──────┘ ──► banco
```

Cada mensagem tem o formato do batch OsmAnd (`deviceId` + `positions[]`, ver `position-batch.md`).
A semântica é at-least-once: reenvios são reconhecidos pelo `id` de cada posição (ou pelo fix,
quando não há `id`).

## Publicador

`org.traccar.sqs.SqsPublisherMain` roda em um **processo separado** do Traccar, no mesmo host. Ele não
abre o banco: valida o formato, publica e só responde depois do aceite da SQS. Assim, deploy, restart,
crash ou pausa do Traccar não interrompem a entrada; os dispositivos só dependem do publicador e da SQS.
Nenhuma chamada bloqueia o event loop: a resposta sai do callback do `SqsAsyncClient`.

| Requisição | Resposta |
| --- | --- |
| `POST /` com o OsmAnd em query ou form (`id`, `lat`, `lon`, `timestamp`, `speed`, `bearing`, `altitude`, `accuracy`, `batt`, `charge`, `reportId`…) | `200` após o aceite; `400` sem device, sem coordenadas ou com valor inválido; `503` se a SQS recusar |
| `POST /api/positions/batch` | o mesmo JSON do batch do Traccar; itens válidos ficam `queued`, inválidos `rejected`; `503` se a SQS recusar |
| `GET /health` | `200` |

Limitações do formulário: só os campos que o batch carrega são publicados. `cell`, `wifi`, `hdop`,
`driverUniqueId`, tokens de notificação e atributos livres são descartados, e relatórios sem coordenadas
são recusados. A existência do device é conferida pelo consumidor (device desconhecido vai à DLQ).

Configuração (`conf/publisher.xml`):

```xml
<entry key="sqs.queueUrl">https://sqs.us-east-1.amazonaws.com/123456789012/positions</entry>
<entry key="sqs.region">us-east-1</entry>
<entry key="sqs.publisher.port">8090</entry>
<entry key="sqs.publisher.address">127.0.0.1</entry>
<entry key="logger.console">true</entry>
```

Execução: `java -cp tracker-server.jar:lib/* org.traccar.sqs.SqsPublisherMain conf/publisher.xml`, ou
a unit `setup/traccar-sqs-publisher.service`. Instale o publicador em **diretório próprio**
(`/opt/traccar-sqs-publisher`): o deploy do Traccar substitui `tracker-server.jar` e `lib/`, e trocar o
jar de uma JVM em execução pode quebrar o carregamento de classes. Atualize o publicador em um passo
separado.

IAM: `sqs:SendMessage` na fila principal.

### Nginx

```nginx
location /position-queue/ {
    proxy_pass http://127.0.0.1:8090/;
}

location = /api/position-queue/batch {
    client_max_body_size 256k;
    proxy_pass http://127.0.0.1:8090/api/positions/batch;
}
```

As rotas atuais (`/position-mobile/` e `/api/position-mobile/batch`, direto no Traccar) continuam
funcionando; os clientes migram de rota quando a fila estiver em produção.

## Consumidor

`SqsPositionConsumer` roda dentro de cada servidor Traccar quando `sqs.queueUrl` está definida.

| Chave | Padrão | Uso |
| --- | --- | --- |
| `sqs.queueUrl` | — | liga o consumidor |
| `sqs.deadLetterQueueUrl` | — | recebe mensagens que nunca passarão, com o motivo no atributo `reason` |
| `sqs.region` / `sqs.endpoint` | cadeia padrão da AWS | região; endpoint só para testes locais |
| `sqs.pollers` | 2 | laços de receive concorrentes (até 10 mensagens em voo cada) |
| `sqs.pauseThreshold` | 1000 | posições pendentes nos workers acima das quais o consumo pausa |

A mensagem é excluída quando todas as posições têm resultado final; com `FAILED` ou timeout
(`ingest.timeout`) ela volta com backoff de 30 s a 15 min. Corpo inválido, device desconhecido ou
desativado e itens inválidos vão à DLQ. O visibility timeout da fila deve ser maior que `ingest.timeout`.

IAM: `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:ChangeMessageVisibility` na fila principal e
`sqs:SendMessage` na DLQ.

## Testes locais

```
docker run -d --rm --name ministack -p 4566:4566 ministackorg/ministack
SQS_ENDPOINT=http://localhost:4566 ./gradlew test --tests 'org.traccar.sqs.*'
```
