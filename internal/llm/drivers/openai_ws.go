package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"forge/internal/llm"

	"github.com/gorilla/websocket"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
)

type wsResponseCreate struct {
	Type               string                                  `json:"type"`
	Model              string                                  `json:"model"`
	Store              bool                                    `json:"store"`
	Input              []responses.ResponseInputItemUnionParam `json:"input"`
	Tools              []responses.ToolUnionParam              `json:"tools,omitempty"`
	Instructions       string                                  `json:"instructions,omitempty"`
	PreviousResponseID string                                  `json:"previous_response_id,omitempty"`
	Include            []string                                `json:"include,omitempty"`
	PromptCacheKey     string                                  `json:"prompt_cache_key,omitempty"`
	MaxOutputTokens    int                                     `json:"max_output_tokens,omitempty"`
	Temperature        float64                                 `json:"temperature,omitempty"`
}

type wsServerEvent struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

func (e *wsServerEvent) UnmarshalJSON(data []byte) error {
	type alias wsServerEvent
	if err := json.Unmarshal(data, (*alias)(e)); err != nil {
		return err
	}
	e.Raw = data
	return nil
}

func (d *OpenAIDriver) wsAvailable() bool {
	return d.wsAuthManager != nil && d.wsBaseURL != ""
}

func (d *OpenAIDriver) wsEnsureConnection(ctx context.Context) (*websocket.Conn, error) {
	d.wsMu.Lock()
	defer d.wsMu.Unlock()

	if d.wsFallbackHTTP {
		return nil, fmt.Errorf("websocket disabled for this session")
	}
	if d.wsConn != nil {
		return d.wsConn, nil
	}

	headers, err := d.wsAuthHeaders(ctx)
	if err != nil {
		d.wsFallbackHTTP = true
		return nil, fmt.Errorf("websocket auth: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, d.wsBaseURL, headers)
	if err != nil {
		d.wsFallbackHTTP = true
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	d.wsConn = conn
	d.wsLastRequestID = ""
	return conn, nil
}

func (d *OpenAIDriver) wsAuthHeaders(ctx context.Context) (http.Header, error) {
	h := http.Header{}
	if d.wsAuthManager != nil {
		token, accountID, err := d.wsAuthManager.Authorization(ctx)
		if err != nil {
			return nil, err
		}
		h.Set("Authorization", "Bearer "+token)
		if accountID != "" {
			h.Set("ChatGPT-Account-Id", accountID)
		}
		return h, nil
	}
	return nil, fmt.Errorf("no auth manager available")
}

func (d *OpenAIDriver) wsStreamResponses(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.NativeToolOptions, out chan<- llm.Token) error {
	if !d.wsAvailable() {
		return fmt.Errorf("ws not available")
	}

	conn, err := d.wsEnsureConnection(ctx)
	if err != nil {
		return err
	}

	err = d.wsSendAndRead(ctx, conn, messages, tools, out, true)
	if err != nil && d.wsPrevNotFound(err) {
		return d.wsSendAndRead(ctx, conn, messages, tools, out, false)
	}
	return err
}

func (d *OpenAIDriver) wsPrevNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "previous_response_not_found")
}

func (d *OpenAIDriver) wsSendAndRead(ctx context.Context, conn *websocket.Conn, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.Token, useDelta bool) error {
	instructions := responseInstructions(messages)
	inputMessages := stripSystemMessages(messages)
	store := param.NewOpt(false)

	var prevResponseID string
	d.mu.Lock()
	lastCount := 0
	for _, m := range d.lastMessages {
		if m.Role != llm.RoleSystem {
			lastCount++
		}
	}
	d.mu.Unlock()

	if useDelta && d.wsLastRequestID != "" && lastCount > 0 && len(inputMessages) > lastCount {
		inputMessages = inputMessages[lastCount:]
		prevResponseID = d.wsLastRequestID
	}

	req := wsResponseCreate{
		Type:               "response.create",
		Model:              d.apiModel,
		Store:              store.Value,
		Input:              toResponseInput(inputMessages),
		Tools:              toolDefsToResponses(tools),
		Instructions:       instructions,
		PreviousResponseID: prevResponseID,
		PromptCacheKey:     responsePromptCacheKey(d.apiModel, instructions),
	}

	if d.providerRequiresStatelessResponses() {
		req.Include = append(req.Include, "reasoning.encrypted_content")
	}
	if d.params.MaxTokens > 0 {
		req.MaxOutputTokens = d.params.MaxTokens
	}
	if d.params.Temperature >= 0 && d.modelSupportsTemperature() {
		req.Temperature = d.params.Temperature
	}

	d.wsMu.Lock()
	err := conn.WriteJSON(req)
	d.wsMu.Unlock()
	if err != nil {
		d.wsDisconnect()
		return fmt.Errorf("ws write: %w", err)
	}

	return d.wsReadEvents(ctx, conn, out, len(tools) > 0)
}

func (d *OpenAIDriver) wsReadEvents(ctx context.Context, conn *websocket.Conn, out chan<- llm.Token, hasTools bool) error {
	var outputChars int
	var completedOutput []responses.ResponseOutputItemUnion
	var responseID string
	var sawCompleted bool

	readCh := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		defer close(readCh)
		for {
			var raw json.RawMessage
			err := conn.ReadJSON(&raw)
			if err != nil {
				if isWSCloseError(err) {
					errCh <- nil
					return
				}
				errCh <- err
				return
			}

			var evt wsServerEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}

			switch evt.Type {
			case "response.output_text.delta":
				var delta responses.ResponseTextDeltaEvent
				if err := json.Unmarshal(raw, &delta); err != nil {
					continue
				}
				if delta.Delta == "" {
					continue
				}
				outputChars += len(delta.Delta)
				select {
				case out <- llm.Token{Text: delta.Delta}:
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				}

			case "response.output_item.done":
				var item responses.ResponseOutputItemDoneEvent
				if err := json.Unmarshal(raw, &item); err != nil {
					continue
				}
				completedOutput = append(completedOutput, item.Item)

			case "response.completed":
				var done responses.ResponseCompletedEvent
				if err := json.Unmarshal(raw, &done); err != nil {
					continue
				}
				responseID = done.Response.ID
				if len(done.Response.Output) > 0 {
					completedOutput = append(completedOutput, done.Response.Output...)
				}
				usage := llm.Usage{}
				if done.Response.Usage.InputTokens > 0 || done.Response.Usage.OutputTokens > 0 {
					usage.InputTokens = int(done.Response.Usage.InputTokens)
					usage.OutputTokens = int(done.Response.Usage.OutputTokens)
				}
				if usage.InputTokens > 0 || usage.OutputTokens > 0 {
					d.mu.Lock()
					d.lastUsage = usage
					d.mu.Unlock()
				}
				sawCompleted = true

			case "error":
				var errMsg struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal(raw, &errMsg); err != nil {
					continue
				}
				if errMsg.Code == "previous_response_not_found" {
					d.wsMu.Lock()
					d.wsLastRequestID = ""
					d.wsMu.Unlock()
				}
				errCh <- fmt.Errorf("ws error: %s - %s", errMsg.Code, errMsg.Message)
				return

			case "response.created", "response.in_progress", "response.output_item.added",
				"response.content_part.added", "response.content_part.done",
				"response.function_call_arguments.delta", "response.function_call_arguments.done",
				"response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	<-readCh

	if !sawCompleted && outputChars == 0 && len(completedOutput) == 0 {
		return fmt.Errorf("ws: no content received")
	}

	if responseID != "" {
		d.wsMu.Lock()
		d.wsLastRequestID = responseID
		d.wsMu.Unlock()
	}

	if hasTools && len(completedOutput) > 0 {
		if err := emitResponsesFunctionCalls(ctx, out, completedOutput); err != nil {
			return err
		}
	}

	return nil
}

func (d *OpenAIDriver) wsDisconnect() {
	d.wsMu.Lock()
	defer d.wsMu.Unlock()
	if d.wsConn != nil {
		msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = d.wsConn.WriteMessage(websocket.CloseMessage, msg)
		_ = d.wsConn.Close()
		d.wsConn = nil
	}
	d.wsLastRequestID = ""
}

func isWSCloseError(err error) bool {
	if err == nil {
		return true
	}
	if _, ok := err.(*websocket.CloseError); ok {
		return true
	}
	return strings.Contains(err.Error(), "close")
}
