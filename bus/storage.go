package bus

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *Store) StorageSummary(ctx context.Context, scopeID string) (StorageSummary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StorageSummary{}, err
	}
	defer tx.Rollback()
	now := nowMillis()
	if err := expireMessages(ctx, tx, scopeID, now); err != nil {
		return StorageSummary{}, err
	}
	if err := releaseStaleTaskClaims(ctx, tx, scopeID); err != nil {
		return StorageSummary{}, err
	}
	summary := StorageSummary{ScopeID: scopeID, GeneratedAt: instant(now), Records: []StorageRecordSummary{}}
	queries := []struct {
		recordType string
		statement  string
	}{
		{"message", `SELECT state,COUNT(*),COALESCE(SUM(length(CAST(body AS BLOB))+length(CAST(context_json AS BLOB))),0),
MIN(CASE state WHEN 'queued' THEN created_at WHEN 'reserved' THEN created_at WHEN 'delivered' THEN delivered_at WHEN 'acknowledged' THEN acknowledged_at WHEN 'expired' THEN expires_at END)
FROM messages WHERE scope_id=? GROUP BY state ORDER BY state`},
		{"task", `SELECT status,COUNT(*),COALESCE(SUM(length(CAST(title AS BLOB))+length(CAST(description AS BLOB))+length(CAST(dependencies_json AS BLOB))+COALESCE(length(CAST(note AS BLOB)),0)),0),
MIN(CASE status WHEN 'open' THEN created_at ELSE updated_at END)
FROM tasks WHERE scope_id=? GROUP BY status ORDER BY status`},
		{"escalation", `SELECT status,COUNT(*),COALESCE(SUM(length(CAST(question AS BLOB))+length(CAST(options_json AS BLOB))+COALESCE(length(CAST(answer AS BLOB)),0)),0),
MIN(CASE status WHEN 'pending' THEN created_at ELSE resolved_at END)
FROM escalations WHERE scope_id=? GROUP BY status ORDER BY status`},
	}
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query.statement, scopeID)
		if err != nil {
			return StorageSummary{}, err
		}
		for rows.Next() {
			var record StorageRecordSummary
			var oldest sql.NullInt64
			if err := rows.Scan(&record.State, &record.Count, &record.EstimatedBytes, &oldest); err != nil {
				rows.Close()
				return StorageSummary{}, err
			}
			record.RecordType = query.recordType
			record.OldestAt = instant(oldest.Int64)
			summary.TotalEstimatedBytes += record.EstimatedBytes
			summary.Records = append(summary.Records, record)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return StorageSummary{}, err
		}
		rows.Close()
	}
	if err := tx.Commit(); err != nil {
		return StorageSummary{}, err
	}
	return summary, nil
}

type retentionMessage struct {
	id                string
	mode              MessageMode
	state             DeliveryState
	responseTo        string
	responseMessageID string
	deliveredAt       sql.NullInt64
	expiresAt         sql.NullInt64
	acknowledgedAt    sql.NullInt64
}

func (message retentionMessage) terminalBefore(before int64) bool {
	switch message.state {
	case DeliveryAcknowledged:
		return message.acknowledgedAt.Valid && message.acknowledgedAt.Int64 < before
	case DeliveryExpired:
		return message.expiresAt.Valid && message.expiresAt.Int64 < before
	default:
		return false
	}
}

func retentionMessageCandidates(ctx context.Context, tx *sql.Tx, scopeID string, before int64) (map[string]retentionMessage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT message_id,mode,state,response_to,response_message_id,delivered_at,expires_at,acknowledged_at FROM messages WHERE scope_id=?`, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := map[string]retentionMessage{}
	for rows.Next() {
		var message retentionMessage
		var responseTo, responseMessageID sql.NullString
		if err := rows.Scan(&message.id, &message.mode, &message.state, &responseTo, &responseMessageID, &message.deliveredAt, &message.expiresAt, &message.acknowledgedAt); err != nil {
			return nil, err
		}
		message.responseTo = responseTo.String
		message.responseMessageID = responseMessageID.String
		all[message.id] = message
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	candidates := map[string]retentionMessage{}
	for _, message := range all {
		if !message.terminalBefore(before) || message.mode == MessageResponse {
			continue
		}
		switch message.mode {
		case MessageNotify:
			candidates[message.id] = message
		case MessageRequest:
			if message.responseMessageID == "" {
				if message.state == DeliveryExpired && !message.deliveredAt.Valid {
					candidates[message.id] = message
				}
				continue
			}
			response, ok := all[message.responseMessageID]
			if ok && response.mode == MessageResponse && response.responseTo == message.id && response.terminalBefore(before) {
				candidates[message.id] = message
				candidates[response.id] = response
			}
		}
	}
	return candidates, nil
}

func retentionTaskCandidates(ctx context.Context, tx *sql.Tx, scopeID string, before int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT task_id,status,dependencies_json,updated_at FROM tasks WHERE scope_id=?`, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type taskRecord struct {
		id           string
		status       string
		dependencies []string
		updatedAt    int64
	}
	tasks := []taskRecord{}
	activeDependencies := map[string]bool{}
	for rows.Next() {
		var task taskRecord
		var dependenciesJSON string
		if err := rows.Scan(&task.id, &task.status, &dependenciesJSON, &task.updatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(dependenciesJSON), &task.dependencies); err != nil {
			return nil, err
		}
		if task.status != "done" {
			for _, dependency := range task.dependencies {
				activeDependencies[dependency] = true
			}
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	candidates := []string{}
	for _, task := range tasks {
		if task.status == "done" && task.updatedAt < before && !activeDependencies[task.id] {
			candidates = append(candidates, task.id)
		}
	}
	return candidates, nil
}

func retentionEscalationCandidates(ctx context.Context, tx *sql.Tx, scopeID string, before int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT escalation_id FROM escalations WHERE scope_id=? AND status='resolved' AND resolved_at<?`, scopeID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) PruneScope(ctx context.Context, scopeID string, before int64, execute bool) (PruneScopeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneScopeResult{}, err
	}
	defer tx.Rollback()
	now := nowMillis()
	if err := expireMessages(ctx, tx, scopeID, now); err != nil {
		return PruneScopeResult{}, err
	}
	if err := releaseStaleTaskClaims(ctx, tx, scopeID); err != nil {
		return PruneScopeResult{}, err
	}
	messages, err := retentionMessageCandidates(ctx, tx, scopeID, before)
	if err != nil {
		return PruneScopeResult{}, err
	}
	tasks, err := retentionTaskCandidates(ctx, tx, scopeID, before)
	if err != nil {
		return PruneScopeResult{}, err
	}
	escalations, err := retentionEscalationCandidates(ctx, tx, scopeID, before)
	if err != nil {
		return PruneScopeResult{}, err
	}
	result := PruneScopeResult{
		ScopeID: scopeID, Before: time.UnixMilli(before).UTC().Format(time.RFC3339Nano), DryRun: !execute,
		Records: RetentionCounts{Messages: int64(len(messages)), Tasks: int64(len(tasks)), Escalations: int64(len(escalations))},
	}
	if execute {
		for _, message := range messages {
			if message.mode != MessageResponse {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE scope_id=? AND message_id=?`, scopeID, message.id); err != nil {
				return PruneScopeResult{}, err
			}
		}
		for _, message := range messages {
			if message.mode == MessageResponse {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE scope_id=? AND message_id=?`, scopeID, message.id); err != nil {
				return PruneScopeResult{}, err
			}
		}
		for _, taskID := range tasks {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE scope_id=? AND task_id=?`, scopeID, taskID); err != nil {
				return PruneScopeResult{}, err
			}
		}
		for _, escalationID := range escalations {
			if _, err := tx.ExecContext(ctx, `DELETE FROM escalations WHERE scope_id=? AND escalation_id=?`, scopeID, escalationID); err != nil {
				return PruneScopeResult{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return PruneScopeResult{}, err
	}
	return result, nil
}
