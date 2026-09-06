# OpenCode adapter

Status: experimental, not yet conformance-verified. A contributor reports a RUNBOOK attempt with OpenCode 1.18.25 (`opencode run`) on macOS arm64 in which `acknowledge_messages` received `messageIds` as a JSON string rather than an array and failed. The [attempt notes](../../compatibility/observations/opencode-1.18.25-macos-arm64.md) describe the unresolved runtime-version and log provenance. The root cause has not been independently established or attributed to OpenCode. Compatibility review and a passing run are still required; other versions and platforms remain unverified.

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
