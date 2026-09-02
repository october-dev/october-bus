# HTTP API

The reference transport is JSON over HTTP. The local runtime listens on loopback by default.

## General rules

- Base URL example: `http://127.0.0.1:4765`
- Authenticated requests use `Authorization: Bearer <token>`.
- Request bodies contain one JSON value and reject unknown fields.
- Request bodies are limited to 1 MiB. Scope archive imports are limited to 64 MiB.
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
| `GET` | `/health` | None | Readiness, storage health, and protocol version |
| `GET` | `/health/live` | None | Process liveness |
| `GET` | `/health/ready` | None | Readiness, storage health, and protocol version |
| `POST` | `/v1/admin/shutdown` | Admin | Accepted shutdown request |
| `GET` | `/v1/admin/scopes/{scopeId}/export` | Admin | Portable scope archive |
| `POST` | `/v1/admin/scopes/import` | Admin | Imported scope and one-time scope token |
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
| `POST` | `/v1/tasks/{taskId}/progress` | Claiming execution | Appended task progress |
| `GET` | `/v1/tasks/{taskId}/progress` | Scope or agent | Ordered task progress history |
| `POST` | `/v1/escalations` | Agent | New escalation |
| `GET` | `/v1/escalations/{escalationId}` | Agent | Escalation in the scope |
| `GET` | `/v1/scope/escalations` | Scope | Escalations in the scope |
| `POST` | `/v1/scope/escalations/{escalationId}/resolve` | Scope | Resolved escalation |
| `GET` | `/v1/scope/storage` | Scope | Counts, estimated bytes, and oldest timestamps |
| `POST` | `/v1/scope/storage/prune` | Scope | Dry-run or executed retention result |
| `GET` | `/v1/events` | Scope | Resumable scope event batch |
| `POST` | `/v1/a2a/publications` | Scope | New Agent Card publication |
| `GET` | `/v1/a2a/publications` | Scope | Agent Card publications in the scope |
| `POST` | `/v1/a2a/publications/{publicationId}/enable` | Scope | Enabled publication |
| `POST` | `/v1/a2a/publications/{publicationId}/disable` | Scope | Disabled publication |
| `POST` | `/v1/a2a/principals` | Scope | New principal and one-time credential |
| `GET` | `/v1/a2a/principals` | Scope | Remote A2A principals without credentials |
| `GET` | `/v1/a2a/principals/usage` | Scope | Per-principal unfinished inbound usage and limits |
| `POST` | `/v1/a2a/principals/{principalId}/rotate` | Scope | Principal and replacement credential |
| `POST` | `/v1/a2a/principals/{principalId}/enable` | Scope | Enabled principal |
| `POST` | `/v1/a2a/principals/{principalId}/disable` | Scope | Disabled principal |
| `POST` | `/v1/output-streams` | Scope | New output stream |
| `GET` | `/v1/output-streams` | Scope | Output streams in the scope |
| `GET` | `/v1/output-streams/{streamId}` | Scope | Output stream metadata |
| `DELETE` | `/v1/output-streams/{streamId}` | Scope | Removed output stream, values, and principals |
| `PUT` | `/v1/output-streams/{streamId}/publishers/{agentId}` | Scope | Authorized agent publisher |
| `DELETE` | `/v1/output-streams/{streamId}/publishers/{agentId}` | Scope | Removed agent publisher |
| `POST` | `/v1/output-principals` | Scope | New output principal and one-time credential |
| `GET` | `/v1/output-principals` | Scope | Output principals without credentials |
| `POST` | `/v1/output-principals/{principalId}/rotate` | Scope | Principal and replacement credential |
| `POST` | `/v1/output-principals/{principalId}/enable` | Scope | Enabled output principal |
| `POST` | `/v1/output-principals/{principalId}/disable` | Scope | Disabled output principal |
| `POST` | `/outputs/{streamId}/values` | Agent or scoped publish | Published output value |
| `GET` | `/outputs/{streamId}/values` | Scope or scoped read | Ordered output history |
| `GET` | `/outputs/{streamId}/latest` | Scope or scoped read | Latest output value or `null` |
| `GET` | `/a2a/agents/{publicationId}/.well-known/agent-card.json` | None | Enabled A2A Agent Card |
| `POST` | `/a2a/agents/{publicationId}/message:send` | Scoped A2A | Durable A2A Task |
| `POST` | `/mcp` | Agent | MCP Streamable HTTP endpoint |

`/health/live` returns HTTP 200 while the server process can answer requests. `/health` and `/health/ready` return HTTP 200 only when the runtime can reach its storage backend. An unavailable backend returns HTTP 503 with `status: not_ready`. Health responses expose the backend name and availability, never its address or credentials.

`POST /v1/inbox/reserve` accepts an optional `limit` from 1 through 100; omission or 0 selects the default of 50. It also accepts an optional `waitMs` value from 0 through 25000. When no message is immediately reservable, a positive value waits until work arrives, the wait expires, the request is canceled, the server stops, or the execution loses authority. The default is 0 and returns immediately. A successful timeout returns `null` and does not reserve a message.

`GET /v1/tasks?ready=true` returns only open, unclaimed tasks whose dependencies are complete. The default returns every task in the scope.

`POST /v1/scope/storage/prune` requires an RFC 3339 `before` timestamp. Omitted or false `execute` performs a dry run. `execute=true` removes the reported terminal records in one transaction.

`GET /v1/events?after=0&limit=50&waitMs=25000` returns events after the supplied scope revision. The limit is 1 through 100 and the bounded wait is 0 through 25000 milliseconds. The default cursor is 0, the default limit is 50, and the default wait returns immediately. Event envelopes contain identifiers and state metadata, not message bodies, task text, progress text, escalation questions, answers, output values, references, or credentials.

Clients resume from `nextRevision`. `minimumCursor` is the oldest cursor that can still produce a complete continuation. A batch with `resyncRequired: true` means retention removed events needed by the supplied cursor. The client must rebuild its projection from the resource APIs and resume from the returned `nextRevision`.

Agent Card publications are absent by default. A scope owner publishes one registered agent by sending its exact `agentId`. The returned opaque publication ID and URLs remain stable while the publication is disabled and re-enabled. Public card requests for unknown and disabled IDs return the same `NOT_FOUND` response. Card and interface URLs come from the runtime's trusted address configuration, never the request `Host` header.

`POST /v1/a2a/principals` accepts a publication ID and label. Create and rotate responses are the only responses that contain the bearer credential. List, enable, and disable responses return principal metadata only. A principal credential is restricted to its publication and cannot authenticate to any `/v1` or `/mcp` operation.

`GET /v1/a2a/principals/usage` returns unfinished message counts, text bytes, and effective limits for every A2A principal in the scope. It does not return message content. Terminal tasks and undelivered expired requests do not consume capacity.

`POST /a2a/agents/{publicationId}/message:send` implements A2A 1.0 HTTP+JSON `SendMessage`. It accepts user messages made only of plain text parts and returns a durable A2A Task. The bearer credential must belong to the requested publication. The optional `A2A-Version` header must contain exactly `1.0`. Other A2A operations and content types return A2A protocol errors.

`POST /outputs/{streamId}/values` accepts `contentType`, `value`, and an optional URI reference. Agent credentials require an explicit publisher grant. Scoped output credentials require `publish` permission. `GET /outputs/{streamId}/values?after=0&limit=50` returns ordered values after the cursor. The limit is 1 through 100. Clients use `nextSequence` as their next cursor and rebuild from the latest value when `resyncRequired` is true.

Output credentials are bearer credentials and MUST be sent in the `Authorization` header. Credentials in query strings are not accepted. Browser CORS policy is deployment configuration rather than part of the protocol.

Scope export and import use the archive rules in [archives.md](archives.md). Export rejects a scope with an active agent execution. Import validates and applies the full archive in one transaction. A successful retry returns `imported=false` and does not return the original one-time scope token again.

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
