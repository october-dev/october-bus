# Core surface inventory before the stable freeze

This inventory maps the agent-facing core. Scope/admin operations are intentionally not exposed to models. The authoritative wire contracts remain [HTTP](../spec/0.1/http.md), [MCP](../spec/0.1/mcp.md) and [JSON Schemas](../spec/0.1/schemas); Go and TypeScript client methods wrap the HTTP contract.

| MCP tool | Go / TypeScript client | Wire shape / boundary |
| --- | --- | --- |
| `list_peers` | `ListPeers` / `listPeers` | MCP wraps the array in `peers` |
| `message_peer` | `SendMessage` / `sendMessage` | `peer` → `to`, `message` → `body`; MCP also resolves unique exact display names |
| `message_receipt` | `Receipt` / `receipt` | `messageId`; delivery state and optional linked response, not message contents |
| `check_inbox` | reserve + commit / `pullInbox` | MCP wraps messages and acknowledgement guidance; explicit ack remains separate |
| `acknowledge_messages` | `AcknowledgeMessages` / `acknowledgeMessages` | Array `messageIds`; MCP wraps count in `acknowledged` |
| `add_task` | `AddTask` / `addTask` | Required title; optional description and dependency IDs |
| `claim_task` | `ClaimTask` / `claimTask` | Task ID; claim belongs to current execution |
| `release_task` | `ReleaseTask` / `releaseTask` | Task ID; only claim owner can release |
| `complete_task` | `CompleteTask` / `completeTask` | Task ID and optional note |
| `add_task_progress` | `AddTaskProgress` / `addTaskProgress` | Task ID, kind and text; claimed execution required |
| `list_task_progress` | `ListTaskProgress` / `listTaskProgress` | MCP wraps history in `progress` |
| `list_tasks` | `ListTasks` / `listTasks` | MCP wraps array in `tasks`; optional ready filter |
| `publish_output` | `PublishOutput` / `publishOutput` | Explicit stream authority; JSON/text value and optional reference |
| `ask_user` | `AskHuman` / `askHuman` | Creates escalation only; it cannot resolve human permission |
| `get_node_status` | `NodeStatus` / `nodeStatus` | Current identity, lease and lifecycle |

The bridge preserves the daemon's tool schemas and structured results. Pi prefixes native tool names `october_bus_` without renaming protocol fields. Only the stdio boundary coerces a string into a schema-required array/object; direct HTTP and daemon MCP remain strict. Go schema/authority tests and SDK integration cover these differences.

Registration, linking, lease renewal, retirement and human-escalation resolution use authenticated HTTP/client operations outside the model loop. Event cursors, paging, output-reader principals, A2A publications and admin recovery are HTTP extensions, not missing core MCP tools. They retain their own authority checks and schemas. Do not give scope/admin tokens to a model to fill a surface gap.

Portable scope archives omit reusable credentials and execution authority; import returns new scope authority only once. Full SQLite backup preserves sensitive stored authority and requires a separate restore/rotation policy. Neither is an alternate wire version of an agent message.

Before freezing 1.0, review request/response/event nullability, limits, error codes and archive behavior against the schemas and migrations. Same-release runtime/SDK pairing is policy; retirement-feature checks and schema refusal are narrower compatibility checks, not exact-version enforcement.
