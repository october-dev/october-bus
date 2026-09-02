package bus

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type routeHandler func(http.ResponseWriter, *http.Request) error

type routeMethod struct {
	method  string
	handler routeHandler
}

type routeInfo struct {
	methods []string
	all     bool
}

func (*routeInfo) ServeHTTP(http.ResponseWriter, *http.Request) {}

type serverRouter struct {
	methods *http.ServeMux
	paths   *http.ServeMux
}

func newServerRouter() *serverRouter {
	return &serverRouter{methods: http.NewServeMux(), paths: http.NewServeMux()}
}

func (r *serverRouter) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	pathHandler, pattern := r.paths.Handler(request)
	info, matched := pathHandler.(*routeInfo)
	if pattern == "" || !matched {
		writeFailure(response, Errorf(CodeNotFound, "Route not found"))
		return
	}
	if !info.all && !containsMethod(info.methods, request.Method) {
		response.Header().Set("Allow", strings.Join(info.methods, ", "))
		writeFailure(response, Errorf(CodeMethodNotAllowed, "Method not allowed"))
		return
	}
	_, pattern = r.methods.Handler(request)
	if pattern == "" {
		writeFailure(response, Errorf(CodeNotFound, "Route not found"))
		return
	}
	r.methods.ServeHTTP(response, request)
}

func (s *Server) newRouter() http.Handler {
	router := newServerRouter()

	registerRoute(router, "/health",
		routeMethod{http.MethodGet, s.health},
	)
	registerAllMethods(router, "/mcp", s.serveMCP)
	registerRoute(router, "/v1/admin/shutdown",
		routeMethod{http.MethodPost, s.shutdownServer},
	)
	registerRoute(router, "/v1/scopes",
		routeMethod{http.MethodPost, s.createScope},
	)
	registerRoute(router, "/v1/agents",
		routeMethod{http.MethodGet, s.listAgents},
		routeMethod{http.MethodPost, s.registerAgent},
	)
	registerRoute(router, "/v1/links",
		routeMethod{http.MethodPost, s.linkAgents},
	)
	registerRoute(router, "/v1/me/heartbeat",
		routeMethod{http.MethodPatch, s.heartbeat},
	)
	registerRoute(router, "/v1/peers",
		routeMethod{http.MethodGet, s.listPeers},
	)
	registerRoute(router, "/v1/messages",
		routeMethod{http.MethodPost, s.sendMessage},
	)
	registerRoute(router, "/v1/messages/ack",
		routeMethod{http.MethodPost, s.acknowledgeMessages},
	)
	registerRoute(router, "/v1/messages/{messageId}",
		routeMethod{http.MethodGet, s.messageReceipt},
	)
	registerRoute(router, "/v1/inbox/reserve",
		routeMethod{http.MethodPost, s.reserveInbox},
	)
	registerRoute(router, "/v1/inbox/{reservationId}/commit",
		routeMethod{http.MethodPost, s.commitInbox},
	)
	registerRoute(router, "/v1/inbox/{reservationId}/release",
		routeMethod{http.MethodPost, s.releaseInbox},
	)
	registerRoute(router, "/v1/tasks",
		routeMethod{http.MethodGet, s.listTasks},
		routeMethod{http.MethodPost, s.addTask},
	)
	registerRoute(router, "/v1/tasks/{taskId}/claim",
		routeMethod{http.MethodPost, s.claimTask},
	)
	registerRoute(router, "/v1/tasks/{taskId}/release",
		routeMethod{http.MethodPost, s.releaseTask},
	)
	registerRoute(router, "/v1/tasks/{taskId}/complete",
		routeMethod{http.MethodPost, s.completeTask},
	)
	registerRoute(router, "/v1/tasks/{taskId}/progress",
		routeMethod{http.MethodGet, s.listTaskProgress},
		routeMethod{http.MethodPost, s.addTaskProgress},
	)
	registerRoute(router, "/v1/escalations",
		routeMethod{http.MethodPost, s.askHuman},
	)
	registerRoute(router, "/v1/escalations/{escalationId}",
		routeMethod{http.MethodGet, s.getEscalation},
	)
	registerRoute(router, "/v1/scope/escalations",
		routeMethod{http.MethodGet, s.listEscalations},
	)
	registerRoute(router, "/v1/scope/escalations/{escalationId}/resolve",
		routeMethod{http.MethodPost, s.resolveEscalation},
	)
	registerRoute(router, "/v1/scope/storage",
		routeMethod{http.MethodGet, s.storageSummary},
	)
	registerRoute(router, "/v1/scope/storage/prune",
		routeMethod{http.MethodPost, s.pruneScope},
	)
	registerRoute(router, "/v1/events",
		routeMethod{http.MethodGet, s.events},
	)
	return router
}

func registerRoute(router *serverRouter, pattern string, methods ...routeMethod) {
	allowed := make([]string, 0, len(methods))
	for _, route := range methods {
		allowed = append(allowed, route.method)
		router.methods.Handle(route.method+" "+pattern, handleRoute(route.handler))
	}
	router.paths.Handle(pattern, &routeInfo{methods: allowed})
}

func registerAllMethods(router *serverRouter, pattern string, handler http.HandlerFunc) {
	router.methods.HandleFunc(pattern, handler)
	router.paths.Handle(pattern, &routeInfo{all: true})
}

func containsMethod(methods []string, target string) bool {
	for _, method := range methods {
		if method == target {
			return true
		}
	}
	return false
}

func handleRoute(handler routeHandler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := handler(response, request); err != nil {
			writeFailure(response, err)
		}
	})
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) error {
	writeJSON(response, http.StatusOK, Health{
		Name: "october-bus", ProtocolVersion: ProtocolVersion, RuntimeVersion: Version,
		Status: "ready", StartedAt: s.options.StartedAt,
	})
	return nil
}

func (s *Server) serveMCP(response http.ResponseWriter, request *http.Request) {
	token, err := bearer(request)
	if err == nil {
		_, err = s.runtime.Principal(request.Context(), token)
	}
	if err != nil {
		writeFailure(response, err)
		return
	}
	request = request.WithContext(context.WithValue(request.Context(), mcpTokenKey{}, token))
	s.mcpHandler.ServeHTTP(response, request)
}

func (s *Server) shutdownServer(response http.ResponseWriter, request *http.Request) error {
	if err := s.requireAdmin(request); err != nil {
		return err
	}
	var input emptyInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	writeResult(response, http.StatusAccepted, map[string]bool{"stopping": true})
	s.requestShutdown()
	return nil
}

func (s *Server) createScope(response http.ResponseWriter, request *http.Request) error {
	if err := s.requireAdmin(request); err != nil {
		return err
	}
	var input CreateScopeInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.CreateScope(request.Context(), input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, result)
	return nil
}

func (s *Server) registerAgent(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input RegisterAgentInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.RegisterAgent(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, result)
	return nil
}

func (s *Server) listAgents(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.ListAgents(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) linkAgents(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input struct {
		Left  string `json:"left"`
		Right string `json:"right"`
	}
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	if err := s.runtime.LinkAgents(request.Context(), token, input.Left, input.Right); err != nil {
		return err
	}
	writeResult(response, http.StatusOK, map[string]bool{"linked": true})
	return nil
}

func (s *Server) heartbeat(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input HeartbeatInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.Heartbeat(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) listPeers(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.ListPeers(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) sendMessage(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input SendMessageInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.SendMessage(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusAccepted, result)
	return nil
}

func (s *Server) acknowledgeMessages(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input struct {
		MessageIDs []string `json:"messageIds"`
	}
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	count, err := s.runtime.AcknowledgeMessages(request.Context(), token, input.MessageIDs)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, map[string]int64{"acknowledged": count})
	return nil
}

func (s *Server) messageReceipt(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.Receipt(request.Context(), token, request.PathValue("messageId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) reserveInbox(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input struct {
		Limit  int   `json:"limit,omitempty"`
		WaitMS int64 `json:"waitMs,omitempty"`
	}
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	parentContext := request.Context()
	waitContext, cancel := s.inboxWaitContext(parentContext)
	defer cancel()
	result, err := s.runtime.ReserveInbox(waitContext, token, input.Limit, input.WaitMS)
	if s.inboxWaitStopped(parentContext, err) {
		writeResult(response, http.StatusOK, nil)
		return nil
	}
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) commitInbox(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.CommitInbox(request.Context(), token, request.PathValue("reservationId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) releaseInbox(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	if err := s.runtime.ReleaseInbox(request.Context(), token, request.PathValue("reservationId")); err != nil {
		return err
	}
	writeResult(response, http.StatusOK, map[string]bool{"released": true})
	return nil
}

func (s *Server) addTask(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input AddTaskInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.AddTask(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, result)
	return nil
}

func (s *Server) listTasks(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	readyOnly := false
	if value := request.URL.Query().Get("ready"); value != "" {
		if value != "true" && value != "false" {
			return Errorf(CodeInvalidArgument, "ready must be true or false")
		}
		readyOnly = value == "true"
	}
	result, err := s.runtime.ListTasks(request.Context(), token, readyOnly)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) claimTask(response http.ResponseWriter, request *http.Request) error {
	return s.taskAction(response, request, s.runtime.ClaimTask)
}

func (s *Server) releaseTask(response http.ResponseWriter, request *http.Request) error {
	return s.taskAction(response, request, s.runtime.ReleaseTask)
}

func (s *Server) taskAction(response http.ResponseWriter, request *http.Request, action func(context.Context, string, string) (Task, error)) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := action(request.Context(), token, request.PathValue("taskId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) completeTask(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input struct {
		Note string `json:"note,omitempty"`
	}
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.CompleteTask(request.Context(), token, request.PathValue("taskId"), input.Note)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) addTaskProgress(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input AddTaskProgressInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.AddTaskProgress(request.Context(), token, request.PathValue("taskId"), input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, result)
	return nil
}

func (s *Server) listTaskProgress(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.ListTaskProgress(request.Context(), token, request.PathValue("taskId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) storageSummary(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.StorageSummary(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) pruneScope(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input PruneScopeInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.PruneScope(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func queryInt64(request *http.Request, name string, defaultValue int64) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, Errorf(CodeInvalidArgument, name+" must be an integer")
	}
	return parsed, nil
}

func (s *Server) events(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	after, err := queryInt64(request, "after", 0)
	if err != nil {
		return err
	}
	limitValue, err := queryInt64(request, "limit", defaultEventLimit)
	if err != nil {
		return err
	}
	waitMS, err := queryInt64(request, "waitMs", 0)
	if err != nil {
		return err
	}
	if limitValue < 1 || limitValue > maxEventLimit {
		return Errorf(CodeInvalidArgument, "limit must be between 1 and 100")
	}
	if waitMS < 0 || waitMS > maxEventWaitMS {
		return Errorf(CodeInvalidArgument, "waitMs must be between 0 and 25000")
	}
	parentContext := request.Context()
	waitContext, cancel := s.inboxWaitContext(parentContext)
	defer cancel()
	result, err := s.runtime.Events(waitContext, token, after, int(limitValue), time.Duration(waitMS)*time.Millisecond)
	if s.inboxWaitStopped(parentContext, err) {
		result, err = s.runtime.Events(parentContext, token, after, int(limitValue), 0)
	}
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) askHuman(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input AskHumanInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.AskHuman(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, result)
	return nil
}

func (s *Server) getEscalation(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.Escalation(request.Context(), token, request.PathValue("escalationId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) listEscalations(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.ListEscalations(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) resolveEscalation(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input struct {
		Answer string `json:"answer"`
	}
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.ResolveEscalation(request.Context(), token, request.PathValue("escalationId"), input.Answer)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}
