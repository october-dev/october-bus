import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  OctoberBusAdminClient,
  OctoberBusAgentSession,
  OctoberBusClient,
  OctoberBusScopeClient,
  newIdempotencyKey,
  pollInbox
} from '../dist/index.js'

const binary = process.env.OCTOBER_BUS_BINARY
if (!binary) throw new Error('OCTOBER_BUS_BINARY is required')

const root = await mkdtemp(join(tmpdir(), 'october-bus-ready-edge-'))
const dataDir = join(root, 'data')
const runtimeDir = join(root, 'run')
const runFile = join(runtimeDir, 'bus.json')
const child = spawn(binary, ['start'], {
  env: {
    ...process.env,
    OCTOBER_BUS_DATA_DIR: dataDir,
    OCTOBER_BUS_RUNTIME_DIR: runtimeDir
  },
  stdio: ['ignore', 'pipe', 'pipe']
})

let stderr = ''
child.stderr.setEncoding('utf8')
child.stderr.on('data', (value) => {
  stderr += value
})

async function readRunFile() {
  const deadline = Date.now() + 10_000
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`October Bus exited early: ${stderr}`)
    try {
      return JSON.parse(await readFile(runFile, 'utf8'))
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
  }
  throw new Error(`October Bus did not start: ${stderr}`)
}

let session

try {
  const run = await readRunFile()
  const admin = new OctoberBusAdminClient(run.address, run.adminToken)
  const scope = await admin.createScope({ id: 'ready-edge' })
  const owner = new OctoberBusScopeClient(run.address, scope.scopeToken)

  // A delivery is queued for the host BEFORE it reports ready. It must become
  // visible only once the host resumes its own inbox consumer on the ready edge.
  session = await OctoberBusAgentSession.start({
    address: run.address,
    scopeToken: scope.scopeToken,
    registration: { id: 'host', displayName: 'Host', leaseMs: 30_000 },
    heartbeatIntervalMs: 100,
    initialReady: false
  })

  const peer = await owner.registerAgent({ id: 'peer', displayName: 'Peer', connectTo: ['host'] })
  const peerClient = new OctoberBusClient(run.address, peer.agentToken)
  await peerClient.heartbeat('ready', true, 30_000)

  // Queue a delivery while the host is not ready. It stays in the inbox until
  // the host's own reservation returns it.
  const receipt = await peerClient.sendMessage({
    to: 'host',
    body: 'queued before ready',
    idempotencyKey: newIdempotencyKey()
  })

  // The host runs its own consumer loop, wired to the session wake signal. The
  // wake fires on the false->true ready edge, interrupting the blocked wait so
  // the queued delivery is picked up promptly (not after the full waitMs).
  const inbox = pollInbox(session.client, { waitMs: 25_000, wake: () => session.wake })
  const first = inbox.next()
  // Give the loop time to block inside its first waitMs reserve.
  await new Promise((resolve) => setTimeout(resolve, 200))
  const startedAt = Date.now()
  await session.setState('ready', true)
  const result = await first
  assert.equal(result.done, false)
  assert.equal(result.value.length, 1)
  assert.equal(result.value[0].id, receipt.messageId)
  assert.equal(Date.now() - startedAt < 3_000, true, `ready-edge wake should resume promptly, took ${Date.now() - startedAt}ms`)
  assert.equal(await session.client.acknowledgeMessages([receipt.messageId]), 1)
  await inbox.return()

  // Heartbeat failure ordering: setState must NOT commit ready or fire the wake
  // signal when the heartbeat fails. It must reject. (The session's background
  // heartbeat timer may then close the session, which is fine after this point.)
  child.kill('SIGTERM')
  await Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    new Promise((resolve) => setTimeout(resolve, 5_000))
  ])
  if (child.exitCode === null) child.kill('SIGKILL')
  await assert.rejects(
    () => session.setState('ready', true),
    (error) => {
      // Any network/transport failure is acceptable; the key is that a failed
      // heartbeat rejects rather than silently committing ready.
      assert(error instanceof Error)
      return true
    }
  )
} finally {
  await session?.close().catch(() => {})
  if (child.exitCode === null) child.kill('SIGKILL')
  await rm(root, { recursive: true, force: true })
}
