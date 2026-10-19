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

Aceita JSON e `Content-Encoding: gzip`, até 1 MiB descomprimido, 500 posições e
30 lotes por minuto por aparelho. `speed` está em nós; `timestamp` aceita ISO 8601
(sem fuso é UTC), epoch em segundos ou milissegundos. A identidade tem a mesma
confiança do envio OsmAnd existente: `deviceId` é o identificador único, sem token;
`installId` é atributo, não credencial.

Resposta 200: `{"accepted":N}`. Posições são ordenadas por horário e seguem o pipeline
normal do Traccar, incluindo filtros, geocercas, eventos e persistência. Inválidas e
duplicatas de horário/coordenadas dentro do lote também contam em `accepted`, permitindo
removê-las da fila. Não há deduplicação entre requisições.

Diferentemente do repasse HTTP, o lote inteiro é decodificado antes de entrar no pipeline;
não há um prefixo parcial devido à falha de uma chamada a outro serviço. `accepted`
confirma a admissão, não uma transação durável de todas as posições. A política global
`server.delayAcknowledgement` continua controlando o atraso da resposta até o processamento;
filtros normais podem descartar posições. Lotes concorrentes entram na fila por aparelho do
pipeline, sem o bloqueio HTTP do TrackHub. Envios ao vivo e lotes não são reordenados entre si.

Erros: 400 para corpo inválido, 413 para corpo acima do limite, 422 para aparelho
não reconhecido e 429 para excesso de lotes. O limite de 1 MiB também passa a valer
para os envios individuais neste listener; os demais formatos OsmAnd são preservados.
