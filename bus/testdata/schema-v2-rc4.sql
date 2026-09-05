-- Exact database DDL from v0.1.0-rc.4 bus/store.go; do not regenerate from current schema.
BEGIN IMMEDIATE;
CREATE TABLE scopes (
  scope_id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);
CREATE TABLE agents (
  scope_id TEXT NOT NULL REFERENCES scopes(scope_id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  display_name TEXT NOT NULL,
  capabilities_json TEXT NOT NULL,
  execution_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  lifecycle TEXT NOT NULL CHECK(lifecycle IN ('starting','ready','working','idle','needs_input','offline')),
  ready INTEGER NOT NULL CHECK(ready IN (0,1)),
  lease_expires_at INTEGER NOT NULL,
  registered_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(scope_id, agent_id)
);
CREATE INDEX agents_scope_updated ON agents(scope_id, updated_at DESC);
CREATE TABLE peer_links (
  scope_id TEXT NOT NULL REFERENCES scopes(scope_id) ON DELETE CASCADE,
  left_agent TEXT NOT NULL,
  right_agent TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(scope_id, left_agent, right_agent),
  CHECK(left_agent < right_agent),
  FOREIGN KEY(scope_id, left_agent) REFERENCES agents(scope_id, agent_id) ON DELETE CASCADE,
  FOREIGN KEY(scope_id, right_agent) REFERENCES agents(scope_id, agent_id) ON DELETE CASCADE
);
CREATE TABLE messages (
  message_id TEXT PRIMARY KEY,
  scope_id TEXT NOT NULL REFERENCES scopes(scope_id) ON DELETE CASCADE,
  from_agent TEXT NOT NULL,
  to_agent TEXT NOT NULL,
  mode TEXT NOT NULL CHECK(mode IN ('notify','request','response')),
  body TEXT NOT NULL,
  context_json TEXT NOT NULL,
  response_to TEXT,
  idempotency_key TEXT,
  request_hash TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('queued','reserved','delivered','acknowledged','expired')),
  reservation_id TEXT,
  created_at INTEGER NOT NULL,
  expires_at INTEGER,
  delivered_at INTEGER,
  acknowledged_at INTEGER,
  replied_at INTEGER,
  response_message_id TEXT,
  FOREIGN KEY(scope_id, from_agent) REFERENCES agents(scope_id, agent_id),
  FOREIGN KEY(scope_id, to_agent) REFERENCES agents(scope_id, agent_id),
  FOREIGN KEY(response_to) REFERENCES messages(message_id)
);
CREATE INDEX messages_inbox ON messages(scope_id, to_agent, state, created_at);
CREATE INDEX messages_sender ON messages(scope_id, from_agent, created_at DESC);
CREATE UNIQUE INDEX messages_idempotency ON messages(scope_id, from_agent, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TABLE reservations (
  reservation_id TEXT PRIMARY KEY,
  scope_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  FOREIGN KEY(scope_id, agent_id) REFERENCES agents(scope_id, agent_id) ON DELETE CASCADE
);
CREATE TABLE tasks (
  task_id TEXT PRIMARY KEY,
  scope_id TEXT NOT NULL REFERENCES scopes(scope_id) ON DELETE CASCADE,
  description TEXT NOT NULL,
  created_by TEXT NOT NULL,
  claimed_by TEXT,
  claimed_execution_id TEXT,
  status TEXT NOT NULL CHECK(status IN ('open','claimed','done')),
  dependencies_json TEXT NOT NULL,
  note TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
	CHECK((status='open' AND claimed_by IS NULL AND claimed_execution_id IS NULL) OR (status!='open' AND claimed_by IS NOT NULL AND claimed_execution_id IS NOT NULL)),
  FOREIGN KEY(scope_id, created_by) REFERENCES agents(scope_id, agent_id),
  FOREIGN KEY(scope_id, claimed_by) REFERENCES agents(scope_id, agent_id)
);
CREATE INDEX tasks_scope_status ON tasks(scope_id, status, created_at);
CREATE TABLE escalations (
  escalation_id TEXT PRIMARY KEY,
  scope_id TEXT NOT NULL REFERENCES scopes(scope_id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  question TEXT NOT NULL,
  options_json TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','resolved','cancelled')),
  answer TEXT,
  created_at INTEGER NOT NULL,
  resolved_at INTEGER,
  FOREIGN KEY(scope_id, agent_id) REFERENCES agents(scope_id, agent_id)
);
CREATE INDEX escalations_scope_status ON escalations(scope_id, status, created_at);
PRAGMA user_version=2;
COMMIT;
