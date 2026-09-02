package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	Address string
	Token   string
	HTTP    *http.Client
}

type responseEnvelope[T any] struct {
	OK     bool `json:"ok"`
	Result T    `json:"result"`
	Error  struct {
		Code    ErrorCode `json:"code"`
		Message string    `json:"message"`
	} `json:"error"`
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func request[T any](ctx context.Context, client Client, method, path string, input any) (T, error) {
	var zero T
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return zero, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.Address+path, body)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if client.Token != "" {
		req.Header.Set("Authorization", "Bearer "+client.Token)
	}
	response, err := client.httpClient().Do(req)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()
	var envelope responseEnvelope[T]
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return zero, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.OK {
		if envelope.Error.Message != "" {
			return zero, Errorf(envelope.Error.Code, envelope.Error.Message)
		}
		return zero, fmt.Errorf("October Bus request failed with HTTP %d", response.StatusCode)
	}
	return envelope.Result, nil
}

func (c Client) Health(ctx context.Context) (Health, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Address+"/health", nil)
	if err != nil {
		return Health{}, err
	}
	response, err := c.httpClient().Do(req)
	if err != nil {
		return Health{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("health check failed with HTTP %d", response.StatusCode)
	}
	var health Health
	err = json.NewDecoder(response.Body).Decode(&health)
	return health, err
}

func (c Client) CreateScope(ctx context.Context, input CreateScopeInput) (CreateScopeResult, error) {
	return request[CreateScopeResult](ctx, c, http.MethodPost, "/v1/scopes", input)
}

func (c Client) Shutdown(ctx context.Context) error {
	_, err := request[map[string]bool](ctx, c, http.MethodPost, "/v1/admin/shutdown", map[string]any{})
	return err
}

func (c Client) RegisterAgent(ctx context.Context, input RegisterAgentInput) (RegisterAgentResult, error) {
	return request[RegisterAgentResult](ctx, c, http.MethodPost, "/v1/agents", input)
}

func (c Client) ListAgents(ctx context.Context) ([]Agent, error) {
	return request[[]Agent](ctx, c, http.MethodGet, "/v1/agents", nil)
}

func (c Client) LinkAgents(ctx context.Context, left, right string) error {
	_, err := request[map[string]bool](ctx, c, http.MethodPost, "/v1/links", map[string]string{"left": left, "right": right})
	return err
}

func (c Client) Heartbeat(ctx context.Context, input HeartbeatInput) (Agent, error) {
	return request[Agent](ctx, c, http.MethodPatch, "/v1/me/heartbeat", input)
}

func (c Client) ListPeers(ctx context.Context) ([]Agent, error) {
	return request[[]Agent](ctx, c, http.MethodGet, "/v1/peers", nil)
}

func (c Client) SendMessage(ctx context.Context, input SendMessageInput) (DeliveryReceipt, error) {
	return request[DeliveryReceipt](ctx, c, http.MethodPost, "/v1/messages", input)
}

func (c Client) Receipt(ctx context.Context, messageID string) (DeliveryReceipt, error) {
	return request[DeliveryReceipt](ctx, c, http.MethodGet, "/v1/messages/"+url.PathEscape(messageID), nil)
}

func (c Client) ReserveInbox(ctx context.Context, limit int, wait time.Duration) (*InboxReservation, error) {
	waitMS := wait.Milliseconds()
	if wait > 0 && waitMS == 0 {
		waitMS = 1
	}
	return request[*InboxReservation](ctx, c, http.MethodPost, "/v1/inbox/reserve", map[string]any{"limit": limit, "waitMs": waitMS})
}

func (c Client) CommitInbox(ctx context.Context, reservationID string) ([]Message, error) {
	return request[[]Message](ctx, c, http.MethodPost, "/v1/inbox/"+url.PathEscape(reservationID)+"/commit", map[string]any{})
}

func (c Client) ReleaseInbox(ctx context.Context, reservationID string) error {
	_, err := request[map[string]bool](ctx, c, http.MethodPost, "/v1/inbox/"+url.PathEscape(reservationID)+"/release", map[string]any{})
	return err
}

func (c Client) PullInbox(ctx context.Context, limit int, wait time.Duration) ([]Message, error) {
	reservation, err := c.ReserveInbox(ctx, limit, wait)
	if err != nil || reservation == nil {
		return nil, err
	}
	return c.CommitInbox(ctx, reservation.ID)
}

func (c Client) AcknowledgeMessages(ctx context.Context, messageIDs []string) (int64, error) {
	result, err := request[map[string]int64](ctx, c, http.MethodPost, "/v1/messages/ack", map[string]any{"messageIds": messageIDs})
	return result["acknowledged"], err
}

func (c Client) AddTask(ctx context.Context, input AddTaskInput) (Task, error) {
	return request[Task](ctx, c, http.MethodPost, "/v1/tasks", input)
}

func (c Client) ListTasks(ctx context.Context, readyOnly bool) ([]Task, error) {
	path := "/v1/tasks"
	if readyOnly {
		path += "?ready=true"
	}
	return request[[]Task](ctx, c, http.MethodGet, path, nil)
}

func (c Client) ClaimTask(ctx context.Context, taskID string) (Task, error) {
	return request[Task](ctx, c, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/claim", map[string]any{})
}

func (c Client) ReleaseTask(ctx context.Context, taskID string) (Task, error) {
	return request[Task](ctx, c, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/release", map[string]any{})
}

func (c Client) CompleteTask(ctx context.Context, taskID, note string) (Task, error) {
	return request[Task](ctx, c, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/complete", map[string]string{"note": note})
}

func (c Client) AskHuman(ctx context.Context, input AskHumanInput) (HumanEscalation, error) {
	return request[HumanEscalation](ctx, c, http.MethodPost, "/v1/escalations", input)
}

func (c Client) Escalation(ctx context.Context, id string) (HumanEscalation, error) {
	return request[HumanEscalation](ctx, c, http.MethodGet, "/v1/escalations/"+url.PathEscape(id), nil)
}

func (c Client) ListEscalations(ctx context.Context) ([]HumanEscalation, error) {
	return request[[]HumanEscalation](ctx, c, http.MethodGet, "/v1/scope/escalations", nil)
}

func (c Client) ResolveEscalation(ctx context.Context, id, answer string) (HumanEscalation, error) {
	return request[HumanEscalation](ctx, c, http.MethodPost, "/v1/scope/escalations/"+url.PathEscape(id)+"/resolve", map[string]string{"answer": answer})
}
