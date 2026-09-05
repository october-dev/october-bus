import assert from 'node:assert/strict'
import test from 'node:test'
import { OctoberBusAgentSession } from '../dist/index.js'

const options = { address: 'http://session.invalid', scopeToken: 'synthetic', registration: { id: 'worker', displayName: 'Worker' } }
const ok = (result) => new Response(JSON.stringify({ ok: true, result }), { status: 200 })
const registration = { scopeId: 'scope', agentId: 'worker', executionId: 'exec_test', agentToken: 'synthetic', leaseExpiresAt: new Date(Date.now() + 300000).toISOString() }
const deferred = () => {
  let resolve
  const promise = new Promise((done) => { resolve = done })
  return { promise, resolve }
}

test('failed initial heartbeat retires registration without hiding the startup error', async (t) => {
  let retired = 0
  t.mock.method(globalThis, 'fetch', async (url) => {
    if (url.endsWith('/agents')) return ok(registration)
    if (url.endsWith('/retire')) { retired++; return ok({ retired: true }) }
    throw new Error('initial heartbeat failed')
  })
  await assert.rejects(OctoberBusAgentSession.start(options), /initial heartbeat failed/)
  assert.equal(retired, 1)
})

test('close drains setState, is shared, and rejects further operations', async (t) => {
  const workingStarted = deferred()
  const releaseWorking = deferred()
  const order = []
  t.mock.method(globalThis, 'fetch', async (url, request) => {
    if (url.endsWith('/agents')) return ok(registration)
    if (url.endsWith('/retire')) { order.push('retired'); return ok({ retired: true }) }
    const { lifecycle } = JSON.parse(request.body)
    if (lifecycle === 'working') { workingStarted.resolve(); await releaseWorking.promise }
    order.push(lifecycle)
    return ok({ lifecycle })
  })
  const session = await OctoberBusAgentSession.start(options)
  t.after(() => session.close())
  const updating = session.setState('working', true)
  await workingStarted.promise
  const closing = session.close()
  assert.equal(session.close(), closing)
  await assert.rejects(session.setState('ready', true), /closed/)
  releaseWorking.resolve()
  await Promise.all([updating, closing, session.done])
  assert.deepEqual(order, ['starting', 'working', 'retired'])
})

test('abort during registration retires once the committed result arrives', async (t) => {
  const controller = new AbortController()
  const registered = deferred()
  let retired = 0
  t.mock.method(globalThis, 'fetch', async (url) => {
    if (url.endsWith('/agents')) { await registered.promise; return ok(registration) }
    if (url.endsWith('/retire')) { retired++; return ok({ retired: true }) }
    assert.fail('canceled startup must not heartbeat')
  })
  const starting = OctoberBusAgentSession.start({ ...options, signal: controller.signal })
  controller.abort(new Error('canceled startup'))
  registered.resolve()
  await assert.rejects(starting, /canceled startup/)
  assert.equal(retired, 1)
})

test('heartbeat failure still attempts retirement and preserves the error', async (t) => {
  let beats = 0
  let retired = 0
  t.mock.method(globalThis, 'fetch', async (url) => {
    if (url.endsWith('/agents')) return ok(registration)
    if (url.endsWith('/retire')) { retired++; throw new Error('cleanup failed') }
    if (++beats > 1) throw new Error('authority lost')
    return ok({ lifecycle: 'starting' })
  })
  const session = await OctoberBusAgentSession.start({ ...options, heartbeatIntervalMs: 5 })
  t.after(() => session.close())
  await session.done
  assert.match(String(session.error), /authority lost/)
  assert.equal(retired, 1)
  await session.close()
  assert.equal(retired, 1)
})

test('close cancels an in-flight background heartbeat without reporting failure', async (t) => {
  const heartbeatStarted = deferred()
  let beats = 0
  let retired = 0
  t.mock.method(globalThis, 'fetch', async (url, request) => {
    if (url.endsWith('/agents')) return ok(registration)
    if (url.endsWith('/retire')) { retired++; return ok({ retired: true }) }
    if (++beats === 1) return ok({ lifecycle: 'starting' })
    heartbeatStarted.resolve()
    return new Promise((resolve, reject) => {
      request.signal.addEventListener('abort', () => reject(request.signal.reason), { once: true })
    })
  })
  const session = await OctoberBusAgentSession.start({ ...options, heartbeatIntervalMs: 5 })
  t.after(() => session.close())
  await heartbeatStarted.promise
  await session.close()
  assert.equal(session.error, undefined)
  assert.equal(retired, 1)
})
