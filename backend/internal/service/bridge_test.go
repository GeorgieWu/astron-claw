package service

import (
	"testing"
)

func TestTranslateBotEvent_AgentMessageChunk(t *testing.T) {
	params := map[string]interface{}{
		"sessionId": "sess-123",
		"update": map[string]interface{}{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]interface{}{"text": "hello"},
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "chunk" {
		t.Errorf("expected type=chunk, got %v", result["type"])
	}
	if result["content"] != "hello" {
		t.Errorf("expected content=hello, got %v", result["content"])
	}
}

func TestTranslateBotEvent_AgentMessageFinal(t *testing.T) {
	params := map[string]interface{}{
		"sessionId": "sess-123",
		"update": map[string]interface{}{
			"sessionUpdate": "agent_message_final",
			"content":       map[string]interface{}{"text": "final answer"},
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "done" {
		t.Errorf("expected type=done, got %v", result["type"])
	}
	if result["content"] != "final answer" {
		t.Errorf("expected content='final answer', got %v", result["content"])
	}
}

func TestTranslateBotEvent_ToolCall(t *testing.T) {
	params := map[string]interface{}{
		"sessionId": "sess-123",
		"update": map[string]interface{}{
			"sessionUpdate": "tool_call",
			"title":         "search",
			"content":       "query text",
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "tool_call" {
		t.Errorf("expected type=tool_call, got %v", result["type"])
	}
	if result["name"] != "search" {
		t.Errorf("expected name=search, got %v", result["name"])
	}
	if result["input"] != "query text" {
		t.Errorf("expected input='query text', got %v", result["input"])
	}
}

func TestTranslateBotEvent_ToolResult(t *testing.T) {
	params := map[string]interface{}{
		"sessionId": "sess-123",
		"update": map[string]interface{}{
			"sessionUpdate": "tool_result",
			"title":         "search",
			"status":        "completed",
			"content":       map[string]interface{}{"text": "result data"},
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "tool_result" {
		t.Errorf("expected type=tool_result, got %v", result["type"])
	}
	if result["name"] != "search" {
		t.Errorf("expected name=search, got %v", result["name"])
	}
	if result["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", result["status"])
	}
	if result["content"] != "result data" {
		t.Errorf("expected content='result data', got %v", result["content"])
	}
}

func TestTranslateBotEvent_AgentThoughtChunk(t *testing.T) {
	params := map[string]interface{}{
		"sessionId": "sess-123",
		"update": map[string]interface{}{
			"sessionUpdate": "agent_thought_chunk",
			"content":       map[string]interface{}{"text": "thinking..."},
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "thinking" {
		t.Errorf("expected type=thinking, got %v", result["type"])
	}
	if result["content"] != "thinking..." {
		t.Errorf("expected content='thinking...', got %v", result["content"])
	}
}

func TestTranslateBotEvent_AgentMedia(t *testing.T) {
	params := map[string]interface{}{
		"sessionId": "sess-123",
		"update": map[string]interface{}{
			"sessionUpdate": "agent_media",
			"content": map[string]interface{}{
				"text": "Here is the image",
				"media": map[string]interface{}{
					"downloadUrl": "https://example.com/img.png",
				},
			},
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "media" {
		t.Errorf("expected type=media, got %v", result["type"])
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["type"] != "url" {
		t.Errorf("expected data.type=url, got %v", data["type"])
	}
	if data["content"] != "https://example.com/img.png" {
		t.Errorf("expected data.content=url, got %v", data["content"])
	}
	if data["caption"] != "Here is the image" {
		t.Errorf("expected caption, got %v", data["caption"])
	}
}

func TestTranslateBotEvent_WrongMethod(t *testing.T) {
	params := map[string]interface{}{
		"update": map[string]interface{}{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]interface{}{"text": "hello"},
		},
	}
	result := TranslateBotEvent("other/method", params)
	if result != nil {
		t.Errorf("expected nil for non session/update method, got %v", result)
	}
}

func TestTranslateBotEvent_NilParams(t *testing.T) {
	result := TranslateBotEvent("session/update", nil)
	if result != nil {
		t.Errorf("expected nil for nil params, got %v", result)
	}
}

func TestTranslateBotEvent_UnknownUpdateType(t *testing.T) {
	params := map[string]interface{}{
		"update": map[string]interface{}{
			"sessionUpdate": "unknown_type",
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result != nil {
		t.Errorf("expected nil for unknown update type, got %v", result)
	}
}

func TestTranslateBotEvent_ToolCallDefaultTitle(t *testing.T) {
	params := map[string]interface{}{
		"update": map[string]interface{}{
			"sessionUpdate": "tool_call",
			"content":       "input",
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["name"] != "tool" {
		t.Errorf("expected default name=tool, got %v", result["name"])
	}
}

func TestTranslateBotEvent_ToolResultDefaultStatus(t *testing.T) {
	params := map[string]interface{}{
		"update": map[string]interface{}{
			"sessionUpdate": "tool_result",
			"content":       "result",
		},
	}
	result := TranslateBotEvent("session/update", params)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["name"] != "tool" {
		t.Errorf("expected default name=tool, got %v", result["name"])
	}
	if result["status"] != "completed" {
		t.Errorf("expected default status=completed, got %v", result["status"])
	}
}

func TestEnsureEncodedURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/path/file.txt", "https://example.com/path/file.txt"},
		{"https://example.com/path/file name.txt", "https://example.com/path/file%20name.txt"},
		{"https://example.com/path/中文.txt", "https://example.com/path/%E4%B8%AD%E6%96%87.txt"},
	}
	for _, tt := range tests {
		result := ensureEncodedURL(tt.input)
		if result != tt.expected {
			t.Errorf("ensureEncodedURL(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGetNestedString(t *testing.T) {
	m := map[string]interface{}{"key": "value", "num": 42}

	v, ok := getNestedString(m, "key")
	if !ok || v != "value" {
		t.Errorf("expected (value, true), got (%q, %v)", v, ok)
	}

	v, ok = getNestedString(m, "num")
	if ok {
		t.Errorf("expected false for non-string, got true")
	}

	v, ok = getNestedString(m, "missing")
	if ok {
		t.Errorf("expected false for missing key, got true")
	}

	v, ok = getNestedString(nil, "key")
	if ok {
		t.Errorf("expected false for nil map, got true")
	}
}

func TestBeginDrain(t *testing.T) {
	bridge := &ConnectionBridge{}
	if bridge.IsDraining() {
		t.Error("expected IsDraining() == false initially")
	}
	bridge.BeginDrain()
	if !bridge.IsDraining() {
		t.Error("expected IsDraining() == true after BeginDrain()")
	}
}
