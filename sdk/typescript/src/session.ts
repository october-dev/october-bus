import { OctoberBusClient, OctoberBusScopeClient } from './client.js'
import type {
  Agent,
  AgentLifecycle,
  BusMessage,
  BusTask,
  RegisterAgentInput,
  RegisterAgentResult
} from './protocol.js'

export interface AgentSessionOptions {
  address: string
  scopeToken: string
  registration: RegisterAgentInput
  heartbeatIntervalMs?: number
  initialLifecycle?: AgentLifecycle
  initialReady?: boolean
  signal?: AbortSignal
}

export interface InboxPollingOptions {
  limit?: number
  waitMs?: number
  /** Aborting this signal terminates the polling loop entirely. */
  signal?: AbortSignal
  /**
   * Accepted for API compatibility. The server wakes the blocked reserve on a
   * ready edge (see {@link OctoberBusAgentSession}), so a ready host's own
   * consumer loop resumes promptly and picks up queued deliveries without the
   * client aborting the long-poll. The host owns every returned batch; nothing
   * is reserved-and-dropped here.
   */
  wake?: () => AbortSignal | undefined
}

export interface ClaimedTaskResult<T> {
  task: BusTask
  value: T
}

export function requiredEnvironmentValue(
  environment: Readonly<Record<string, string | undefined>>,
  name: string
): string {
  const value = environment[name]
  if (!value) throw new Error(`${name} is required`)
  return value
}

export function newIdempotencyKey(): string {
  return crypto.randomUUID()
}

export class OctoberBusAgentSession {
  readonly client: OctoberBusClient
  readonly registration: RegisterAgentResult
  readonly done: Promise<void>

  private readonly leaseMs: number
  private readonly heartbeatIntervalMs: number
  private lifecycle: AgentLifecycle
  private ready: boolean
  private timer: ReturnType<typeof setTimeout> | undefined
  private pendingHeartbeat: Promise<Agent> | undefined
  private resolveDone!: () => void
  private closed = false
  private sessionError: unknown
  private wakeController: AbortController = new AbortController()

  /**
   * A wake signal that fires on every false->true ready transition. Wire it to
   * {@link pollInbox} (`wake: session.wake`) so a host reporting ready=true
   * promptly resumes its own inbox loop and picks up queued deliveries. The
   * signal is replaced after each transition, so callers should read `wake`
   * per polling iteration (or pass it as a getter to `pollInbox`).
   */
  get wake(): AbortSignal {
    return this.wakeController.signal
  }

  private constructor(options: AgentSessionOptions, registration: RegisterAgentResult) {
    this.registration = registration
    this.client = new OctoberBusClient(options.address, registration.agentToken)
    this.leaseMs = options.registration.leaseMs || 300_000
    this.heartbeatIntervalMs = options.heartbeatIntervalMs ?? Math.floor(this.leaseMs / 3)
    this.lifecycle = options.initialLifecycle ?? 'starting'
    this.ready = options.initialReady ?? false
    this.done = new Promise((resolve) => {
      this.resolveDone = resolve
    })
    options.signal?.addEventListener('abort', () => void this.close(), { once: true })
  }

  static async start(options: AgentSessionOptions): Promise<OctoberBusAgentSession> {
    if (options.signal?.aborted) {
      throw options.signal.reason ?? new Error('Operation aborted')
    }
    const leaseMs = options.registration.leaseMs || 300_000
    const interval = options.heartbeatIntervalMs ?? Math.floor(leaseMs / 3)
    const lifecycle = options.initialLifecycle ?? 'starting'
    const ready = options.initialReady ?? false
    if (interval <= 0 || interval >= leaseMs) {
      throw new Error('heartbeatIntervalMs must be shorter than the execution lease')
    }
    if (lifecycle === 'offline' && ready) {
      throw new Error('offline agents cannot be ready')
    }
    const scope = new OctoberBusScopeClient(options.address, options.scopeToken)
    const registration = await scope.registerAgent({ ...options.registration, leaseMs })
    if (options.signal?.aborted) {
      const client = new OctoberBusClient(options.address, registration.agentToken)
      try {
        await client.heartbeat('offline', false, leaseMs)
      } catch {
        // The lease remains the cleanup fallback if the execution was replaced.
      }
      throw options.signal.reason ?? new Error('Operation aborted')
    }
    const session = new OctoberBusAgentSession(options, registration)
    await session.client.heartbeat(session.lifecycle, session.ready, session.leaseMs)
    session.scheduleHeartbeat()
    return session
  }

  get error(): unknown {
    return this.sessionError
  }

  async setState(lifecycle: AgentLifecycle, ready: boolean): Promise<Agent> {
    if (this.closed) throw new Error('agent session is closed')
    if (lifecycle === 'offline' && ready) throw new Error('offline agents cannot be ready')
    // The heartbeat runs first and internal state is only committed once it
    // succeeds. If it fails, `this.ready` still reflects the previous state,
    // so a later retry of the same transition still sees the false->true edge
    // and fires the wake signal again.
    const agent = await this.client.heartbeat(lifecycle, ready, this.leaseMs)
    const becameReady = ready && !this.ready
    this.lifecycle = lifecycle
    this.ready = ready
    if (becameReady) {
      // Signal the host's own consumer loop (via pollInbox's `wake`) so it
      // resumes immediately and owns the queued deliveries. Nothing is pulled
      // or discarded here: the host keeps every returned batch.
      this.wakeController.abort()
      this.wakeController = new AbortController()
    }
    return agent
  }

  async close(): Promise<void> {
    if (this.closed) return
    this.closed = true
    if (this.timer !== undefined) clearTimeout(this.timer)
    try {
      await this.pendingHeartbeat
      await this.client.heartbeat('offline', false, this.leaseMs)
    } catch (error) {
      if (this.sessionError === undefined) this.sessionError = error
    } finally {
      this.resolveDone()
    }
  }

  private scheduleHeartbeat(): void {
    if (this.closed) return
    this.timer = setTimeout(() => {
      this.pendingHeartbeat = this.client.heartbeat(this.lifecycle, this.ready, this.leaseMs)
      void this.pendingHeartbeat.then(
        () => {
          this.pendingHeartbeat = undefined
          this.scheduleHeartbeat()
        },
        (error: unknown) => {
          this.pendingHeartbeat = undefined
          this.sessionError = error
          this.closed = true
          this.resolveDone()
        }
      )
    }, this.heartbeatIntervalMs)
  }
}

export async function* pollInbox(
  client: OctoberBusClient,
  options: InboxPollingOptions = {}
): AsyncGenerator<BusMessage[]> {
  const limit = options.limit ?? 50
  const waitMs = options.waitMs ?? 25_000
  if (limit < 1 || limit > 100) throw new Error('limit must be between 1 and 100')
  if (!Number.isInteger(waitMs) || waitMs < 1 || waitMs > 25_000) {
    throw new Error('waitMs must be an integer between 1 and 25000')
  }
  const terminal = options.signal
  // The server wakes the blocked reserve on the ready edge (Runtime.Heartbeat
  // notifies the per-agent signal that ReserveInbox waits on), so a ready host
  // receives its queued deliveries promptly without the client aborting the
  // long-poll. Aborting the pull here would race the server's prompt response
  // and orphan an already-committed reservation, so the in-flight pull is NOT
  // cancelled on `wake` — the server delivers through it. `wake` is accepted
  // for API compatibility and is redundant with the server-side mechanism.
  while (!terminal?.aborted) {
    let messages: BusMessage[]
    try {
      messages = await client.pullInbox(limit, {
        waitMs,
        ...(terminal === undefined ? {} : { signal: terminal })
      })
    } catch (error) {
      if (terminal?.aborted) return // terminal abort: stop
      throw error
    }
    // pullInbox has already committed any returned batch. Deliver it before
    // honoring the terminal abort so an abort racing the response cannot drop
    // mail.
    if (messages.length > 0) yield messages
    if (terminal?.aborted) return
  }
}

export async function withClaimedTask<T>(
  client: OctoberBusClient,
  taskId: string,
  work: (task: BusTask) => Promise<T>,
  completionNote?: (value: T) => string | undefined
): Promise<ClaimedTaskResult<T>> {
  const claimed = await client.claimTask(taskId)
  try {
    const value = await work(claimed)
    const task = await client.completeTask(taskId, completionNote?.(value))
    return { task, value }
  } catch (error) {
    try {
      await client.releaseTask(taskId)
    } catch {
      // Preserve the work error. Lease recovery remains the final fallback.
    }
    throw error
  }
}
