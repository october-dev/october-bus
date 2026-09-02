# October Bus protocol 0.1

Status: draft

October Bus is a communication and coordination protocol for AI agents and harnesses. It defines identity, discovery, durable messages, shared tasks, and human escalation. It does not select agents, staff teams, route models, or supervise an overall operation.

## Model

```text
scope
├── agent identity
│   └── current execution and lease
├── authorized peer links
├── durable messages and receipts
├── shared tasks and dependencies
└── human escalations
```

### Scope

A scope is one collaboration boundary. Agents, peer links, messages, tasks, and escalations never cross scopes.

A scope token MAY register agents, list all agents, create peer links, add and list shared tasks, and resolve human escalations. It MUST NOT be given to an agent process when an execution-bound agent token is sufficient.

### Agent and execution

An agent ID is the stable programmatic identity of one participant inside a scope. It MUST match:

```text
^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$
```

Agent IDs are byte-exact and case-sensitive. `Reviewer` and `reviewer` are different agents.

Each registration creates a new execution ID and agent token. Registering the same agent ID replaces its current execution. The previous token MUST stop authorizing requests.

An execution has a renewable lease between 30 seconds and 24 hours. The default is five minutes. An expired execution is unreachable and cannot act. A launcher SHOULD heartbeat at roughly one third of the lease duration.

### Presence and lifecycle

Reachability, readiness, and lifecycle are separate facts:

- `reachable` means the execution lease is current and the execution is not offline;
- `ready` means the adapter has evidence that the agent can accept work;
- `lifecycle` is one of `starting`, `ready`, `working`, `idle`, `needs_input`, or `offline`.

An adapter MUST report only states it can prove. A generic process launcher MAY report reachability but SHOULD leave `ready=false` until a host signal proves readiness.

An `offline` heartbeat MUST set `ready=false`.

### Capabilities

An agent MAY declare up to 64 capabilities. Capability names use the agent-ID character rules. Names MUST be unique within one declaration. Descriptions are optional and limited to 512 bytes.

Capabilities describe what an agent claims it can do. They do not grant authority.

## Peer discovery and addressing

Agents communicate only across explicit peer links. A link is symmetric.

Protocol clients MUST address a peer by its exact agent ID. An interactive adapter MAY accept a display name as a convenience if it applies these rules in order:

1. resolve a byte-exact agent ID first;
2. otherwise resolve a case-insensitive exact display name only when it identifies one linked peer;
3. reject duplicate exact display names with `CONFLICT`;
4. reject unknown and partial names without guessing.

A peer can remain discoverable while offline. Clients MUST inspect `reachable` and `ready` instead of treating discovery as availability.

## Messages

Message modes are `notify`, `request`, and `response`. An omitted mode means `notify`.

A successful send means the runtime durably accepted the message. It does not mean the recipient read or processed it.

Delivery states are:

```text
queued -> reserved -> delivered -> acknowledged
   └──────────────────────────────> expired
```

Reservations prevent two concurrent delivery attempts from consuming the same inbox item. A reservation expires after 30 seconds in the reference runtime. Releasing or expiring a reservation makes an undelivered message available again. Delivered but unacknowledged messages MAY be redelivered.

An inbox reservation request MAY wait for work with `waitMs`. The reference profile allows values from 0 through 25000. The wait ends when work becomes reservable, its deadline passes, the caller cancels, the server stops, or the execution loses authority. A timeout returns no reservation and does not consume work.

Acknowledgement means the recipient reports that it processed the message. Clients SHOULD acknowledge only after processing succeeds.

### Idempotency

An idempotency key is scoped to one sender inside one scope. Retrying the same logical send with the same key and identical content MUST return the original receipt. Reusing the key with different content MUST return `CONFLICT`.

Keys are permanent for the sender. SDKs SHOULD generate a new UUID for every logical send and MUST NOT recycle keys.

### Requests and responses

A response MUST name one delivered request in `responseTo`. The response sender and recipient MUST be the reverse of the request. One request accepts at most one response.

A request that expires before delivery cannot receive a response. A request delivered before expiry MAY receive one response after expiry. Its receipt then reports both expiry and the linked response.

### Expiry and durability

`expiresInMs=0` means no protocol expiry. A positive expiry can be at most 30 days. Expiry is materialized before delivery, reservation release, reservation commit, and receipt reads. An expired message cannot be delivered or resurrected.

The reference runtime limits each scope to 10,000 messages that are neither acknowledged nor expired. It returns `BACKPRESSURE` when the limit is reached. Operators remain responsible for diagnostics and retention policy.

### Bounded context

A message MAY include up to 32 context items and 256 KiB of total context metadata and content. Supported kinds are `text`, `file`, `url`, and `reference`.

A context item is a description or explicit payload. It does not grant access to a file, URL, tool, account, or host.

## Shared tasks

A task has a short title and an optional description. It is `open`, `claimed`, or `done`, and MAY depend on existing tasks in the same scope. Scope authority and agents can add and list tasks. A task created by scope authority has a null `createdBy` value.

An open task is ready when it is unclaimed and every dependency is done. Clients MAY request only ready work when listing tasks.

An agent can claim an open task only after every dependency is done. A claim belongs to the current execution, not only the logical agent. Only that execution can release or complete it.

The claimant MUST keep its execution lease current. When an execution expires or is replaced, the reference runtime lazily releases its stale claims so another execution can claim them.

The reference runtime limits a scope to 5,000 tasks that are not done.

## Human escalation

An agent MAY create an escalation with a question and either no options or two to four options. Creating an escalation does not grant the agent permission or answer the question.

Only scope authority resolves escalations. Agent authority can create and read escalations in its scope but cannot resolve them. The reference runtime limits pending escalations to 100 per agent and 1,000 per scope.

## Authority

| Credential | Allowed operations |
| --- | --- |
| Admin token | Create a scope and request local daemon shutdown |
| Scope token | Register and list agents, create peer links, add and list tasks, list and resolve escalations |
| Agent token | Heartbeat, discover peers, message linked peers, use inboxes, coordinate tasks, create and read escalations |

Tokens are bearer credentials. Implementations MUST compare them safely, MUST NOT log them, and MUST reject empty credentials. Agent tokens MUST stop working after lease expiry or execution replacement.

Messages do not expand authority. A recipient MUST still apply its own permissions before using tools or changing external state.

## Errors

Protocol error codes are:

- `INVALID_ARGUMENT`
- `UNAUTHENTICATED`
- `PERMISSION_DENIED`
- `NOT_FOUND`
- `METHOD_NOT_ALLOWED`
- `CONFLICT`
- `BACKPRESSURE`
- `INTERNAL`

Clients SHOULD branch on the code, not the human-readable message.

## Limits and ordering

Text limits in the reference runtime are measured in UTF-8 bytes. JSON Schema string limits are a portable approximation because JSON Schema measures characters.

The reference runtime returns peers by registration time, tasks by creation time, and messages by creation time. Clients MUST NOT use this ordering as a fairness or scheduling guarantee.

Protocol 0.1 has no pagination, extension negotiation, or mixed-version negotiation. Implementations MUST reject unsupported protocol versions rather than silently changing semantics.

## Profiles

### Local runtime profile

Requires the HTTP API, durable persistence, loopback binding by default, all authority rules, and every semantic in this document.

### MCP adapter profile

Requires the local runtime profile plus the tool mapping in [mcp.md](mcp.md). Registration, token handoff, heartbeat, and cleanup remain adapter responsibilities outside the model loop.

### Native adapter profile

Uses the same semantics through public APIs and MAY add host hooks for stronger lifecycle and wake evidence. Native hooks cannot weaken authority, durability, or acknowledgement rules.

## Related documents

- [HTTP API](http.md)
- [MCP mapping](mcp.md)
- [Adapter contract](adapters.md)
- [JSON Schemas](schemas/protocol.schema.json)
