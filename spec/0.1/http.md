# HTTP API

The reference transport is JSON over HTTP. The local runtime listens on loopback by default.

## General rules

- Base URL example: `http://127.0.0.1:4765`
- Authenticated requests use `Authorization: Bearer <token>`.
- Request bodies contain one JSON value and reject unknown fields.
- Request bodies are limited to 1 MiB.
- Responses set `Content-Type: application/json` and `Cache-Control: no-store`.
- Identifiers in paths are URL-escaped.

Successful API responses use:

```json
{
  "ok": true,
  "result": {}
}
```

Failures use:

```json
{
  "ok": false,
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "Readable explanation"
  }
}
```

## Routes

| Method | Route | Authority | Result |
| --- | --- | --- | --- |
| `GET` | `/health` | None | Runtime health and protocol version |
| `POST` | `/v1/admin/shutdown` | Admin | Accepted shutdown request |
| `POST` | `/v1/scopes` | Admin | New scope ID and scope token |
| `POST` | `/v1/agents` | Scope | New execution identity, lease, and agent token |
| `GET` | `/v1/agents` | Scope | Agents in the scope |
| `POST` | `/v1/links` | Scope | Symmetric peer link |
| `PATCH` | `/v1/me/heartbeat` | Agent | Renewed presence |
| `GET` | `/v1/peers` | Agent | Linked peers |
| `POST` | `/v1/messages` | Agent | Durable delivery receipt |
| `GET` | `/v1/messages/{messageId}` | Sender or recipient | Delivery receipt |
| `POST` | `/v1/messages/ack` | Recipient | Acknowledgement count |
| `POST` | `/v1/inbox/reserve` | Agent | Reservation or `null` |
| `POST` | `/v1/inbox/{reservationId}/commit` | Reserving agent | Delivered messages |
| `POST` | `/v1/inbox/{reservationId}/release` | Reserving agent | Release confirmation |
| `POST` | `/v1/tasks` | Scope or agent | New task |
| `GET` | `/v1/tasks` | Scope or agent | Tasks in the scope |
| `POST` | `/v1/tasks/{taskId}/claim` | Agent | Claimed task |
| `POST` | `/v1/tasks/{taskId}/release` | Claiming execution | Open task |
| `POST` | `/v1/tasks/{taskId}/complete` | Claiming execution | Completed task |
| `POST` | `/v1/escalations` | Agent | New escalation |
| `GET` | `/v1/escalations/{escalationId}` | Agent | Escalation in the scope |
| `GET` | `/v1/scope/escalations` | Scope | Escalations in the scope |
| `POST` | `/v1/scope/escalations/{escalationId}/resolve` | Scope | Resolved escalation |
| `POST` | `/mcp` | Agent | MCP Streamable HTTP endpoint |

`POST /v1/inbox/reserve` accepts an optional `limit` from 1 through 100; omission or 0 selects the default of 50. It also accepts an optional `waitMs` value from 0 through 25000. When no message is immediately reservable, a positive value waits until work arrives, the wait expires, the request is canceled, the server stops, or the execution loses authority. The default is 0 and returns immediately. A successful timeout returns `null` and does not reserve a message.

`GET /v1/tasks?ready=true` returns only open, unclaimed tasks whose dependencies are complete. The default returns every task in the scope.

Request and result shapes are defined in [protocol.schema.json](schemas/protocol.schema.json). Consumers can reference individual definitions with a fragment such as:

```text
protocol.schema.json#/$defs/sendMessageInput
```

## HTTP status mapping

| Code | HTTP status |
| --- | ---: |
| `INVALID_ARGUMENT` | 400 |
| `UNAUTHENTICATED` | 401 |
| `PERMISSION_DENIED` | 403 |
| `NOT_FOUND` | 404 |
| `METHOD_NOT_ALLOWED` | 405 |
| `CONFLICT` | 409 |
| `BACKPRESSURE` | 429 |
| `INTERNAL` | 500 |
