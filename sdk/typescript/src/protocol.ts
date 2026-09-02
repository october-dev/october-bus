export const OCTOBER_BUS_PROTOCOL_VERSION = '0.1' as const

export type ScopeId = string
export type AgentId = string
export type ExecutionId = string
export type MessageId = string
export type TaskId = string

export type AgentLifecycle = 'starting' | 'ready' | 'working' | 'idle' | 'needs_input' | 'offline'
export type MessageMode = 'notify' | 'request' | 'response'
export type DeliveryState = 'queued' | 'reserved' | 'delivered' | 'acknowledged' | 'expired'
export type TaskStatus = 'open' | 'claimed' | 'done'
export type EscalationStatus = 'pending' | 'resolved'

export interface AgentCapability {
  name: string
  description?: string
}

export interface AgentIdentity {
  scopeId: ScopeId
  agentId: AgentId
  executionId: ExecutionId
}

export interface Agent {
  id: AgentId
  displayName: string
  capabilities: AgentCapability[]
  lifecycle: AgentLifecycle
  ready: boolean
  reachable: boolean
  executionId: ExecutionId
  registeredAt: string
  updatedAt: string
}

export interface ContextItem {
  kind: 'text' | 'file' | 'url' | 'reference'
  title: string
  text?: string
  uri?: string
  mediaType?: string
}

export interface BusMessage {
  id: MessageId
  scopeId: ScopeId
  from: AgentId
  to: AgentId
  mode: MessageMode
  body: string
  context: ContextItem[]
  responseTo?: MessageId
  state: DeliveryState
  createdAt: string
  expiresAt?: string
  deliveredAt?: string
  acknowledgedAt?: string
  repliedAt?: string
  responseMessageId?: MessageId
}

export interface DeliveryReceipt {
  messageId: MessageId
  state: DeliveryState
  acceptedAt: string
  deliveredAt?: string
  acknowledgedAt?: string
  repliedAt?: string
  responseMessageId?: MessageId
}

export interface InboxReservation {
  id: string
  expiresAt: string
  messages: BusMessage[]
}

export interface BusTask {
  id: TaskId
  scopeId: ScopeId
  title: string
  description: string
  createdBy: AgentId | null
  claimedBy?: AgentId
  status: TaskStatus
  dependencies: TaskId[]
  ready: boolean
  note?: string
  createdAt: string
  updatedAt: string
}

export interface HumanEscalation {
  id: string
  scopeId: ScopeId
  agentId: AgentId
  question: string
  options: string[]
  status: EscalationStatus
  answer?: string
  createdAt: string
  resolvedAt?: string
}

export interface CreateScopeInput {
  id?: ScopeId
}

export interface CreateScopeResult {
  scopeId: ScopeId
  scopeToken: string
}

export interface RegisterAgentInput {
  id?: AgentId
  displayName: string
  capabilities?: AgentCapability[]
  connectTo?: AgentId[]
  leaseMs?: number
}

export interface RegisterAgentResult extends AgentIdentity {
  agentToken: string
  leaseExpiresAt: string
}

export interface SendMessageInput {
  to: AgentId
  body: string
  mode?: MessageMode
  responseTo?: MessageId
  idempotencyKey?: string
  expiresInMs?: number
  context?: ContextItem[]
}

export interface AddTaskInput {
  title: string
  description?: string
  dependencies?: TaskId[]
}

export interface StorageRecordSummary {
  recordType: 'message' | 'task' | 'escalation'
  state: string
  count: number
  estimatedBytes: number
  oldestAt?: string
}

export interface StorageSummary {
  scopeId: ScopeId
  generatedAt: string
  totalEstimatedBytes: number
  records: StorageRecordSummary[]
}

export interface PruneScopeInput {
  before: string
  execute?: boolean
}

export interface RetentionCounts {
  messages: number
  tasks: number
  escalations: number
}

export interface PruneScopeResult {
  scopeId: ScopeId
  before: string
  dryRun: boolean
  records: RetentionCounts
}

export interface AskHumanInput {
  question: string
  options?: string[]
}

export interface BusRunFile {
  protocolVersion: string
  address: string
  pid: number
  startedAt: string
  adminToken: string
}

export interface BusHealth {
  name: 'october-bus'
  protocolVersion: string
  runtimeVersion: string
  status: 'ready'
  startedAt: string
}
