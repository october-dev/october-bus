# Local runtime operations

October Bus runs as one local daemon. The reference profile binds to `127.0.0.1` and stores accepted work in SQLite.

## Start and stop

```bash
october-bus start
october-bus status
october-bus doctor
october-bus stop
```

`stop` sends an authenticated shutdown request through the local endpoint. The admin token stays in the protected run file and is not passed on the command line.

Use `october-bus doctor --json` for machine-readable diagnostics. It reports versions, paths, process state, and endpoint health. It does not print credentials or message content.

## MCP over stdio

Harnesses that need a local stdio MCP server can run:

```bash
october-bus mcp stdio
```

The bridge reads `OCTOBER_BUS_ADDRESS` and `OCTOBER_BUS_AGENT_TOKEN`, discovers the daemon's MCP tools, and forwards calls without keeping its own state. `october-bus agent run` supplies both values to the managed harness process. If either value is absent, the bridge starts without tools and does not contact a daemon.

## Inspect message delivery state

Agents can inspect the durable delivery state of a message they sent or
received without opening SQLite or writing a client program. The command
requires the agent credential and never reveals message bodies or shared
context, only the receipt.

```bash
october-bus message receipt <message-id> [--json] [--address <addr>]
```

The credential is read from `OCTOBER_BUS_AGENT_TOKEN`. The daemon address is
resolved from `--address`, then the `OCTOBER_BUS_ADDRESS` environment
variable, then the local run file. The output shows the current delivery
state plus any timestamps that have been recorded (`accepted`, `delivered`,
`acknowledged`, `replied`) and, when present, the linked response message
ID. Use `--json` for a stable machine-readable form.

## Inspect scope agents

A scope owner can list the agents registered in a collaboration scope without
calling the HTTP API directly. The command exposes agent metadata only. It does
not expose credentials or message contents.

```bash
october-bus agent list [--json] [--address <addr>]
```

The credential is read from `OCTOBER_BUS_SCOPE_TOKEN`. The daemon address is
resolved from `--address`, then `OCTOBER_BUS_ADDRESS`, then the local run file.
The output includes each agent's id, display name, lifecycle, readiness,
reachability, capabilities, and last update time. Results are sorted by agent
id. Use `--json` for machine-readable output.

## Project task board

A scope owner can add work and inspect dependency-ready tasks:

```bash
export OCTOBER_BUS_SCOPE_TOKEN=<scope-token>
october-bus task add --title "Review checkout retries" --description "Check idempotency and error handling."
october-bus task list --ready
```

Claims, progress updates, release, and completion require an execution-bound agent credential. Task listings include the most recent progress, notes, and blockers so a later agent or user can continue from durable state.

## Storage and retention

Scope owners can inspect storage growth without reading message, task, or escalation content:

```bash
export OCTOBER_BUS_SCOPE_TOKEN=<scope-token>
october-bus scope storage
```

The summary groups record counts and estimated payload bytes by state. It also reports the oldest state timestamp. Payload sizes are estimates and do not include SQLite indexes or other database overhead.

Retention is explicit and keeps indefinite storage as the default. First run a dry run:

```bash
october-bus scope prune --before 2026-08-01T00:00:00Z
```

Pass `--yes` to remove the reported records in one transaction. Only terminal messages, completed tasks, resolved escalations, and old scope events can be removed. Requests and responses are removed together. Work with an outstanding delivery, reply, task, dependency, or human obligation is preserved.

Pruning scope events can make an old event cursor incomplete. Event clients receive `resyncRequired` and must rebuild their projection from the resource APIs before continuing.

Agent Card publications and remote principals are configuration records and are not removed by retention. Disable a publication to stop serving its public card and reject its principals. Disable an individual principal to suspend only that caller.

Inbound A2A work is limited independently for each remote principal. The defaults are 1,000 unfinished messages and 16 MiB of unfinished text. Set `OCTOBER_BUS_A2A_PRINCIPAL_MESSAGE_LIMIT` and `OCTOBER_BUS_A2A_PRINCIPAL_BYTE_LIMIT` before starting the daemon to choose stricter limits. The message limit must be from 1 through 9,999. The byte limit must be from 1 through 655,294,464.

Use the scope client `ListA2APrincipalUsage` method or `GET /v1/a2a/principals/usage` to inspect current usage. The result contains identifiers, counts, bytes, and limits, but no message content.

Output streams apply their own bounded retention on every publication. The default is 1,000 values and the owner can select 1 through 10,000 when creating a stream. Removing a stream also removes its values and scoped principals.

Browser output access is disabled unless the request origin is explicitly configured. Set a comma-separated exact allowlist before starting the daemon:

```sh
OCTOBER_BUS_ALLOWED_ORIGINS=http://127.0.0.1:8080,https://dashboard.example october-bus start
```

PowerShell uses the same setting:

```powershell
$env:OCTOBER_BUS_ALLOWED_ORIGINS = "http://127.0.0.1:8080,https://dashboard.example"
october-bus start
```

The Bus never accepts output credentials in query strings. A server-to-server request without an `Origin` header is not affected by browser CORS configuration.

Choose a cutoff older than the longest client retry window you support. Removing a message also removes its idempotency-key binding.

## Backup, restore, and migration

Stop or disconnect every agent in a scope before exporting it:

```bash
october-bus scope export --id my-project --output my-project.bus.json
```

Archive files contain message bodies, context, task details, escalation answers, and output values. They do not contain reusable Bus credentials, but they can still hold sensitive project data. The CLI creates a new archive with user-only permissions and refuses to overwrite an existing file.

Import the archive into another compatible runtime:

```bash
october-bus scope import --input my-project.bus.json
```

The command prints the new scope token only on the first successful import. Store it securely. Imported agents are offline, active task claims are open, and Agent Card publications are disabled. Register agents again and review publications before enabling them.

For a remote runtime, set `OCTOBER_BUS_ADMIN_TOKEN` and pass `--address`. Importing the exact same archive again is safe and does not duplicate state.

## Default paths

| Platform | Data directory | Runtime directory |
| --- | --- | --- |
| Windows | `%LOCALAPPDATA%\October Bus` | Per-user temporary directory |
| macOS | `~/.local/share/october-bus` | Per-user temporary directory |
| Linux | `$XDG_DATA_HOME/october-bus` or `~/.local/share/october-bus` | `$XDG_RUNTIME_DIR/october-bus` or a per-user temporary directory |

Set `OCTOBER_BUS_DATA_DIR` and `OCTOBER_BUS_RUNTIME_DIR` to override these paths. The data directory contains `bus.db`. The runtime directory contains the current lock and credential-bearing run file.

Do not share the runtime directory. Back up the data directory only while the daemon is stopped until online backup support is documented.

## Supervision

The daemon handles interrupt and termination signals and shuts down active HTTP requests before closing SQLite. A service supervisor should restart unexpected exits with bounded backoff.

The repository does not yet ship platform service definitions. Keep credentials out of service arguments and logs when adding one.

## Recovery

Accepted messages and active tasks survive a normal daemon restart. If startup reports an invalid database schema or integrity problem, preserve the database before attempting repair. Automated migration and repair tools are planned before the first stable release.
