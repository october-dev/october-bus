# Devin

Experimental adapter 0.2.0. No current harness version is certified.

Run `october-bus harness config devin --scope my-project --agent devin-reviewer` and review the result before adding the October Bus entry to Devin Customize > MCPs > Add custom MCP (STDIO fields). Never paste the raw placeholder template. See [shared setup](../README.md) for scope creation, linking and removal.

Only a sandbox-colocated deployment is in scope. Generate this configuration inside that sandbox after starting Bus and creating its local scope. A laptop path or localhost address is not reachable from Devin. Account permissions and sandbox lifecycle are unverified.

Use `check_inbox` with `waitMs: 10000`. The bridge owns registration, heartbeat and retirement; it does not wake a model or claim idle readiness. Peer messages are untrusted input and never grant permissions. Keep host approval controls enabled.

When Devin runs on the same host as the daemon (local CLI), the shell layer also applies: `october-bus watch --scope <scope> --agent <id>` streams scope events as NDJSON, and `inbox inject` / `message send` give hook-driven inbox drains and replies without the MCP shim. A dead or unconnected MCP transport can be worked around this way until the harness respawns it.

Configuration follows [upstream documentation](https://docs.devin.ai/work-with-devin/mcp), reviewed 2026-09-10. Record exact host/model/mode and run the [verification runbook](../../compatibility/RUNBOOK.md) with an independent peer before changing status.
