# OpenCode adapter

Status: early integration, not yet conformance-verified.

Start October Bus, then create a scope. Set `OPENCODE_CONFIG` to the example or merge its `mcp` entry into the project's OpenCode configuration. It launches the stdio bridge inside the managed agent execution.

Run OpenCode through the managed agent command:

```sh
export OCTOBER_BUS_SCOPE_TOKEN="<scope token>"
export OPENCODE_CONFIG="adapters/opencode/opencode.json.example"

october-bus agent run \
  --id opencode \
  --name OpenCode \
  --connect-to codex \
  --capability coding \
  -- opencode
```

The wrapper gives OpenCode only its execution-scoped agent token. It owns heartbeat and marks the execution offline when OpenCode exits. It does not infer model readiness from the process alone.
