package bus

import (
	"context"
	"errors"
	"testing"
)

func TestCredentialKindClassifiesOnlyCurrentNonScopeCredentials(t *testing.T) {
	agents := setupAgents(t, ":memory:")
	defer agents.runtime.Close()
	ctx := context.Background()
	store := sqliteStore(t, agents.runtime)

	publication, a2aPrincipal := setupA2APrincipal(t, agents)
	if _, err := agents.runtime.SetAgentCardPublicationEnabled(ctx, agents.scope.ScopeToken, publication.ID, false); err != nil {
		t.Fatal(err)
	}
	stream, err := agents.runtime.CreateOutputStream(ctx, agents.scope.ScopeToken, CreateOutputStreamInput{Name: "credential-kind"})
	requireNoError(t, err)
	outputPrincipal, err := agents.runtime.CreateOutputPrincipal(ctx, agents.scope.ScopeToken, CreateOutputPrincipalInput{
		StreamID: stream.ID, Label: "Credential classifier", Permissions: []OutputPermission{OutputRead},
	})
	requireNoError(t, err)

	for name, testCase := range map[string]struct {
		token string
		want  CredentialKind
	}{
		"current agent":               {token: agents.plannerToken, want: CredentialKindAgent},
		"a2a principal":               {token: a2aPrincipal.Credential, want: CredentialKindScopedPrincipal},
		"output principal":            {token: outputPrincipal.Credential, want: CredentialKindScopedPrincipal},
		"scope token is not resolved": {token: agents.scope.ScopeToken, want: CredentialKindUnknown},
		"malformed":                   {token: "malformed", want: CredentialKindUnknown},
		"unknown id":                  {token: "cred_unknown.secret", want: CredentialKindUnknown},
		"wrong secret":                {token: a2aPrincipal.Principal.ID + ".wrong-secret", want: CredentialKindUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := store.CredentialKind(ctx, testCase.token)
			if err != nil || got != testCase.want {
				t.Fatalf("CredentialKind() = %v, %v; want %v, nil", got, err, testCase.want)
			}
		})
	}

	boundary := nowMillis()
	if _, err := store.db.ExecContext(ctx, `UPDATE agents SET lease_expires_at=?,lifecycle='offline' WHERE token_hash=?`, boundary, tokenDigest(agents.reviewerToken)); err != nil {
		t.Fatal(err)
	}
	if kind, err := store.CredentialKind(ctx, agents.reviewerToken); err != nil || kind != CredentialKindUnknown {
		t.Fatalf("expired boundary credential = %v, %v; want unknown", kind, err)
	}

	replacement, err := agents.runtime.RegisterAgent(ctx, agents.scope.ScopeToken, RegisterAgentInput{ID: agents.planner.AgentID, DisplayName: "Replacement"})
	requireNoError(t, err)
	if _, err := store.db.ExecContext(ctx, `UPDATE agents SET lifecycle='offline' WHERE token_hash=?`, tokenDigest(replacement.AgentToken)); err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		token string
		want  CredentialKind
	}{
		"replaced execution":    {token: agents.plannerToken, want: CredentialKindUnknown},
		"offline current agent": {token: replacement.AgentToken, want: CredentialKindAgent},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := store.CredentialKind(ctx, testCase.token)
			if err != nil || got != testCase.want {
				t.Fatalf("CredentialKind() = %v, %v; want %v, nil", got, err, testCase.want)
			}
		})
	}

	if _, err := agents.runtime.SetOutputPrincipalEnabled(ctx, agents.scope.ScopeToken, outputPrincipal.Principal.ID, false); err != nil {
		t.Fatal(err)
	}
	if kind, err := store.CredentialKind(ctx, outputPrincipal.Credential); err != nil || kind != CredentialKindUnknown {
		t.Fatalf("disabled principal credential = %v, %v; want unknown", kind, err)
	}

	rotated, err := agents.runtime.RotateA2APrincipal(ctx, agents.scope.ScopeToken, a2aPrincipal.Principal.ID)
	requireNoError(t, err)
	for name, testCase := range map[string]struct {
		token string
		want  CredentialKind
	}{
		"rotated old secret": {token: a2aPrincipal.Credential, want: CredentialKindUnknown},
		"rotated new secret": {token: rotated.Credential, want: CredentialKindScopedPrincipal},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := store.CredentialKind(ctx, testCase.token)
			if err != nil || got != testCase.want {
				t.Fatalf("CredentialKind() = %v, %v; want %v, nil", got, err, testCase.want)
			}
		})
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.CredentialKind(canceled, replacement.AgentToken); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled classifier returned %v, want context cancellation", err)
	}
}
