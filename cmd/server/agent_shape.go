package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// agentCoreTools are the tool names the Zen free tier expects on an
// agent-shaped request. Requests without them, or with streaming disabled,
// are rejected with 403 FreeTierError. Only the names matter; minimal
// definitions are synthesized for whichever ones the client did not declare.
var agentCoreTools = []string{"bash", "edit", "glob", "grep", "read"}

// shapeFreeModelBody returns a copy of body normalized for the free tier:
// streaming enabled plus the core agent tools present, with token usage kept
// available via stream_options.include_usage. Bodies that already satisfy
// everything (or are not JSON objects) are returned unchanged. The boolean
// reports whether the body changed.
func shapeFreeModelBody(body []byte) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}
	changed := false
	if streaming, ok := payload["stream"].(bool); !ok || !streaming {
		payload["stream"] = true
		changed = true
	}
	if forceIncludeUsage(payload) {
		changed = true
	}
	if forceAgentTools(payload) {
		changed = true
	}
	if !changed {
		return body, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return encoded, true
}

// forceIncludeUsage keeps token usage available when a non-streaming request
// is forced onto the Chat SSE path. OpenAI-compatible Chat streams require
// stream_options.include_usage for the final usage event.
func forceIncludeUsage(payload map[string]any) bool {
	options, ok := payload["stream_options"].(map[string]any)
	if !ok {
		payload["stream_options"] = map[string]any{"include_usage": true}
		return true
	}
	includeUsage, ok := options["include_usage"].(bool)
	if ok && includeUsage {
		return false
	}
	options["include_usage"] = true
	return true
}

// forceAgentTools appends minimal definitions for any missing core tool so
// the request reads as an agent session upstream. Tools the client already
// declared are left untouched.
func forceAgentTools(payload map[string]any) bool {
	raw, exists := payload["tools"]
	if !exists {
		payload["tools"] = agentToolset(nil)
		return true
	}
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	present := make(map[string]bool, len(items))
	for _, item := range items {
		if name := agentToolName(item); name != "" {
			present[name] = true
		}
	}
	missing := make([]string, 0, len(agentCoreTools))
	for _, name := range agentCoreTools {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return false
	}
	payload["tools"] = append(items, agentToolset(missing)...)
	return true
}

func agentToolName(item any) string {
	entry, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	fn, ok := entry["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := fn["name"].(string)
	return name
}

func agentToolset(names []string) []any {
	if names == nil {
		names = agentCoreTools
	}
	tools := make([]any, 0, len(names))
	for _, name := range names {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": "Agent tool " + name,
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		})
	}
	return tools
}

// collapseChatCompletionStream drains an OpenAI-compatible Chat SSE response
// and folds it into a single chat.completion JSON document, mirroring the
// non-streaming wire format (message plus reasoning_content and tool_calls).
func collapseChatCompletionStream(reader io.Reader, fallbackModel string, startedAt time.Time) ([]byte, *tokenUsage, *time.Duration, error) {
	acc := &chatCollapseAccumulator{startedAt: startedAt, toolByIdx: map[int]*chatCollapsedTool{}}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	events := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 {
			continue
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		events++
		if err := acc.consume(payload); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("collapse: reading upstream stream: %w", err)
	}
	if events == 0 {
		return nil, nil, nil, fmt.Errorf("collapse: upstream stream carried no events")
	}
	collapsed := acc.encode(fallbackModel)
	return collapsed, acc.tokenUsage(), acc.firstToken, nil
}

type chatCollapseAccumulator struct {
	id         string
	model      string
	created    int64
	content    strings.Builder
	reasoning  strings.Builder
	tools      []*chatCollapsedTool
	toolByIdx  map[int]*chatCollapsedTool
	finish     string
	usage      map[string]any
	firstToken *time.Duration
	startedAt  time.Time
	marked     bool
}

type chatCollapsedTool struct {
	id   string
	name strings.Builder
	args strings.Builder
}

func (acc *chatCollapseAccumulator) tokenUsage() *tokenUsage {
	if len(acc.usage) == 0 {
		return nil
	}
	return &tokenUsage{
		Prompt:     usageNumber(acc.usage["prompt_tokens"]),
		Completion: usageNumber(acc.usage["completion_tokens"]),
		Total:      usageNumber(acc.usage["total_tokens"]),
	}
}

func (acc *chatCollapseAccumulator) markFirstToken() {
	if acc.marked {
		return
	}
	acc.marked = true
	latency := time.Since(acc.startedAt)
	acc.firstToken = &latency
}

func (acc *chatCollapseAccumulator) consume(payload []byte) error {
	var event struct {
		ID      any                `json:"id"`
		Model   string             `json:"model"`
		Created int64              `json:"created"`
		Choices []chatStreamChoice `json:"choices"`
		Usage   json.RawMessage    `json:"usage"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("collapse: invalid upstream SSE event: %w", err)
	}
	if event.Model != "" && acc.model == "" {
		acc.model = event.Model
	}
	if event.Created != 0 && acc.created == 0 {
		acc.created = event.Created
	}
	if id, ok := event.ID.(string); ok && id != "" && acc.id == "" {
		acc.id = id
	}
	if len(event.Usage) > 0 && !bytes.Equal(event.Usage, []byte("null")) {
		var usage map[string]any
		if json.Unmarshal(event.Usage, &usage) == nil && len(usage) > 0 {
			acc.usage = usage
		}
	}
	for _, choice := range event.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" && *choice.FinishReason != "null" {
			acc.finish = *choice.FinishReason
		}
		acc.consumeDelta(choice.Delta)
	}
	return nil
}

func (acc *chatCollapseAccumulator) consumeDelta(delta chatStreamDelta) {
	contentSeen := false
	if delta.Content != nil && *delta.Content != "" {
		acc.content.WriteString(*delta.Content)
		contentSeen = true
	}
	if delta.Reasoning != nil && *delta.Reasoning != "" {
		acc.reasoning.WriteString(*delta.Reasoning)
		contentSeen = true
	} else if delta.ReasoningLegacy != nil && *delta.ReasoningLegacy != "" {
		acc.reasoning.WriteString(*delta.ReasoningLegacy)
		contentSeen = true
	}
	toolSeen := false
	for _, raw := range delta.ToolCalls {
		toolSeen = true
		idx := raw.Index
		if idx == nil {
			idx = intPtr(len(acc.toolByIdx))
		}
		tool, ok := acc.toolByIdx[*idx]
		if !ok {
			tool = &chatCollapsedTool{}
			acc.toolByIdx[*idx] = tool
			acc.tools = append(acc.tools, tool)
		}
		if raw.ID != "" {
			tool.id = raw.ID
		}
		if raw.Function.Name != "" {
			writeMergedName(&tool.name, raw.Function.Name)
		}
		if raw.Function.Arguments != "" {
			tool.args.WriteString(raw.Function.Arguments)
		}
	}
	if contentSeen || toolSeen {
		acc.markFirstToken()
	}
}

type chatStreamChoice struct {
	FinishReason *string         `json:"finish_reason"`
	Delta        chatStreamDelta `json:"delta"`
}

type chatStreamDelta struct {
	Content         *string         `json:"content"`
	Reasoning       *string         `json:"reasoning_content"`
	ReasoningLegacy *string         `json:"reasoning"`
	ToolCalls       []chatDeltaTool `json:"tool_calls"`
}

type chatDeltaTool struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (acc *chatCollapseAccumulator) encode(fallbackModel string) []byte {
	message := map[string]any{
		"role":    "assistant",
		"content": acc.content.String(),
	}
	if reasoning := acc.reasoning.String(); reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(acc.tools) > 0 {
		toolCalls := make([]any, 0, len(acc.tools))
		for _, tool := range acc.tools {
			toolCalls = append(toolCalls, map[string]any{
				"id":   tool.id,
				"type": "function",
				"function": map[string]any{
					"name":      tool.name.String(),
					"arguments": tool.args.String(),
				},
			})
		}
		message["tool_calls"] = toolCalls
	}
	finish := acc.finish
	if finish == "" {
		finish = "stop"
	}
	model := acc.model
	if model == "" {
		model = fallbackModel
	}
	created := acc.created
	if created == 0 {
		created = time.Now().Unix()
	}
	id := acc.id
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	response := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finish,
			},
		},
	}
	if len(acc.usage) > 0 {
		response["usage"] = acc.usage
	}
	encoded, _ := json.Marshal(response)
	return encoded
}

func writeMergedName(builder *strings.Builder, fragment string) {
	current := builder.String()
	if current == "" {
		builder.WriteString(fragment)
		return
	}
	if fragment == current {
		return
	}
	if strings.HasPrefix(fragment, current) {
		builder.Reset()
		builder.WriteString(fragment)
		return
	}
	builder.WriteString(fragment)
}

func intPtr(v int) *int {
	return &v
}