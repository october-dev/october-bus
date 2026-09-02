<div align="center">

<pre aria-label="OCTOBER">
  ___   ____ _____ ___  ____  _____ ____
 / _ \ / ___|_   _/ _ \| __ )| ____|  _ \
| | | | |     | || | | |  _ \|  _| | |_) |
| |_| | |___  | || |_| | |_) | |___|  _ <
 \___/ \____| |_| \___/|____/|_____|_| \_\
</pre>

# October Bus

### Open communication and coordination for AI agents.

[![License](https://img.shields.io/badge/license-Apache%202.0-7C6CF0.svg)](LICENSE)
[![Protocol](https://img.shields.io/badge/protocol-agent%20interoperability-28B8D8.svg)](#protocol)
[![Runtime](https://img.shields.io/badge/runtime-local--first-3BAA6E.svg)](#security-and-trust)

</div>

---

**October Bus is an open communication and coordination layer for AI agents and harnesses.**

It gives agents a common way to discover peers, exchange messages, delegate work, coordinate shared tasks, and ask a human for help.

## Why it exists

People increasingly run several coding agents at once. The human often becomes the message bus, copying context between terminals and checking whether work was received or completed.

Agents should be able to coordinate directly while keeping their own tools, permissions, and context. October Bus provides the shared language for that coordination. It does not decide which agents to run or how to manage the overall operation.

> [!WARNING]
> **Project status:** October Bus is a new standalone project extracted from the coordination layer built for October. It is under active development and is not yet stable. The native Go daemon, TypeScript client, MCP tools, durable SQLite store, draft 0.1 specification, and conformance profiles are runnable. Codex CLI 0.152.1 is verified on macOS arm64. Early Claude Code, Cursor, and OpenCode configurations are included. Protocol and package interfaces may change before the first stable release.

## What agents can do

October Bus lets an agent:

- discover available peers and their declared capabilities;
- send durable messages and receive replies;
- share only the context needed for a task;
- delegate work without sharing an entire session;
- create, claim, block, and complete shared tasks;
- express dependencies between tasks;
- track delivery and acknowledgement;
- ask a human when work needs input or permission.

The Bus answers practical questions: Who else is here? What can they do? Can I send them this context? Did they receive my request? What work is blocked? Does a human need to decide?

## A concrete example

Claude Code is implementing a checkout change while Codex reviews it.

```text
Claude Code  -> discover peers
October Bus  -> Codex is available for review

Claude Code  -> request: Review the retry path in checkout.ts
Codex        -> claims the review task
Codex        -> response: The retry drops the idempotency key

Claude Code  -> receives the reply while still working
```

Codex receives the request and the bounded context needed for the review. It does not need Claude Code's full transcript.

The same pattern works when an analytics agent already has the user logs. An implementation agent can ask only for findings related to a bug instead of loading the analytics agent's entire history.

## How it fits together

```text
Agent or harness
       |
October Bus client or adapter
       |
October Bus
  identity | discovery | messages | tasks | human escalation
       |
Other agents | shared work | human
```

Harnesses keep their own model access, tools, sessions, and permission systems. The Bus provides the common coordination contract between them.

## October Bus and MCP

October Bus uses MCP where a harness supports it.

MCP provides useful tool, resource, and context primitives. October Bus uses those primitives to provide opinionated semantics for agent identity, peer discovery, messaging, delivery, shared tasks, lifecycle, and human escalation.

MCP can provide the integration surface. October Bus defines how collaborating agents behave on top of those primitives. Harnesses may also use native adapters when that is a better fit.

## Quickstart

Prebuilt release-candidate archives are available on the [releases page](https://github.com/october-dev/october-bus/releases). Download the archive for your operating system and architecture, verify it against `checksums.txt`, extract it, and place `october-bus` on your `PATH`.

Building October Bus from source requires Go 1.25 or newer.

```bash
git clone https://github.com/october-dev/october-bus.git
cd october-bus
go run ./cmd/october-bus demo
```

The demo starts an isolated local Bus and two example agents. One discovers the other, sends a durable request with bounded context, delegates a task, and receives a reply.

```text
planner  -> list_peers()
bus      -> builder [ready]

planner  -> message_peer(builder, mode=request, "Review the checkout flow")
bus      -> accepted as msg_01

planner  -> add_task("Review checkout flow")
bus      -> created as task_01

builder  -> claim_task(task_01)
builder  -> message_peer(planner, mode=response, responseTo=msg_01,
                         "The retry path drops the idempotency key")

planner  -> reply received
```

No October Desktop installation is required.

To start a persistent local daemon from source:

```bash
go run ./cmd/october-bus start
```

Check it or stop it cleanly from another terminal:

```bash
go run ./cmd/october-bus status
go run ./cmd/october-bus doctor
go run ./cmd/october-bus stop
```

In another terminal, create a collaboration scope:

```bash
go run ./cmd/october-bus scope create my-project
```

The command returns a scope token. A harness uses that token once to register an execution and receives a separate, execution-bound agent token. The TypeScript client lives in `sdk/typescript`. MCP clients can connect to the daemon's `/mcp` endpoint or spawn `october-bus mcp stdio` inside a managed execution.

Use the scope as a persistent project todo board:

```bash
export OCTOBER_BUS_SCOPE_TOKEN=<scope-token>
october-bus task add --title "Implement checkout retries"
october-bus task add --title "Review checkout retries" --depends-on <task-id>
october-bus task list --ready
```

Tasks survive daemon restarts. An agent joining the same scope can list ready work, claim one item atomically, and continue where another agent stopped.

## Protocol

The public draft specification, HTTP contract, MCP mapping, adapter contract, and JSON Schemas live in [`spec/0.1`](spec/0.1). Protocol versions are independent of runtime and SDK versions. See [Client SDKs](docs/clients.md) for Go and TypeScript usage.

| Primitive | What it means |
| --- | --- |
| Identity | A stable agent identity with authority bound to the current execution |
| Discovery | Peers can find each other and inspect declared capabilities |
| Presence | Existence, readiness, reachability, and lifecycle remain separate facts |
| Messaging | Durable notifications, requests, responses, inboxes, and receipts |
| Delegation | One agent can request bounded work from another agent |
| Shared tasks | People and agents can add work; agents can claim, release, complete, and depend on tasks |
| Context | Agents exchange explicit, bounded context instead of a global transcript |
| Human escalation | Agents can request input or permission without inventing authority |

### Delivery and replies

A send is accepted only after the local runtime has persisted it. A retry with the same idempotency key returns the original receipt. Keys remain bound to the original message for that sender, so clients should generate a new UUID for each logical send. Reusing a key with different content is rejected.

Requests open one reply obligation. Responses name the delivered request they complete. Expiry stops delivery attempts. A request delivered before its deadline may still receive one reply, and its receipt shows both the expiry and the late reply.

Delivery state is explicit. A message may be queued, reserved by one delivery attempt, delivered, acknowledged, or expired.

### Identity and lifecycle

A logical agent identity is not enough to act. The runtime checks the current execution token and lease. Re-registering an agent replaces its execution and retires the previous token. Task claims belong to that execution. A harness must heartbeat while it holds a claim, or the Bus may release the claim for another agent. Adapters remain responsible for reporting only readiness and lifecycle states they can prove.

Agent IDs are case-sensitive and exact. MCP tools may accept a unique exact display name for convenience, but adapters should address peers by agent ID.

## Integrating a new harness

A harness can support October Bus without using October Desktop.

1. **Register the harness.** Give each running agent a stable logical identity.
2. **Bind the execution.** Authenticate the current process or session with short-lived authority.
3. **Declare capabilities.** State how the harness receives work, reports readiness, and completes requests.
4. **Expose discovery.** Let the agent list peers and inspect their capabilities.
5. **Support messages.** Send notifications, requests, responses, and delivery acknowledgements.
6. **Support shared tasks.** Create, claim, release, complete, and inspect dependencies.
7. **Handle human escalation.** Surface requests for input or permission to the owning user.
8. **Report lifecycle.** Publish only states the harness can prove, such as working, idle, or needs input.
9. **Clean up.** Retire credentials, reservations, and execution state when the run ends.
10. **Run conformance.** Test every capability the adapter claims.

MCP is the simplest integration path for many harnesses. Native hooks or plugins are valid when they provide stronger lifecycle evidence.

If a harness cannot safely wake itself or prove that it is idle, it can implement pull-only delivery. Limited, honest support is better than claiming behavior the harness cannot guarantee.

## Compatibility

Codex CLI 0.152.1 is verified on macOS arm64. Early Claude Code, Cursor, and OpenCode configurations live in `adapters/`, but are not compatibility claims. The Omarchy service manifest is included as early integration work, but it has not been submitted to or validated by the Omarchy marketplace. The [compatibility registry](compatibility/README.md) lists only adapters with current passing evidence.

The conformance runner can start an isolated runtime and remove its state when the run finishes:

```bash
october-bus-conformance --start-runtime --format text
```

The `mcp-adapter` profile drives an adapter command over stdio while checking results independently through the public Bus API:

```bash
october-bus-conformance \
  --profile mcp-adapter \
  --start-runtime \
  --adapter-command october-bus \
  --adapter-arg mcp \
  --adapter-arg stdio \
  --format text
```

JSON is the default. Failed runs exit nonzero with the failed check recorded. An adapter command passing this profile does not by itself verify a harness. Harness compatibility also requires a released harness to pass the [verification runbook](compatibility/RUNBOOK.md). See [conformance profiles](docs/conformance.md) for details.

## Security and trust

October Bus is local-first. The reference runtime is designed to listen on loopback, persist local state durably, and avoid a required cloud control plane.

- **Identity is not permission.** Knowing an agent exists does not grant access to its process, files, tools, or context.
- **Authority belongs to one execution.** Live credentials are short-lived and retired when the process or session changes.
- **Peer requests cannot expand scope.** Receiving a message does not approve destructive work or bypass the harness's permission system.
- **Context is explicit and bounded.** An agent sees only context shared for the collaboration. Resource descriptions do not grant access to the resource.
- **Delivery is observable.** Acceptance, delivery, acknowledgement, and expiry are different states.
- **Human boundaries remain intact.** An agent can ask for input or permission, but it cannot answer on the user's behalf.
- **Remote transport needs separate trust.** A remote peer identity alone is not permission to control a process or device.

These are protocol and implementation requirements. The current tests cover the implemented local guarantees. The conformance suite will define the stable compatibility profiles.

## What October Bus is not

October Bus is not:

- an AI model;
- an autonomous supervisor;
- a model or harness router;
- an automatic team-staffing system;
- a shared-memory product;
- October Desktop;
- the complete October runtime.

The Bus makes interoperability cheap. October makes the resulting system easy and intelligent to operate.

## Built for October, open for everyone

October Bus started as the communication substrate underneath October. We are opening it because agent communication should not become another closed ecosystem.

Claude Code, Codex, Cursor, OpenCode, Grok, Gemini, Kimi, October Harness, and future harnesses should be able to coordinate without every product inventing an incompatible protocol.

October should remain the best place to use the Bus, but October Bus should not require October.

```text
October Bus
connect -> discover -> message -> delegate -> task -> acknowledge

October
understand goal -> choose team -> choose context -> build topology
-> execute -> observe -> adapt -> learn
```

October builds the visual runtime and control plane above the open substrate. It adds automatic staffing, harness and model selection, quota and cost awareness, context routing, supervision, cross-machine and team operation, apps, permissions, outcome learning, and Autopilot.

## Open-source boundary

| October Bus, open | October product, proprietary |
| --- | --- |
| Agent identity, presence, and discovery | Automatic staffing, role assignment, and topology creation |
| Capabilities, messages, inboxes, receipts, and replies | Harness ranking, model selection, quota routing, and cost optimization |
| Delegation, shared tasks, ownership, and dependencies | Operation planning, blueprint selection, and context-routing policy |
| Bounded context exchange and human escalation | Outcome scoring, performance history, benchmarking, and collective learning |
| Local authentication, execution authority, and delivery safety | October Cloud, managed compute, billing, entitlements, and enterprise controls |
| Protocol, local runtime, SDKs, adapters, examples, and tests | October Desktop's canvas, Kanban, apps, supervision, and complete control-plane UX |

The rule is simple:

> If a feature helps an agent become a good interoperable citizen, it probably belongs in the Bus. If it decides how an operation is staffed, routed, optimized, supervised, or learned from, it belongs in October.

## Coordination patterns

### Across repositories

An agent in a frontend repository can request an API change from an agent in a backend repository. Each agent keeps its own checkout and receives only the contract or task context it needs.

### Across machines

The protocol may support pluggable remote transports. Each machine remains responsible for authenticating its peers and authorizing its own processes.

October Cloud, managed cross-device permissions, and the commercial multiplayer control plane are not part of this repository.

## Roadmap

- Harden and version the standalone protocol and local reference implementation.
- Ship the first SDK, examples, and independently usable harness adapters.
- Expand the conformance suite with verified harness profiles.
- Validate and publish the headless Omarchy service integration.
- Add SDKs for more languages and runtimes.
- Define pluggable transport interfaces without coupling the protocol to October Cloud.
- Stabilize a versioned interoperability specification.

The detailed implementation plan lives in [ROADMAP.md](ROADMAP.md).

## Contributing

We welcome:

- adapters for new harnesses;
- protocol improvements;
- interoperability and conformance tests;
- examples for real coordination patterns;
- SDKs in other languages;
- fixes that make compatibility safer and easier to implement.

Keep contributions focused on interoperable agent communication. Product intelligence that staffs, routes, supervises, optimizes, or learns from a whole operation belongs above the Bus.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development, protocol, adapter, and pull request guidance. Report vulnerabilities through [the security policy](SECURITY.md).

## License

October Bus is licensed under the [Apache License 2.0](LICENSE).

The permissive license is intentional. Agent and harness developers, including commercial products, should be able to adopt and implement the protocol.

## Trademark

The Apache 2.0 license applies to the code and documentation in this repository. It does not grant rights to October's names, logos, or other brand assets.

You may use “October Bus compatible” to describe an implementation that passes the applicable conformance profile. Do not imply endorsement by October. Use a distinct name and branding for modified distributions unless you have written permission.

---

<div align="center">

Built in the open by [October](https://october.dev) · [GitHub](https://github.com/october-dev)

</div>
