import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  OctoberBusAdminClient,
  OctoberBusAgentSession,
  OctoberBusScopeClient,
  newIdempotencyKey,
  withClaimedTask
} from '../dist/index.js'

const binary = process.env.OCTOBER_BUS_BINARY
if (!binary) throw new Error('OCTOBER_BUS_BINARY is required')

const root = await mkdtemp(join(tmpdir(), 'october-bus-sdk-'))
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
let plannerSession
let reviewerSession
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

try {
  const run = await readRunFile()
  const admin = new OctoberBusAdminClient(run.address, run.adminToken)
  const health = await admin.health()
  assert.equal(health.protocolVersion, '0.1')
  assert.equal(typeof health.runtimeVersion, 'string')
  const scope = await admin.createScope({ id: 'typescript-integration' })
  const owner = new OctoberBusScopeClient(run.address, scope.scopeToken)
  plannerSession = await OctoberBusAgentSession.start({
    address: run.address,
    scopeToken: scope.scopeToken,
    registration: { id: 'planner', displayName: 'Planner', leaseMs: 30_000 },
    heartbeatIntervalMs: 10
  })
  reviewerSession = await OctoberBusAgentSession.start({
    address: run.address,
    scopeToken: scope.scopeToken,
    registration: { id: 'reviewer', displayName: 'Reviewer', connectTo: ['planner'], leaseMs: 30_000 },
    heartbeatIntervalMs: 10
  })
  const agentsBeforeReady = await owner.listAgents()
  assert.equal(agentsBeforeReady.every((agent) => !agent.ready), true)
  await plannerSession.setState('ready', true)
  await reviewerSession.setState('ready', true)
  const planner = plannerSession.client
  const reviewer = reviewerSession.client
  const peers = await planner.listPeers()
  assert.equal(peers.length, 1)
  assert.equal(peers[0].id, 'reviewer')
  const message = { to: 'reviewer', mode: 'request', body: 'Review this', idempotencyKey: newIdempotencyKey() }
  const receipt = await planner.sendMessage(message)
  const retry = await planner.sendMessage(message)
  assert.equal(retry.messageId, receipt.messageId)
  const messages = await reviewer.pullInbox()
  assert.equal(messages.length, 1)
  assert.equal(messages[0].id, receipt.messageId)
  assert.equal(await reviewer.acknowledgeMessages([messages[0].id]), 1)
  const waitingInbox = reviewer.pullInbox(50, { waitMs: 2_000, timeoutMs: 3_000 })
  await new Promise((resolve) => setTimeout(resolve, 50))
  const waitingReceipt = await planner.sendMessage({
    to: 'reviewer',
    body: 'Wake the waiting reviewer',
    idempotencyKey: newIdempotencyKey()
  })
  const waitingMessages = await waitingInbox
  assert.equal(waitingMessages.length, 1)
  assert.equal(waitingMessages[0].id, waitingReceipt.messageId)
  assert.equal(await reviewer.acknowledgeMessages([waitingMessages[0].id]), 1)
  const task = await owner.addTask({ title: 'Review integration' })
  assert.equal(task.createdBy, null)
  assert.equal(task.ready, true)
  assert.deepEqual((await owner.listTasks({ ready: true })).map((value) => value.id), [task.id])
  const completed = await withClaimedTask(reviewer, task.id, async () => 'reviewed', (value) => value)
  assert.equal(completed.task.status, 'done')
  assert.equal(completed.value, 'reviewed')
  await reviewerSession.close()
  await plannerSession.close()
  const agentsAfterClose = await owner.listAgents()
  assert.equal(agentsAfterClose.every((agent) => !agent.reachable && agent.lifecycle === 'offline'), true)
} finally {
  await reviewerSession?.close()
  await plannerSession?.close()
  child.kill('SIGTERM')
  await Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    new Promise((resolve) => setTimeout(resolve, 5_000))
  ])
  if (child.exitCode === null) child.kill('SIGKILL')
  await rm(root, { recursive: true, force: true })
}
