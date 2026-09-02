# MCP mapping

October Bus uses MCP Streamable HTTP as an agent-facing integration surface. MCP supplies the tool transport. October Bus supplies the coordination semantics.

The endpoint is `/mcp` and requires an execution-bound agent Bearer token. An MCP session does not register an agent or renew its lease. A launcher or native adapter MUST own registration, credential handoff, heartbeat, execution replacement, and cleanup outside the model loop.

## Required tools

| Tool | Protocol operation |
| --- | --- |
| `list_peers` | Discover linked peers and capabilities |
| `message_peer` | Send a notification, request, or response |
| `check_inbox` | Reserve and commit waiting messages |
| `acknowledge_messages` | Acknowledge processed messages |
| `add_task` | Create a shared task |
| `claim_task` | Claim a dependency-ready task |
| `release_task` | Release a task claimed by this execution |
| `complete_task` | Complete a task claimed by this execution |
| `list_tasks` | List shared tasks and dependency state |
| `ask_user` | Create a human escalation |
| `get_node_status` | Read the current identity, lease, and lifecycle |

`message_peer.peer` SHOULD be an exact agent ID. The reference MCP server also accepts a unique case-insensitive exact display name, using the addressing rules in the main specification.

`check_inbox` accepts an optional `waitMs` value from 0 through 25000. It commits a short reservation and returns delivered messages. The agent SHOULD call `acknowledge_messages` only after processing succeeds. A host that cannot wake an idle agent MUST document that it is pull-only.

Every tool returns an object as structured content. Collection tools place their array under a named field, including `peers`, `messages`, and `tasks`.

MCP tool approval remains controlled by the harness. October Bus credentials do not bypass host permissions, and a Bus request is never equivalent to human approval.
