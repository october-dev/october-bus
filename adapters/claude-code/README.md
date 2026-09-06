# Claude Code adapter

Status: experimental, not yet conformance-verified. In the [reported verification attempt](../../compatibility/observations/claude-code-2.1.251-macos-arm64.md), Claude Code 2.1.251 was installed but not authenticated (`claude auth status` → `loggedIn: false`), so the real-harness compatibility RUNBOOK was not executed. Authentication blocked that attempt, not all users. This is an attempt note, not named-harness evidence; other versions and platforms remain unverified.

Start October Bus, then create a scope. The MCP configuration launches the stdio bridge inside the managed agent execution.

Run Claude Code through the managed agent command:

```sh
export OCTOBER_BUS_SCOPE_TOKEN="<scope token>"

october-bus agent run \
  --id claude-code \
  --name "Claude Code" \
  --capability coding \
  -- claude --strict-mcp-config --mcp-config adapters/claude-code/mcp.json.example
```

The wrapper gives Claude Code only its execution-scoped agent token. It owns heartbeat and marks the execution offline when Claude Code exits. It does not infer model readiness from the process alone.
