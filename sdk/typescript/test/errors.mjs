import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { once } from 'node:events'
import {
  BusError,
  OctoberBusAgentSession,
  OctoberBusClient,
  OctoberBusScopeClient,
  newIdempotencyKey,
  pollInbox,
  requiredEnvironmentValue,
  withClaimedTask
} from '../dist/index.js'

async function expectInternal(work) {
  try {
    await work()
    assert.fail('request unexpectedly succeeded')
  } catch (error) {
    assert(error instanceof BusError)
    assert.equal(error.code, 'INTERNAL')
  }
}

await expectInternal(() => new OctoberBusClient('not a url', 'token').listPeers())

assert.equal(requiredEnvironmentValue({ OCTOBER_BUS_TOKEN: 'token' }, 'OCTOBER_BUS_TOKEN'), 'token')
assert.throws(
  () => requiredEnvironmentValue({}, 'OCTOBER_BUS_TOKEN'),
  /OCTOBER_BUS_TOKEN is required/
)
assert.match(newIdempotencyKey(), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)

const abortController = new AbortController()
abortController.abort(new Error('stopped'))
await assert.rejects(
  () =>
    OctoberBusAgentSession.start({
      address: 'not a url',
      scopeToken: 'token',
      registration: { id: 'aborted', displayName: 'Aborted' },
      signal: abortController.signal
    }),
  /stopped/
)
await assert.rejects(
  () =>
    OctoberBusAgentSession.start({
      address: 'not a url',
      scopeToken: 'token',
      registration: { id: 'offline-ready', displayName: 'Offline Ready' },
      initialLifecycle: 'offline',
      initialReady: true
    }),
  /offline agents cannot be ready/
)

let releasedTask
const failingTaskClient = {
  async claimTask(taskId) {
    return { id: taskId, status: 'claimed' }
  },
  async releaseTask(taskId) {
    releasedTask = taskId
  }
}
await assert.rejects(
  () =>
    withClaimedTask(failingTaskClient, 'task_failure', async () => {
      throw new Error('work failed')
    }),
  /work failed/
)
assert.equal(releasedTask, 'task_failure')

let inboxPolls = 0
const pollingClient = {
  async pullInbox(limit, options) {
    assert.equal(limit, 50)
    assert.equal(options.waitMs, 25_000)
    inboxPolls += 1
    return inboxPolls === 1 ? [] : [{ id: 'message_1' }]
  }
}
const inbox = pollInbox(pollingClient)
const polled = await inbox.next()
assert.equal(polled.done, false)
assert.equal(polled.value[0].id, 'message_1')
await inbox.return()
await assert.rejects(() => pollInbox(pollingClient, { waitMs: 0 }).next(), /waitMs must be an integer between 1 and 25000/)
await assert.rejects(
  () => new OctoberBusScopeClient('not a url', 'token').watchEvents({ waitMs: 0 }).next(),
  /waitMs must be an integer between 1 and 25000/
)

// A controllable inbox client whose pullInbox blocks until its signal aborts or
// a queued batch is available. Mirrors the server-side bounded wait.
function makeInboxClient(batches) {
  const state = {
    polls: 0,
    wakeAborts: 0,
    queue: batches
  }
  const client = {
    async pullInbox(limit, options) {
      state.polls += 1
      const signal = options.signal
      if (signal?.aborted) state.wakeAborts += 1
      return new Promise((resolve) => {
        const tryResolve = () => {
          if (state.queue.length > 0) {
            resolve(state.queue.shift())
            return true
          }
          return false
        }
        if (tryResolve()) return
        const onAbort = () => {
          signal?.removeEventListener('abort', onAbort)
          if (signal?.aborted) state.wakeAborts += 1
          resolve([])
        }
        signal?.addEventListener('abort', onAbort, { once: true })
      })
    }
  }
  return { client, state }
}

// Ready-edge wake regression: a wake signal interrupts the current inbox wait and
// re-polls immediately; it does NOT terminate the loop. Only the termination
// signal ends polling. The host owns every returned batch (none are discarded).
{
  const { client, state } = makeInboxClient([])
  const wake = new AbortController()
  const stop = new AbortController()
  const inbox = pollInbox(client, { waitMs: 25_000, signal: stop.signal, wake: wake.signal })
  const first = inbox.next()
  await new Promise((resolve) => setTimeout(resolve, 30))
  assert.equal(state.wakeAborts, 0, 'no wake before the ready edge')
  // A delivery queues while the host is blocked in waitMs, then the ready edge
  // (false->true) fires. The wake interrupts the wait so the queued delivery is
  // picked up immediately, not after the full waitMs.
  state.queue.push([{ id: 'queued_before_ready' }])
  wake.abort()
  const result = await first
  assert.equal(result.done, false)
  assert.equal(result.value[0].id, 'queued_before_ready')
  assert.equal(state.wakeAborts >= 1, true, 'wake interrupted the inbox wait')
  // The loop continues after a wake (it did not terminate).
  assert.equal(state.polls >= 2, true, 'wake caused an immediate re-poll')
  stop.abort()
  await inbox.return()
}

// Concurrent polling: two independent pollInbox loops, each with its own wake
// signal, re-poll independently without cross-talk or termination.
{
  const { client: clientA, state: stateA } = makeInboxClient([])
  const { client: clientB, state: stateB } = makeInboxClient([])
  const wakeA = new AbortController()
  const wakeB = new AbortController()
  const stopA = new AbortController()
  const stopB = new AbortController()
  const loopA = pollInbox(clientA, { waitMs: 25_000, signal: stopA.signal, wake: wakeA.signal })
  const loopB = pollInbox(clientB, { waitMs: 25_000, signal: stopB.signal, wake: wakeB.signal })
  const nextA = loopA.next()
  const nextB = loopB.next()
  await new Promise((resolve) => setTimeout(resolve, 30))
  assert.equal(stateA.wakeAborts, 0)
  assert.equal(stateB.wakeAborts, 0)
  stateA.queue.push([{ id: 'a_1' }])
  wakeA.abort()
  assert.deepEqual((await nextA).value, [{ id: 'a_1' }])
  assert.equal(stateA.wakeAborts >= 1, true)
  assert.equal(stateB.wakeAborts, 0, 'loop B was not woken by loop A')
  stateB.queue.push([{ id: 'b_1' }])
  wakeB.abort()
  assert.deepEqual((await nextB).value, [{ id: 'b_1' }])
  assert.equal(stateB.wakeAborts >= 1, true)
  stopA.abort()
  stopB.abort()
  await loopA.return()
  await loopB.return()
}

const server = createServer((_request, response) => {
  response.writeHead(502, { 'content-type': 'text/plain' })
  response.end('upstream unavailable')
})
server.listen(0, '127.0.0.1')
await once(server, 'listening')

try {
  const address = server.address()
  assert(address && typeof address === 'object')
  await expectInternal(() =>
    new OctoberBusClient(`http://127.0.0.1:${address.port}`, 'token').listPeers()
  )
} finally {
  server.close()
  await once(server, 'close')
}

const hangingServer = createServer(() => {})
hangingServer.listen(0, '127.0.0.1')
await once(hangingServer, 'listening')

try {
  const address = hangingServer.address()
  assert(address && typeof address === 'object')
  const client = new OctoberBusClient(`http://127.0.0.1:${address.port}`, 'token')
  await assert.rejects(() => client.listPeers({ timeoutMs: 10 }), /timed out after 10ms/)
  const cancelled = new AbortController()
  cancelled.abort(new Error('cancelled by caller'))
  await assert.rejects(() => client.listPeers({ signal: cancelled.signal }), /cancelled by caller/)
  await assert.rejects(() => client.listPeers({ timeoutMs: 0 }), (error) => {
    assert(error instanceof BusError)
    assert.equal(error.code, 'INVALID_ARGUMENT')
    return true
  })
} finally {
  hangingServer.closeAllConnections()
  hangingServer.close()
  await once(hangingServer, 'close')
}
