# API contracts

Base path: `/api/v1`. Request and response bodies use JSON. Errors have the shape `{"error":"message"}`. Browser endpoints use the `signaldesk_session` cookie. Event ingestion uses an organization-scoped bearer key.

## Authentication

```http
POST /api/v1/auth/login
Content-Type: application/json

{"email":"analyst@acme.local","password":"YOUR_DEMO_PASSWORD"}
```

The response identifies the user, organization and role, and sets a 12-hour HttpOnly cookie. `GET /me` returns the current identity. `POST /auth/logout` revokes the session. Login is limited to ten attempts per remote IP per 15-minute window. HTTPS deployments set `COOKIE_SECURE=true` and `PUBLIC_URL` to the exact browser origin.

## Ingest events

```http
POST /api/v1/events
Authorization: Bearer YOUR_ORGANIZATION_INGESTION_KEY
Content-Type: application/json

{
  "events": [{
    "event_id": "9f918802-33d5-44bd-8a80-32ba26b019d7",
    "schema_version": 1,
    "occurred_at": "2026-11-02T12:00:00.000Z",
    "domain": "identity",
    "action": "suspicious_login",
    "severity": "high",
    "entity_id": "alex.morgan",
    "actor": "alex.morgan@acme.example",
    "asset": "sso.acme.example",
    "message": "Login from an unfamiliar device",
    "metadata": {"source":"synthetic"}
  }]
}
```

Use a current timestamp when submitting this example. Batches contain 1–100 events and the HTTP body is capped at 1 MiB. `occurred_at` must be within the last 30 days and no more than five minutes in the future. Domains are `identity`, `endpoint`, `cloud`; severities are `info`, `low`, `medium`, `high`, `critical`.

Do not send `tenant_id` or `ingested_at`; the API assigns them. Reuse the same event ID, occurrence time and payload when retrying the same logical event. `202 {"accepted":1,"status":"queued"}` means Kafka acknowledged publication. A failed batch request can have partially published events; resend the same events so projection keys remain stable. Rate limiting returns `429` and `Retry-After: 60`.

## Read endpoints

| Method and route | Access | Parameters / result |
| --- | --- | --- |
| `GET /events` | Analyst | `q`, `domain`, `severity`, `entity`, `from`, `to`, `limit`, `offset`; returns `events`, `total`, `as_of`, `cached` |
| `GET /timeline/{entity}` | Analyst | `from`, `to`; returns up to 200 events, findings with `evidence_ids`, and query bounds |
| `GET /overview` | Both roles | Counts, approximate distinct entities, severity/domain buckets, latest indexed ingestion; managers may supply time filters only |
| `GET /widgets` | Both roles | Registry version and definitions permitted for the role |
| `GET /workspace` | Both roles | Stored personal workspace or role default |
| `GET /investigations` | Both roles | Most recent 100 investigation records within the organization |

Time bounds use RFC 3339 timestamps. The default range is the last 24 hours; ranges must remain inside the last 30 days. Search accepts `limit=1..200` and `offset + limit <= 1000`. Entity timelines are bounded to seven UTC calendar days. Events are ordered newest first; the console displays a chronological investigation view.

## Save a workspace

```http
PUT /api/v1/workspace
Content-Type: application/json

{
  "version": 1,
  "widgets": [
    {"id":"severity","type":"severity-breakdown","version":1,"config":{}},
    {"id":"recent-cloud","type":"recent-events","version":1,"config":{"domain":"cloud"}},
    {"id":"alex","type":"entity-timeline","version":1,"config":{"entity_id":"alex.morgan"}}
  ]
}
```

Analysts can save up to eight widgets with unique IDs. Unknown types, unsupported versions, unexpected fields and unauthorized widgets return `400`. Managers receive aggregate defaults and cannot update layouts.

## Save an investigation

```http
POST /api/v1/investigations
Content-Type: application/json

{
  "title":"Review unusual activity for Alex",
  "entity_id":"alex.morgan",
  "from":"2026-11-02T00:00:00Z",
  "to":"2026-11-02T23:00:00Z"
}
```

Use a current bounded time range. The response returns an ID, creator and creation timestamp. Saved records preserve the entity and query bounds; reopening them queries retained timeline data. `DELETE /investigations/{id}` removes an organization-owned record. Writes require the analyst role.

## Generate an evidence brief

```http
POST /api/v1/briefs
Content-Type: application/json

{"entity_id":"alex.morgan"}
```

Optional `from` and `to` narrow the evidence range. Response:

```json
{
  "entity_id":"alex.morgan",
  "event_count":4,
  "brief":{
    "method":"evidence",
    "claims":[{
      "text":"Unusual identity login",
      "evidence_ids":["9f918802-33d5-44bd-8a80-32ba26b019d7"]
    }]
  }
}
```

Bedrock output uses `method: "bedrock"` and includes `model_id`. Brief requests require an analyst session. The internal `/summarize` service is called by the API using its separate service credential.

## Operational endpoints

- `GET /healthz`: HTTP process liveness.
- `GET /readyz`: API access to Postgres and Cassandra.
- `GET /metrics`: Prometheus text counters and last-processing timestamp. Each worker also serves `/healthz` and `/metrics` on its configured HTTP port.

Common responses: `400` validation, `401` missing/expired credential, `403` role/origin rejection, `404` missing record, `429` rate limit, `503` dependency unavailable.
