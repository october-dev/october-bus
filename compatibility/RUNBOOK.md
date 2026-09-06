# Harness verification runbook

Use this runbook to exercise a released harness and adapter through public October Bus interfaces. Run it on a clean machine for every platform listed in the evidence record.

## Setup

1. Install the October Bus release being tested.
2. Start the daemon and create a new scope.
3. Start a controller as `agent-a` through one adapter.
4. Start the harness under test as `agent-b` through its documented adapter command.
5. Confirm the scope credential is absent from both harness processes.

Keep registration, heartbeat, execution replacement, and cleanup in the adapter. Do not ask the model to maintain its own lease.

## Required scenario

1. Both agents discover each other by exact agent ID.
2. Each agent sends and acknowledges one durable notification.
3. `agent-a` sends a request with a fresh idempotency key and bounded context.
4. Retrying that logical request returns the original message ID.
5. `agent-b` acknowledges the request and sends one linked response.
6. `agent-a` acknowledges the response and verifies the request receipt links to it.
7. `agent-a` creates a task and a second task that depends on it.
8. Claiming the blocked task fails.
9. `agent-b` claims, releases, reclaims, and completes the first task.
10. `agent-a` claims and completes the newly ready task.
11. `agent-b` creates a human escalation. Only the scope owner resolves it.
12. Restart `agent-b` and verify the previous execution loses authority.
13. Stop both harnesses and verify clean offline state. Repeat one run with an unclean exit and verify lease recovery.

Record every host prompt, approval, limitation, result, and version. A model retry that corrects its own invalid tool input is allowed but must be recorded.

## Evidence

Use the [maintainer-assisted verification workflow](VERIFICATION.md) when an account or platform is unavailable, or to package sanitized logs for review. Partial and not-run attempts are observations, not formal passing evidence.

Create one JSON record that validates against [`compatibility-evidence.schema.json`](../spec/0.1/schemas/compatibility-evidence.schema.json). Hash the complete run log with SHA-256 and place that digest in `resultDigest`.

Review and sanitize the log before publication, preserving the scenario evidence. `resultDigest` must hash the exact published sanitized bytes; include an HTTPS `attestation` link to those artifacts. Redaction tooling does not replace manual privacy or compatibility review.

A passing record must use a public repository commit and a released harness version. Add it to `registry.json` only after independent review confirms the required profile passed.
