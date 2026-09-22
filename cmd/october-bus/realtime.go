package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/october-dev/october-bus/bus"
)

// This file adds the realtime consumption layer:
//
//	october-bus watch         — NDJSON scope-event stream over the existing
//	                            revision-cursor long-poll (the daemon wakes
//	                            waiters on appendEvent, so latency is ~push).
//	october-bus inbox inject  — one-shot formatted drain of an agent inbox,
//	                            for harness prompt-injection hooks.
//	october-bus message send  — durable peer message straight from the CLI.
//
// None of these change the daemon or the protocol; they consume the existing
// /v1/events and inbox surfaces.

const watchMaxBackoff = 30 * time.Second

// scopeReadClient resolves a scope-credential client for read surfaces like
// /v1/events: --scope uses protected local credentials; otherwise it falls
// back to OCTOBER_BUS_SCOPE_TOKEN + --address (the scopeClient helper).
func scopeReadClient(scope, address string) (bus.Client, error) {
	if scope != "" {
		return localScopeClient(scope)
	}
	return scopeClient(address)
}

// agentOp resolves an agent-scoped client for inbox/send operations. Inside
// `agent run` or an MCP-managed env it uses OCTOBER_BUS_AGENT_TOKEN +
// OCTOBER_BUS_ADDRESS directly; otherwise --scope/--agent/--name self-register
// a short session (same path as `mcp stdio`). The returned cleanup must run
// before exit.
func agentOp(ctx context.Context, scope, agentID, name string) (bus.Client, func(context.Context) error, error) {
	address := strings.TrimRight(strings.TrimSpace(os.Getenv("OCTOBER_BUS_ADDRESS")), "/")
	token := strings.TrimSpace(os.Getenv("OCTOBER_BUS_AGENT_TOKEN"))
	if token != "" {
		if scope != "" || agentID != "" {
			return bus.Client{}, nil, errors.New("use either managed agent credentials or --scope/--agent, not both")
		}
		if address == "" {
			return bus.Client{}, nil, errors.New("OCTOBER_BUS_AGENT_TOKEN is set but OCTOBER_BUS_ADDRESS is empty")
		}
		return bus.Client{Address: address, Token: token}, func(context.Context) error { return nil }, nil
	}
	if scope == "" || agentID == "" {
		return bus.Client{}, nil, errors.New("requires managed agent credentials or --scope <scope-id> --agent <id>")
	}
	if name == "" {
		name = agentID
	}
	owner, err := localScopeClient(scope)
	if err != nil {
		return bus.Client{}, nil, err
	}
	session, err := bus.StartAgentSession(ctx, bus.AgentSessionOptions{
		Address: owner.Address, ScopeToken: owner.Token, HeartbeatInterval: 5 * time.Second,
		Registration: bus.RegisterAgentInput{ID: agentID, DisplayName: name, LeaseMS: 30_000},
	})
	if err != nil {
		return bus.Client{}, nil, fmt.Errorf("could not start agent session: %w", err)
	}
	return session.Client, session.Close, nil
}

func runWatch(args []string) error {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	scope := flags.String("scope", "", "local scope ID (protected local credentials)")
	address := flags.String("address", "", "October Bus address")
	agent := flags.String("agent", "", "only print events that name this agent id")
	var typeFilter stringList
	flags.Var(&typeFilter, "type", "event type prefix to include, repeatable (default: all)")
	from := flags.Int64("from", -1, "event revision to start after (-1 = live tail)")
	wait := flags.Int("wait", 25_000, "long-poll wait per request, ms (max 25000)")
	limit := flags.Int("limit", 100, "events per batch (max 100)")
	once := flags.Bool("once", false, "print one batch then exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("watch does not accept positional arguments")
	}
	if *wait < 0 || *wait > 25_000 {
		return errors.New("--wait must be between 0 and 25000")
	}
	if *limit < 1 || *limit > 100 {
		return errors.New("--limit must be between 1 and 100")
	}
	client, err := scopeReadClient(*scope, *address)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --from -1 means "start at the head": one non-blocking read seeds the
	// cursor without replaying history.
	after := *from
	if after < 0 {
		batch, err := client.Events(ctx, 0, 1, 0)
		if err != nil {
			return fmt.Errorf("could not read event log: %w", err)
		}
		after = batch.CurrentRevision
	}

	enc := json.NewEncoder(os.Stdout)
	backoff := time.Second
	for {
		batch, err := client.Events(ctx, after, *limit, time.Duration(*wait)*time.Millisecond)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "watch: %v — retrying in %s\n", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < watchMaxBackoff {
				backoff *= 2
				if backoff > watchMaxBackoff {
					backoff = watchMaxBackoff
				}
			}
			continue
		}
		backoff = time.Second
		if batch.ResyncRequired {
			fmt.Fprintf(os.Stderr, "watch: event cursor behind retention window — resyncing to revision %d\n", batch.MinimumCursor)
			after = batch.MinimumCursor
		}
		for _, event := range batch.Events {
			if !watchInclude(event, *agent, typeFilter) {
				continue
			}
			if err := enc.Encode(event); err != nil {
				return err
			}
		}
		if batch.NextRevision > after {
			after = batch.NextRevision
		}
		if *once {
			return nil
		}
	}
}

func watchInclude(event bus.BusEvent, agent string, typeFilter []string) bool {
	if len(typeFilter) > 0 {
		matched := false
		for _, prefix := range typeFilter {
			if strings.HasPrefix(event.Type, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if agent == "" {
		return true
	}
	for _, value := range event.Attributes {
		if value == agent {
			return true
		}
	}
	return false
}

func runInboxInject(args []string) error {
	flags := flag.NewFlagSet("inbox inject", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	scope := flags.String("scope", "", "local scope ID (protected local credentials)")
	agent := flags.String("agent", "", "agent id whose inbox to drain")
	name := flags.String("name", "", "agent display name (defaults to --agent)")
	wait := flags.Int("wait", 0, "long-poll wait for new messages, ms (max 25000)")
	limit := flags.Int("limit", 50, "maximum messages to drain")
	ack := flags.Bool("ack", false, "acknowledge printed messages so they do not redeliver")
	jsonOutput := flags.Bool("json", false, "print raw JSON instead of prompt text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("inbox inject does not accept positional arguments")
	}
	if *wait < 0 || *wait > 25_000 {
		return errors.New("--wait must be between 0 and 25000")
	}
	ctx := context.Background()
	client, closeSession, err := agentOp(ctx, *scope, *agent, *name)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = closeSession(cleanup)
	}()

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(*wait)*time.Millisecond+10*time.Second)
	defer cancel()
	messages, err := client.PullInbox(callCtx, *limit, time.Duration(*wait)*time.Millisecond)
	if err != nil {
		return fmt.Errorf("could not pull inbox: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}
	if *ack {
		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, message.ID)
		}
		if _, err := client.AcknowledgeMessages(ctx, ids); err != nil {
			return fmt.Errorf("could not acknowledge messages: %w", err)
		}
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(messages)
	}
	displayAgent := *agent
	if displayAgent == "" {
		displayAgent = os.Getenv("OCTOBER_BUS_AGENT_ID")
	}
	fmt.Printf("--- october-bus: %d pending message(s) for %s ---\n", len(messages), displayAgent)
	for _, message := range messages {
		fmt.Printf("[%s] %s -> %s (%s): %s\n", message.CreatedAt, message.From, message.To, message.Mode, message.Body)
		if message.ResponseTo != "" {
			fmt.Printf("    in response to %s\n", message.ResponseTo)
		}
	}
	fmt.Println("--- end bus messages ---")
	return nil
}

func runMessageSend(args []string) error {
	flags := flag.NewFlagSet("message send", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	scope := flags.String("scope", "", "local scope ID (protected local credentials)")
	agent := flags.String("agent", "", "sender agent id")
	name := flags.String("name", "", "sender display name (defaults to --agent)")
	to := flags.String("to", "", "recipient agent id")
	body := flags.String("body", "", "message body")
	useStdin := flags.Bool("stdin", false, "read the message body from stdin")
	mode := flags.String("mode", string(bus.MessageRequest), "message mode: notify | request | response")
	replyTo := flags.String("reply-to", "", "message id this responds to")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("message send does not accept positional arguments")
	}
	if *to == "" {
		return errors.New("message send requires --to <agent-id>")
	}
	text := *body
	if *useStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("could not read stdin: %w", err)
		}
		text = strings.TrimSpace(string(data))
	}
	if text == "" {
		return errors.New("message send requires --body or --stdin")
	}
	messageMode := bus.MessageMode(*mode)
	switch messageMode {
	case bus.MessageNotify, bus.MessageRequest, bus.MessageResponse:
	default:
		return fmt.Errorf("unknown --mode %q (notify | request | response)", *mode)
	}

	ctx := context.Background()
	client, closeSession, err := agentOp(ctx, *scope, *agent, *name)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = closeSession(cleanup)
	}()

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	receipt, err := client.SendMessage(callCtx, bus.SendMessageInput{
		To: *to, Body: text, Mode: messageMode, ResponseTo: *replyTo,
	})
	if err != nil {
		return fmt.Errorf("could not send message: %w", err)
	}
	fmt.Printf("%s -> %s: %s\n", receipt.MessageID, *to, receipt.State)
	return nil
}
