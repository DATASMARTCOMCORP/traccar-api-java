# Lotes offline no OsmAnd

O listener **OsmAnd** recebe `POST /api/positions/batch` diretamente, sem TrackHub e sem
requisições HTTP por posição. Este caminho pertence à porta OsmAnd (normalmente 5055),
não à API administrativa na porta 8082. O proxy deve encaminhar o caminho para essa porta,
preservando `/api/positions/batch`. Alternativamente, publique-o dentro do prefixo já usado
para o OsmAnd e preserve o caminho ao remover esse prefixo.

Exemplo de configuração Nginx (ajuste a porta ao ambiente):

```nginx
location = /api/position-mobile/batch {
    client_max_body_size 1m;
    proxy_pass http://127.0.0.1:5055/api/positions/batch;
}
```

A rota pública é `POST /api/position-mobile/batch`; o proxy a traduz para
`/api/positions/batch` no listener OsmAnd. No ambiente de produção, o cliente deve
usar `https://traccar.datasmartcom.com.br/api/position-mobile/batch` em lugar da URL
do TrackHub. O envio individual em `/position-mobile/` continua preservado.

```json
{
  "deviceId": "identificador-unico-do-aparelho",
  "installId": "instalacao",
  "os": "android",
  "positions": [
    {
      "id": "id-estavel-do-relato",
      "timestamp": "2026-10-19T12:00:00Z",
      "lat": -22.89,
      "lon": -43.18,
      "speed": 10,
      "heading": 90,
      "accuracy": 5,
      "altitude": 12,
      "batt": 80,
      "charge": false,
      "is_moving": true,
      "activity": "in_vehicle",
      "heartbeat": false
    }
  ]
}
```

Aceita JSON e `Content-Encoding: gzip`, até 1 MiB descomprimido e 500 posições. `speed` está em nós;
`timestamp` aceita ISO 8601 (sem fuso é UTC), epoch em segundos ou milissegundos. A identidade tem a
mesma confiança do envio OsmAnd existente: `deviceId` é o identificador único, sem token; `installId` é
atributo, não credencial. O limite de lotes por minuto por aparelho é `ingest.batchesPerMinute`
(padrão 30; `0` desliga, para um consumidor de fila confiável).

## Identidade e idempotência

Cada item é um relato com identidade estável: `id` (texto ou inteiro, até 128 caracteres) quando o
cliente o envia; sem `id`, a identidade é o próprio fix, `fix:<epoch ms>:<lat>:<lon>`. O `id` precisa
nascer na origem (aparelho) e ser reenviado igual; quem repassa (TrackHub, consumidor SQS) não deve
gerar outro. A posição e o recibo (`tc_position_receipts`, chave `deviceid + eventid`) são gravados na
mesma transação. Um relato já gravado é reconhecido em qualquer lote, reenvio, reinício ou servidor
concorrente e não é gravado de novo nem gera eventos ou notificações de novo. O mesmo `id` com
conteúdo diferente é o mesmo relato: vale o primeiro gravado.

Envios individuais (`/position-mobile/`) também gravam recibo (`ingest.liveReceipts`, padrão ligado),
com `reportId` quando enviado ou a identidade do fix. Assim um relato enviado ao vivo e depois
reenviado pela fila offline é gravado uma vez.

## Resposta

A resposta só sai depois que cada item tem um resultado. Status `200` quando todos os resultados são
finais e `503` quando algum falhou (o corpo é o mesmo):

```json
{"accepted": 2, "results": [
  {"index": 0, "id": "a", "status": "stored", "positionId": 101},
  {"index": 1, "id": "b", "status": "duplicate", "positionId": 99},
  {"index": 2, "id": "c", "status": "failed", "reason": "storage"},
  {"index": 3, "status": "rejected", "reason": "invalid"}
]}
```

| status | significado | o remetente |
|---|---|---|
| `stored` | gravado agora, eventos e notificações processados | pode apagar |
| `duplicate` | já gravado antes (`positionId` é o registro original) | pode apagar |
| `rejected` | nunca será gravado: `invalid`, `invalid id`, `too old`, `future`, `filtered` | pode apagar |
| `failed` | resultado incerto (`storage`, `timeout`): pode ou não ter sido gravado | deve reenviar |

`accepted` conta os itens iniciais, na ordem do pedido, com resultado final; um cliente que só lê
`accepted` apaga esse prefixo e reenvia o resto. Um consumidor SQS deve apagar cada mensagem cujo item
é final e devolver à fila as `failed`; reenviar é seguro. Erros do lote inteiro: 400 corpo inválido,
413 acima do limite, 422 aparelho desconhecido, 429 lotes demais. O limite de 1 MiB também vale para
os envios individuais neste listener; os demais formatos OsmAnd são preservados.

## Ordem, atraso e retenção

- `ingest.maxAge` (padrão 7 dias) e `ingest.maxFuture` (padrão 1 hora) limitam o horário do fix. Recibos
  são apagados um dia depois de `ingest.maxAge`, então um item reenviado é reconhecido ou rejeitado
  como antigo; nunca é gravado duas vezes.
- Posições atrasadas são gravadas com o `fixTime` original. A posição atual do aparelho
  (`tc_devices.positionid`, cache, WebSocket) só avança: maior `fixTime` e, no empate, maior `id`. A
  regra é aplicada no banco com bloqueio da linha do aparelho, inclusive entre vários servidores.
- Uma posição atrasada mede a distância a partir da posição gravada anterior, sem passar o
  `totalDistance` da posição seguinte.
- Cercas: se a posição atrasada está do outro lado de uma cerca em relação às vizinhas já gravadas
  (anterior e seguinte do mesmo lado), são gerados os dois eventos, na posição atrasada e na seguinte,
  com o atributo `late: true`. Movimento, excesso de velocidade e ignição não são recalculados para
  posições atrasadas.
- Notificações seguem `notificator.timeThreshold` (15 min): eventos mais antigos são gravados e
  repassados (`event.forward`) mas não notificados.
