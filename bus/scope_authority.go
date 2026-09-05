package bus

import (
	"context"
	"database/sql"
	"errors"
)

type scopeCredentialKey struct{}
type executionKey struct{}

func withExecution(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, executionKey{}, p)
}

func withScopeCredential(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, scopeCredentialKey{}, tokenDigest(token))
}

func (s *Store) beginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if digest, ok := ctx.Value(scopeCredentialKey{}).(string); ok {
		var found int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM scopes WHERE token_hash=?", digest).Scan(&found)
		if err != nil {
			tx.Rollback()
			if errors.Is(err, sql.ErrNoRows) {
				return nil, Errorf(CodeUnauthenticated, "Scope credential is no longer current")
			}
			return nil, err
		}
	}
	if p, ok := ctx.Value(executionKey{}).(Principal); ok {
		if err := requireCurrentExecution(ctx, tx, p, nowMillis()); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	return tx, nil
}
