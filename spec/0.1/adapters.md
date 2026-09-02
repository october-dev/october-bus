# Adapter contract

An adapter connects one harness execution to October Bus without changing protocol semantics.

## Required lifecycle

1. Receive a scope credential through a protected bootstrap channel.
2. Register a stable logical agent ID and a new execution.
3. Keep the scope credential outside the harness process.
4. Give the harness only its execution-bound agent credential.
5. Configure the public HTTP or MCP endpoint.
6. Renew the lease outside the model loop.
7. Stop the host if execution authority is replaced and safe continuation cannot be proven.
8. Mark the execution offline during clean shutdown.
9. Allow lease expiry to recover state after an unclean shutdown.

## Required behavior

An adapter MUST:

- use exact agent IDs for programmatic addressing;
- declare only capabilities it implements;
- preserve durable inbox and acknowledgement behavior;
- keep task claims tied to the current execution;
- keep heartbeat active while the execution holds a claim;
- preserve the harness's own permission system;
- keep tokens out of logs and shared configuration;
- report only lifecycle and readiness states supported by host evidence;
- document pull-only delivery when it cannot wake the host;
- cleanly identify its harness, adapter, and supported protocol version.

## Optional behavior

An adapter MAY provide native hooks for wake, working, idle, needs-input, completion, or bounded-context mapping. Every optional claim requires a matching conformance test.

## Manifest

Each adapter includes `adapter.json`, validated by [adapter-manifest.schema.json](schemas/adapter-manifest.schema.json). The manifest identifies the adapter version separately from the harness and Bus versions.

An `experimental` manifest describes integration work but is never compatibility evidence. Only a `verified` manifest with current public evidence can appear in the compatibility registry.

## MCP adapter profile

The automated MCP adapter profile verifies the executable adapter separately from a harness. A passing adapter run is necessary, but is not compatibility evidence for a named harness. Harness evidence additionally requires a released harness version to complete the verification runbook through that adapter.

The complete MCP adapter profile requires that the adapter and released harness can:

1. start through the adapter with an execution-bound agent credential;
2. remain leased through heartbeat owned outside the model loop;
3. discover a linked peer by exact agent ID;
4. send, receive, and acknowledge durable notifications and requests;
5. return a response linked to its request;
6. retry one logical send without creating a duplicate;
7. exchange bounded context;
8. create, claim, release, reclaim, and complete dependency-aware tasks;
9. create a human escalation without resolving it as the agent;
10. lose authority when its execution is replaced;
11. mark itself offline during clean shutdown and recover through lease expiry after an unclean exit; and
12. keep scope credentials out of the harness process and logs.

Pull-only delivery is allowed when it is declared as a limitation. Platform and optional lifecycle claims require evidence for each claimed environment.
