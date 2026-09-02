import { BusError } from './errors.js'
import type { BusErrorCode } from './errors.js'
import type {
  Agent,
  AgentLifecycle,
  AskHumanInput,
  BusHealth,
  BusMessage,
  BusTask,
  CreateScopeInput,
  CreateScopeResult,
  DeliveryReceipt,
  HumanEscalation,
  InboxReservation,
  RegisterAgentInput,
  RegisterAgentResult,
  SendMessageInput
} from './protocol.js'

interface Success<T> {
  ok: true
  result: T
}

interface Failure {
  ok: false
  error: { code: BusErrorCode; message: string; details?: Record<string, unknown> }
}

export interface OperationOptions {
  signal?: AbortSignal
  timeoutMs?: number
}

export interface InboxReservationOptions extends OperationOptions {
  waitMs?: number
}

async function request<T>(
  address: string,
  token: string | undefined,
  method: string,
  path: string,
  value?: unknown,
  options: OperationOptions = {}
): Promise<T> {
  const timeoutMs = options.timeoutMs ?? 30_000
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new BusError('INVALID_ARGUMENT', 'timeoutMs must be a positive finite number')
  }
  const controller = new AbortController()
  const onAbort = () => controller.abort(options.signal?.reason ?? new Error('Operation aborted'))
  if (options.signal?.aborted) onAbort()
  else options.signal?.addEventListener('abort', onAbort, { once: true })
  const timeout = setTimeout(
    () => controller.abort(new Error(`October Bus request timed out after ${timeoutMs}ms`)),
    timeoutMs
  )
  let response: Response
  let text: string
  try {
    response = await fetch(`${address}${path}`, {
      method,
      headers: {
        accept: 'application/json',
        ...(value === undefined ? {} : { 'content-type': 'application/json' }),
        ...(token === undefined ? {} : { authorization: `Bearer ${token}` })
      },
      ...(value === undefined ? {} : { body: JSON.stringify(value) }),
      signal: controller.signal
    })
    text = await response.text()
  } catch (error) {
    const cause = controller.signal.aborted ? controller.signal.reason : error
    const message = cause instanceof Error ? cause.message : 'Network request failed'
    throw new BusError('INTERNAL', `October Bus request failed: ${message}`)
  } finally {
    clearTimeout(timeout)
    options.signal?.removeEventListener('abort', onAbort)
  }
  let payload: Success<T> | Failure | T
  try {
    payload = JSON.parse(text) as Success<T> | Failure | T
  } catch {
    throw new BusError('INTERNAL', `October Bus returned a non-JSON response with HTTP ${response.status}`)
  }
  if (response.ok) {
    if (payload && typeof payload === 'object' && 'ok' in payload && payload.ok === true)
      return (payload as Success<T>).result
    return payload as T
  }
  if (
    payload &&
    typeof payload === 'object' &&
    'ok' in payload &&
    payload.ok === false &&
    'error' in payload &&
    payload.error &&
    typeof payload.error === 'object' &&
    'code' in payload.error &&
    'message' in payload.error
  ) {
    const failure = payload as Failure
    throw new BusError(failure.error.code, failure.error.message, failure.error.details)
  }
  throw new BusError('INTERNAL', `October Bus request failed with HTTP ${response.status}`)
}

export class OctoberBusAdminClient {
  constructor(
    readonly address: string,
    private readonly adminToken: string
  ) {}

  health(options?: OperationOptions): Promise<BusHealth> {
    return request(this.address, undefined, 'GET', '/health', undefined, options)
  }

  createScope(input: CreateScopeInput = {}, options?: OperationOptions): Promise<CreateScopeResult> {
    return request(this.address, this.adminToken, 'POST', '/v1/scopes', input, options)
  }

  async shutdown(options?: OperationOptions): Promise<void> {
    await request(this.address, this.adminToken, 'POST', '/v1/admin/shutdown', {}, options)
  }
}

export class OctoberBusScopeClient {
  constructor(
    readonly address: string,
    readonly scopeToken: string
  ) {}

  registerAgent(input: RegisterAgentInput, options?: OperationOptions): Promise<RegisterAgentResult> {
    return request(this.address, this.scopeToken, 'POST', '/v1/agents', input, options)
  }

  listAgents(options?: OperationOptions): Promise<Agent[]> {
    return request(this.address, this.scopeToken, 'GET', '/v1/agents', undefined, options)
  }

  async linkAgents(left: string, right: string, options?: OperationOptions): Promise<void> {
    await request(this.address, this.scopeToken, 'POST', '/v1/links', { left, right }, options)
  }

  listEscalations(options?: OperationOptions): Promise<HumanEscalation[]> {
    return request(this.address, this.scopeToken, 'GET', '/v1/scope/escalations', undefined, options)
  }

  resolveEscalation(id: string, answer: string, options?: OperationOptions): Promise<HumanEscalation> {
    return request(this.address, this.scopeToken, 'POST', `/v1/scope/escalations/${encodeURIComponent(id)}/resolve`, {
      answer
    }, options)
  }
}

export class OctoberBusClient {
  constructor(
    readonly address: string,
    readonly agentToken: string
  ) {}

  heartbeat(lifecycle: AgentLifecycle, ready = true, leaseMs?: number, options?: OperationOptions): Promise<Agent> {
    return request(this.address, this.agentToken, 'PATCH', '/v1/me/heartbeat', {
      lifecycle,
      ready,
      ...(leaseMs === undefined ? {} : { leaseMs })
    }, options)
  }

  listPeers(options?: OperationOptions): Promise<Agent[]> {
    return request(this.address, this.agentToken, 'GET', '/v1/peers', undefined, options)
  }

  sendMessage(input: SendMessageInput, options?: OperationOptions): Promise<DeliveryReceipt> {
    return request(this.address, this.agentToken, 'POST', '/v1/messages', input, options)
  }

  receipt(messageId: string, options?: OperationOptions): Promise<DeliveryReceipt> {
    return request(this.address, this.agentToken, 'GET', `/v1/messages/${encodeURIComponent(messageId)}`, undefined, options)
  }

  reserveInbox(limit = 50, options: InboxReservationOptions = {}): Promise<InboxReservation | null> {
    const { waitMs, ...operationOptions } = options
    return request(
      this.address,
      this.agentToken,
      'POST',
      '/v1/inbox/reserve',
      { limit, ...(waitMs === undefined ? {} : { waitMs }) },
      operationOptions
    )
  }

  commitInbox(reservationId: string, options?: OperationOptions): Promise<BusMessage[]> {
    return request(
      this.address,
      this.agentToken,
      'POST',
      `/v1/inbox/${encodeURIComponent(reservationId)}/commit`,
      {},
      options
    )
  }

  async releaseInbox(reservationId: string, options?: OperationOptions): Promise<void> {
    await request(
      this.address,
      this.agentToken,
      'POST',
      `/v1/inbox/${encodeURIComponent(reservationId)}/release`,
      {},
      options
    )
  }

  async pullInbox(limit = 50, options?: InboxReservationOptions): Promise<BusMessage[]> {
    const reservation = await this.reserveInbox(limit, options)
    return reservation ? this.commitInbox(reservation.id, options) : []
  }

  async acknowledgeMessages(messageIds: string[], options?: OperationOptions): Promise<number> {
    const result = await request<{ acknowledged: number }>(
      this.address,
      this.agentToken,
      'POST',
      '/v1/messages/ack',
      { messageIds },
      options
    )
    return result.acknowledged
  }

  addTask(description: string, dependencies: string[] = [], options?: OperationOptions): Promise<BusTask> {
    return request(this.address, this.agentToken, 'POST', '/v1/tasks', { description, dependencies }, options)
  }

  listTasks(options?: OperationOptions): Promise<BusTask[]> {
    return request(this.address, this.agentToken, 'GET', '/v1/tasks', undefined, options)
  }

  claimTask(taskId: string, options?: OperationOptions): Promise<BusTask> {
    return request(this.address, this.agentToken, 'POST', `/v1/tasks/${encodeURIComponent(taskId)}/claim`, {}, options)
  }

  releaseTask(taskId: string, options?: OperationOptions): Promise<BusTask> {
    return request(this.address, this.agentToken, 'POST', `/v1/tasks/${encodeURIComponent(taskId)}/release`, {}, options)
  }

  completeTask(taskId: string, note?: string, options?: OperationOptions): Promise<BusTask> {
    return request(this.address, this.agentToken, 'POST', `/v1/tasks/${encodeURIComponent(taskId)}/complete`, {
      ...(note === undefined ? {} : { note })
    }, options)
  }

  askHuman(input: AskHumanInput, options?: OperationOptions): Promise<HumanEscalation> {
    return request(this.address, this.agentToken, 'POST', '/v1/escalations', input, options)
  }

  escalation(id: string, options?: OperationOptions): Promise<HumanEscalation> {
    return request(this.address, this.agentToken, 'GET', `/v1/escalations/${encodeURIComponent(id)}`, undefined, options)
  }

  mcpEndpoint(): { url: string; headers: Record<string, string> } {
    return { url: `${this.address}/mcp`, headers: { Authorization: `Bearer ${this.agentToken}` } }
  }
}
