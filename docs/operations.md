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
context — only the receipt.

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
