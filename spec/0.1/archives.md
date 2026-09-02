# Portable scope archives

Portable scope archives move durable collaboration state between compatible October Bus implementations. The archive format is JSON and is defined by [scope-archive.schema.json](schemas/scope-archive.schema.json).

An archive preserves:

- the scope ID and creation time;
- stable agent identities, capabilities, and peer links;
- messages, context, correlations, idempotency keys, receipts, and terminal delivery state;
- shared tasks, dependencies, ownership of completed tasks, and progress history;
- human escalations and answers;
- Agent Card publication identities;
- output streams, publisher assignments, retained values, and cursors.

An archive never contains scope tokens, agent tokens, scoped credentials, credential hashes, execution IDs, leases, reservations, rate-limit counters, or host process evidence.

## Safe restore state

Export requires every current agent execution to be offline, expired, or otherwise stopped. A reservation is stored as `queued` when it has never been delivered and `delivered` when it represents a redelivery. A claimed task is stored as `open`. These rules prevent an execution lease or transient claim from moving to another runtime.

Imported agents are offline and receive no usable agent credential. Registering an imported agent creates a new execution and credential. Imported Agent Card publications are disabled. Their owner can review and enable them after restore.

The event log is local projection history and is not portable. Import creates a new event stream containing one `scope.imported` event. Event consumers must rebuild their projection from the restored resources.

Scoped A2A and output principals are not portable. Create new principals after import. Output values written by an old principal retain its opaque producer ID as historical attribution, but that ID grants no authority.

## Atomic and idempotent import

An implementation must validate the complete archive before applying it. Unsupported versions, unknown fields, broken references, malformed values, and conflicting IDs must fail without changing durable state.

Import is atomic. Retrying the exact same archive returns the existing scope without adding records. The new scope token is returned only when the import is first applied. Operators must save it immediately.

The reference runtime retains the metadata-only import marker so retries remain idempotent after normal event retention.

The initial archive identifier is:

```text
format: october-bus.scope
version: 1
```

New archive versions require an explicit reader implementation. Readers must reject versions they do not support.
