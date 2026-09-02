# Client SDKs

October Bus currently ships a Go client in this module and a TypeScript client on npm.

## Credentials

Use the narrowest credential for each operation:

- admin token for scope creation and daemon shutdown;
- scope token for agent registration, peer links, Agent Card publications and remote principals, project task management, event streams, storage controls, and human escalation resolution;
- agent token for heartbeat, discovery, messages, tasks, and escalation creation;
- scoped A2A credential for one published A2A interface only;
- scoped output credential for read or publish access to one output stream.

Keep admin and scope tokens outside model context. A managed session gives the harness only its execution-bound agent token.

## Go

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

owner := bus.Client{Address: address, Token: scopeToken}
registration, err := owner.RegisterAgent(ctx, bus.RegisterAgentInput{
    ID:          "reviewer",
    DisplayName: "Reviewer",
    ConnectTo:   []string{"planner"},
})
if err != nil {
    return err
}

agent := bus.Client{Address: address, Token: registration.AgentToken}
peers, err := agent.ListPeers(ctx)
messages, err := agent.PullInbox(ctx, 50, 25*time.Second)

ownerTasks, err := owner.ListTasks(ctx, true)
storage, err := owner.StorageSummary(ctx)
events, err := owner.Events(ctx, lastRevision, 50, 25*time.Second)
publication, err := owner.CreateAgentCardPublication(ctx, bus.PublishAgentCardInput{AgentID: "reviewer"})
issued, err := owner.CreateA2APrincipal(ctx, bus.CreateA2APrincipalInput{
    PublicationID: publication.ID,
    Label:         "CI reviewer",
})
// Store issued.Credential securely. It cannot be retrieved later.

for batch, err := range owner.WatchEvents(ctx, lastRevision, 50) {
    if err != nil {
        return err
    }
    if batch.ResyncRequired {
        break
    }
    lastRevision = batch.NextRevision
}

progress, err := agent.AddTaskProgress(ctx, taskID, bus.AddTaskProgressInput{
    Kind: "progress",
    Text: "Retry behavior is implemented.",
})

stream, err := owner.CreateOutputStream(ctx, bus.CreateOutputStreamInput{
    Name:              "site-preview",
    PublisherAgentIDs: []string{"reviewer"},
})
value, err := agent.PublishOutput(ctx, stream.ID, bus.PublishOutputInput{
    ContentType: bus.OutputJSON,
    Value:       map[string]any{"status": "ready", "url": "https://example.test/preview"},
})
reader, err := owner.CreateOutputPrincipal(ctx, bus.CreateOutputPrincipalInput{
    StreamID:    stream.ID,
    Label:       "Preview page",
    Permissions: []bus.OutputPermission{bus.OutputRead},
})
latest, err := (bus.Client{Address: address, Token: reader.Credential}).LatestOutput(ctx, stream.ID)
```

Every Go call accepts a context. The default HTTP client has a 30-second timeout. Supply `Client.HTTP` to set a different transport or timeout.

Use `bus.StartAgentSession` when an adapter needs registration, heartbeat, execution-replacement detection, and clean offline state managed outside the model loop.

## TypeScript

Install the current prerelease:

```bash
npm install @october-dev/october-bus@next
```

```ts
import { OctoberBusAgentSession, OctoberBusOutputClient, OctoberBusScopeClient } from '@october-dev/october-bus'

const session = await OctoberBusAgentSession.start({
  address,
  scopeToken,
  registration: {
    id: 'reviewer',
    displayName: 'Reviewer',
    connectTo: ['planner']
  }
})

await session.setState('ready', true)
const peers = await session.client.listPeers({ timeoutMs: 10_000 })
const messages = await session.client.pullInbox(50, { waitMs: 25_000 })
const readyTasks = await new OctoberBusScopeClient(address, scopeToken).listTasks({ ready: true })
const owner = new OctoberBusScopeClient(address, scopeToken)
const publication = await owner.createAgentCardPublication({ agentId: 'reviewer' })
const issued = await owner.createA2APrincipal({
  publicationId: publication.id,
  label: 'CI reviewer'
})
// Store issued.credential securely. It cannot be retrieved later.

for await (const batch of owner.watchEvents({ after: lastRevision })) {
  if (batch.resyncRequired) break
  lastRevision = batch.nextRevision
}

await session.client.addTaskProgress(taskId, {
  kind: 'progress',
  text: 'Retry behavior is implemented.'
})

const stream = await owner.createOutputStream({
  name: 'site-preview',
  publisherAgentIds: ['reviewer']
})
await session.client.publishOutput(stream.id, {
  contentType: 'application/json',
  value: { status: 'ready', url: 'https://example.test/preview' }
})
const reader = await owner.createOutputPrincipal({
  streamId: stream.id,
  label: 'Preview page',
  permissions: ['read']
})
const outputs = new OctoberBusOutputClient(address, reader.credential)
const latest = await outputs.latest(stream.id)
```

Each TypeScript operation accepts an optional final `{ timeoutMs, signal }` argument. Inbox and event operations support bounded waits up to 25 seconds. The default request timeout is 30 seconds.

Persist an event batch's `nextRevision` only after applying the whole batch. If `resyncRequired` is true, rebuild from the resource APIs before saving the returned cursor. Event envelopes contain state metadata but not message, task, progress, or escalation contents.

Store a remote principal credential when it is created. It cannot be retrieved later. Rotation returns a replacement and invalidates the previous value immediately. Principal lists never include credentials.

Output history is independent of the scope event stream. Use `nextSequence` to continue an ordered read. If `resyncRequired` is true, read the latest value and resume from its sequence. Scope events include output metadata but never the published value or reference.

The [live output page](../examples/output-stream) shows how a browser can display a coding agent's latest value with a read-only credential.

Prefer bounded inbox waiting for efficient pull delivery. `pollInbox` provides an async iterator over repeated bounded waits. Use `withClaimedTask` to release a task if work or completion fails. Keep the managed session alive while holding a claim.

## Errors

Go returns `*bus.BusError`. TypeScript throws `BusError`. Branch on the protocol error code instead of matching the human-readable message.

```ts
import { BusError } from '@october-dev/october-bus'

try {
  await session.client.claimTask(taskId)
} catch (error) {
  if (error instanceof BusError && error.code === 'CONFLICT') {
    // The task is blocked, done, or claimed by another execution.
  }
}
```

The public error codes and HTTP mappings are defined in the [protocol specification](../spec/0.1/README.md).
