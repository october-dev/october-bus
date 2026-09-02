# October Bus TypeScript Client

Typed clients and protocol definitions for connecting Node.js applications and harness adapters to October Bus.

The Bus daemon is distributed separately as a native executable.

The client is in active development and has not reached a stable release. Before 1.0, its API, schemas, and protocol behavior may change between releases.

`OctoberBusAdminClient` can export and import versioned portable scope archives. Archives preserve durable collaboration state but exclude credentials, leases, and active execution authority.

## Install

Install the current prerelease from the `next` tag:

```sh
npm install @october-dev/october-bus@next
```

## Example

Start the daemon, create a scope with the October Bus CLI, and set the returned token as `OCTOBER_BUS_SCOPE_TOKEN`.

```ts
import { OctoberBusClient, OctoberBusOutputClient, OctoberBusScopeClient } from '@october-dev/october-bus'

const address = 'http://127.0.0.1:4765'
const scope = new OctoberBusScopeClient(address, process.env.OCTOBER_BUS_SCOPE_TOKEN!)

const plannerRegistration = await scope.registerAgent({
  id: 'planner',
  displayName: 'Planner'
})
const reviewerRegistration = await scope.registerAgent({
  id: 'reviewer',
  displayName: 'Reviewer',
  connectTo: ['planner']
})

const planner = new OctoberBusClient(address, plannerRegistration.agentToken)
const reviewer = new OctoberBusClient(address, reviewerRegistration.agentToken)

const task = await scope.addTask({
  title: 'Review checkout retries',
  description: 'Check idempotency and error handling.'
})
const readyTasks = await scope.listTasks({ ready: true })
const storage = await scope.storageSummary()

const publication = await scope.createAgentCardPublication({ agentId: 'reviewer' })
const issued = await scope.createA2APrincipal({
  publicationId: publication.id,
  label: 'CI reviewer'
})
// Store issued.credential securely. It cannot be retrieved later.

const outputStream = await scope.createOutputStream({
  name: 'site-preview',
  publisherAgentIds: ['reviewer']
})
await reviewer.publishOutput(outputStream.id, {
  contentType: 'application/json',
  value: { status: 'ready', url: 'https://example.test/preview' }
})
const outputReader = await scope.createOutputPrincipal({
  streamId: outputStream.id,
  label: 'Preview page',
  permissions: ['read']
})
const outputs = new OctoberBusOutputClient(address, outputReader.credential)
const latestOutput = await outputs.latest(outputStream.id)

const dryRun = await scope.pruneScope({ before: '2026-08-01T00:00:00Z' })

const claimed = await reviewer.claimTask(task.id)
await reviewer.addTaskProgress(claimed.id, {
  kind: 'progress',
  text: 'Checked idempotency. Reviewing error handling now.'
})

const receipt = await planner.sendMessage({
  to: 'reviewer',
  mode: 'request',
  body: 'Review the retry path',
  idempotencyKey: crypto.randomUUID()
})

const messages = await reviewer.pullInbox()
await reviewer.acknowledgeMessages(messages.map((message) => message.id))
console.log(receipt.messageId, messages)
```

An idle agent can wait for new work without a polling loop:

```ts
const messages = await reviewer.pullInbox(50, { waitMs: 25_000 })
```

The server caps each wait at 25 seconds. Cancellation through `AbortSignal` does not reserve or lose a message.

Scope credentials create agents, manage the project task board, and handle human escalations. Agent credentials discover peers, exchange messages, coordinate tasks, and ask for human input. Claims and completion always require an execution-bound agent credential.

A remote principal credential is returned only when the principal is created or rotated. Store it securely. It is restricted to one published A2A interface and cannot access the Bus API or MCP endpoint.

Operations time out after 30 seconds by default. Pass `{ timeoutMs, signal }` as the final method argument to set a shorter deadline or cancel a request.

Generate a new idempotency key for each logical send. Keys remain bound to their original message. Keep heartbeats running while an execution holds a task claim, or the claim may be released for another agent.

`OctoberBusAgentSession` manages registration, conservative lifecycle state, heartbeat, execution replacement, and shutdown cleanup for adapters that use the TypeScript client.

`pollInbox` provides an abortable async iterator over repeated bounded inbox waits. `withClaimedTask` releases a claim when work or completion fails. Use both with a live agent session so the execution lease remains current while work is claimed.
