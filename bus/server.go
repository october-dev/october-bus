package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxBodyBytes = 1024 * 1024

type ServerOptions struct {
	Host       string
	Port       int
	AdminToken string
	StartedAt  string
}

type Server struct {
	runtime      *Runtime
	options      ServerOptions
	waitContext  context.Context
	cancelWaits  context.CancelFunc
	httpServer   *http.Server
	listener     net.Listener
	mcpHandler   http.Handler
	router       http.Handler
	address      string
	closeOnce    sync.Once
	serveDone    chan error
	shutdown     chan struct{}
	shutdownOnce sync.Once
}

type mcpTokenKey struct{}

func NewServer(runtime *Runtime, options ServerOptions) *Server {
	if options.Host == "" {
		options.Host = "127.0.0.1"
	}
	if options.StartedAt == "" {
		options.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	waitContext, cancelWaits := context.WithCancel(context.Background())
	server := &Server{
		runtime: runtime, options: options,
		waitContext: waitContext, cancelWaits: cancelWaits,
		serveDone: make(chan error, 1), shutdown: make(chan struct{}),
	}
	server.mcpHandler = mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		token, _ := request.Context().Value(mcpTokenKey{}).(string)
		if token == "" {
			return nil
		}
		return server.newMCPServer(token)
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxBodyBytes,
		PropagateRequestCancellation: true,
	})
	server.router = server.newRouter()
	server.httpServer = &http.Server{
		Handler: server, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second,
	}
	return server
}

func (s *Server) Start() (string, error) {
	if s.listener != nil {
		return s.address, nil
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.options.Host, s.options.Port))
	if err != nil {
		return "", err
	}
	s.listener = listener
	s.address = "http://" + listener.Addr().String()
	go func() {
		err := s.httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		} else if err != nil {
			_ = listener.Close()
		}
		s.serveDone <- err
		close(s.serveDone)
	}()
	return s.address, nil
}

func (s *Server) Address() string { return s.address }

func (s *Server) Done() <-chan error { return s.serveDone }

func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Server) requestShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdown) })
}

func (s *Server) Stop(ctx context.Context) error {
	var stopErr error
	s.closeOnce.Do(func() {
		s.cancelWaits()
		if s.listener != nil {
			stopErr = s.httpServer.Shutdown(ctx)
		}
		if err := s.runtime.Close(); stopErr == nil {
			stopErr = err
		}
		s.address = ""
	})
	return stopErr
}

func (s *Server) inboxWaitContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.waitContext, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (s *Server) inboxWaitStopped(parent context.Context, err error) bool {
	return errors.Is(err, context.Canceled) && parent.Err() == nil && s.waitContext.Err() != nil
}

func bearer(request *http.Request) (string, error) {
	scheme, token, found := strings.Cut(request.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", Errorf(CodeUnauthenticated, "Bearer token is required")
	}
	return token, nil
}

func (s *Server) requireAdmin(request *http.Request) error {
	token, err := bearer(request)
	if err != nil || s.options.AdminToken == "" || !secureEqual(token, s.options.AdminToken) {
		return Errorf(CodeUnauthenticated, "Invalid admin token")
	}
	return nil
}

func decodeBody(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return Errorf(CodeInvalidArgument, "Request body exceeds 1 MiB")
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return Errorf(CodeInvalidArgument, "Request body must be valid JSON: "+err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Errorf(CodeInvalidArgument, "Request body must contain one JSON value")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeResult(response http.ResponseWriter, status int, result any) {
	writeJSON(response, status, map[string]any{"ok": true, "result": result})
}

func writeFailure(response http.ResponseWriter, err error) {
	failure := AsBusError(err)
	writeJSON(response, ErrorStatus(failure), map[string]any{
		"ok":    false,
		"error": map[string]any{"code": failure.Code, "message": failure.Message},
	})
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	s.router.ServeHTTP(response, request)
}

type emptyInput struct{}

func (s *Server) newMCPServer(token string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "october-bus", Version: Version}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "list_peers", Description: "List linked agents and their capabilities."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
			peers, err := s.runtime.ListPeers(ctx, token)
			return nil, map[string]any{"peers": peers}, err
		})
	type messagePeerInput struct {
		Peer           string        `json:"peer" jsonschema:"exact agent id preferred, or unique exact display name"`
		Message        string        `json:"message" jsonschema:"message body"`
		Mode           MessageMode   `json:"mode,omitempty" jsonschema:"notify, request, or response; use response when responseTo is set"`
		ResponseTo     string        `json:"responseTo,omitempty" jsonschema:"original request message id; requires mode response"`
		IdempotencyKey string        `json:"idempotencyKey,omitempty"`
		ExpiresInMS    int64         `json:"expiresInMs,omitempty"`
		Context        []ContextItem `json:"context,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "message_peer", Description: "Send a durable notification, request, or response to a linked peer."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input messagePeerInput) (*mcp.CallToolResult, any, error) {
			peer, err := s.resolvePeer(ctx, token, input.Peer)
			if err != nil {
				return nil, nil, err
			}
			result, err := s.runtime.SendMessage(ctx, token, SendMessageInput{
				To: peer.ID, Body: input.Message, Mode: input.Mode, ResponseTo: input.ResponseTo,
				IdempotencyKey: input.IdempotencyKey, ExpiresInMS: input.ExpiresInMS, Context: input.Context,
			})
			return nil, result, err
		})
	type inboxInput struct {
		Limit  int   `json:"limit,omitempty"`
		WaitMS int64 `json:"waitMs,omitempty" jsonschema:"wait for messages for up to 25000 milliseconds"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "check_inbox", Description: "Receive durable messages waiting for this agent. Pass waitMs up to 25000 to wait for new messages instead of polling."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input inboxInput) (*mcp.CallToolResult, any, error) {
			waitContext, cancel := s.inboxWaitContext(ctx)
			defer cancel()
			reservation, err := s.runtime.ReserveInbox(waitContext, token, input.Limit, input.WaitMS)
			if s.inboxWaitStopped(ctx, err) {
				return nil, map[string]any{"messages": []Message{}}, nil
			}
			if err != nil || reservation == nil {
				if reservation == nil && err == nil {
					return nil, map[string]any{"messages": []Message{}}, nil
				}
				return nil, nil, err
			}
			messages, err := s.runtime.CommitInbox(ctx, token, reservation.ID)
			return nil, map[string]any{"messages": messages, "acknowledgement": "Call acknowledge_messages after processing."}, err
		})
	type acknowledgeInput struct {
		MessageIDs []string `json:"messageIds"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "acknowledge_messages", Description: "Acknowledge delivered messages after processing them."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input acknowledgeInput) (*mcp.CallToolResult, any, error) {
			count, err := s.runtime.AcknowledgeMessages(ctx, token, input.MessageIDs)
			return nil, map[string]int64{"acknowledged": count}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "add_task", Description: "Add a shared task with optional dependencies."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input AddTaskInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.AddTask(ctx, token, input)
			return nil, result, err
		})
	type taskIDInput struct {
		TaskID string `json:"taskId"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "claim_task", Description: "Claim a ready shared task."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input taskIDInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.ClaimTask(ctx, token, input.TaskID)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "release_task", Description: "Release a task claimed by this execution."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input taskIDInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.ReleaseTask(ctx, token, input.TaskID)
			return nil, result, err
		})
	type completeTaskInput struct {
		TaskID string `json:"taskId"`
		Note   string `json:"note,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "complete_task", Description: "Complete a task claimed by this agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input completeTaskInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.CompleteTask(ctx, token, input.TaskID, input.Note)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "list_tasks", Description: "List shared tasks and dependency state."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.ListTasks(ctx, token)
			return nil, map[string]any{"tasks": result}, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "ask_user", Description: "Request human input or permission."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input AskHumanInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.AskHuman(ctx, token, input)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "get_node_status", Description: "Return this agent's identity, lease, and lifecycle."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
			result, err := s.runtime.NodeStatus(ctx, token)
			return nil, result, err
		})
	return server
}

func (s *Server) resolvePeer(ctx context.Context, token, value string) (Agent, error) {
	if err := validateText(value, "peer", 256, false); err != nil {
		return Agent{}, err
	}
	peers, err := s.runtime.ListPeers(ctx, token)
	if err != nil {
		return Agent{}, err
	}
	for _, peer := range peers {
		if peer.ID == value {
			return peer, nil
		}
	}
	matches := []Agent{}
	for _, peer := range peers {
		if strings.EqualFold(peer.DisplayName, value) {
			matches = append(matches, peer)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return Agent{}, Errorf(CodeNotFound, "Linked peer "+value+" was not found")
	}
	return Agent{}, Errorf(CodeConflict, "Peer "+value+" is ambiguous")
}
