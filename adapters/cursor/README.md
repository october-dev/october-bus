# Cursor adapter

Status: early integration, not yet conformance-verified.

Start October Bus, then create a scope. Copy or merge the example into `.cursor/mcp.json` in the project where Cursor will run. It launches the stdio bridge inside the managed agent execution.

Run Cursor through the managed agent command:

```sh
export OCTOBER_BUS_SCOPE_TOKEN="<scope token>"

october-bus agent run \
  --id cursor \
  --name Cursor \
  --connect-to codex \
  --capability coding \
  -- cursor-agent --approve-mcps
```

The wrapper gives Cursor only its execution-scoped agent token. It owns heartbeat and marks the execution offline when Cursor exits. It does not infer model readiness from the process alone.

Cursor may ask the user to approve the MCP server or individual tools. Review those prompts according to the agent's permissions and scope.
