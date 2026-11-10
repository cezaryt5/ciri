package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/inference"
)

func testChatModel(t *testing.T) *chatModel {
	t.Helper()
	proxy, err := inference.NewProxy("http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	temp := 0.7
	topP := 0.95
	topK := 20.0
	start := time.Now().Add(-2 * time.Second)
	rec := &inference.Record{
		ID:               0,
		Model:            "test-model",
		Started:          start,
		Ended:            start.Add(1500 * time.Millisecond),
		PromptTokens:     2048,
		CachedTokens:     1536,
		ProcessedPrompt:  512,
		CompletionTokens: 128,
		PromptMS:         900,
		PromptTPS:        568.9,
		GenerationMS:     4000,
		GenerationTPS:    32,
		TTFTMS:           950,
		InterP50MS:       31,
		InterP95MS:       44,
		FinishReason:     "stop",
		Status:           "completed",
		ContextUsed:      2176,
		Settings: inference.Settings{
			Temperature: &temp, TopP: &topP, TopK: &topK,
		},
		GPU: []hardware.Metrics{
			{GPUUtilPct: 98, GPUUtilKnown: true, VRAMUsedGB: 6.8, GPUTempC: 84, GPUPowerW: 110},
			{GPUUtilPct: 99, GPUUtilKnown: true, VRAMUsedGB: 6.9, GPUTempC: 85, GPUPowerW: 112},
			{GPUUtilPct: 100, GPUUtilKnown: true, VRAMUsedGB: 6.9, GPUTempC: 84, GPUPowerW: 111},
		},
		GPUTimes: []time.Time{start, start.Add(500 * time.Millisecond), start.Add(time.Second)},
	}
	records := []*inference.Record{rec, rec}
	active := []inference.Snapshot{{
		Model: "test-model", Started: time.Now().Add(-time.Second),
		PromptTokens: 2048, CachedTokens: 1536, CompletionTokens: 64,
		PromptTPS: 500, GenerationTPS: 31, TTFTMS: 900, AnswerTTFTMS: 1500, ReasoningTokens: 24,
		ContextUsed: 2112, GPU: rec.GPU,
		Settings: inference.Settings{Temperature: &temp, TopP: &topP, TopK: &topK},
	}}
	return &chatModel{
		proxy:         proxy,
		ready:         true,
		modelName:     "test-model",
		contextWindow: 32768,
		workspace:     "/tmp/chat",
		status:        "READY",
		messages: []chatMessage{
			{role: chatUser, text: "Hello"},
			{role: chatThinking, text: "The user said hello. I should answer briefly."},
			{role: chatAssistant, text: "Hello! How can I help?\n\nI can write a document or run a command."},
			{role: chatTool, tool: "bash", callID: "1", status: "completed", text: `{"command":"pwd"}`, result: "/tmp/chat"},
		},
		records:    records,
		active:     active,
		allRecords: records,
	}
}

func TestChatViewFitsTerminal(t *testing.T) {
	for _, width := range []int{60, 80, 120, 160} {
		app := testApp("1.0.0")
		app.width = width
		app.height = 30
		app.screen = screenChat
		app.chat = testChatModel(t)

		for _, analysis := range []bool{false, true} {
			app.chat.analysisOn = analysis
			view := app.View()
			lines := strings.Split(view, "\n")
			if len(lines) > app.height {
				t.Fatalf("width %d analysis=%v: %d lines exceeds height", width, analysis, len(lines))
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w > width {
					t.Fatalf("width %d analysis=%v: line %d is %d cells: %q", width, analysis, i, w, line)
				}
			}
			if !strings.Contains(view, "CHAT") {
				t.Fatalf("width %d: view missing CHAT title", width)
			}
		}
	}
}

func TestChatViewEmptyServerState(t *testing.T) {
	app := testApp("1.0.0")
	app.width = 100
	app.height = 24
	app.screen = screenChat
	app.chat = &chatModel{status: "NO SERVER", workspace: "/tmp/chat"}
	view := app.View()
	if !strings.Contains(view, "NO SERVER") {
		t.Fatalf("view missing server state:\n%s", view)
	}
}

func TestChatHandleEventToolLifecycle(t *testing.T) {
	c := &chatModel{status: "READY"}
	c.handleEvent(dshEvent{
		Type: "tool_call", Tool: "bash", CallID: "abc",
		Input: []byte(`{"command":"pwd"}`),
	})
	if len(c.messages) != 1 || c.messages[0].role != chatTool || c.messages[0].status != "running" {
		t.Fatalf("tool call not recorded: %+v", c.messages)
	}
	c.handleEvent(dshEvent{
		Type: "tool_result", CallID: "abc", Status: "completed", Result: "/tmp\n",
	})
	if c.messages[0].status != "completed" || c.messages[0].result != "/tmp\n" {
		t.Fatalf("tool result not recorded: %+v", c.messages[0])
	}
	c.handleEvent(dshEvent{Type: "text", Text: "done"})
	if c.messages[len(c.messages)-1].role != chatAssistant {
		t.Fatalf("assistant text not recorded: %+v", c.messages)
	}
}

func TestChatTraceExportShape(t *testing.T) {
	m := testChatModel(t)
	rec := m.records[0]
	trace := buildTrace([]*inference.Record{rec})
	names := map[string]bool{}
	for _, ev := range trace.TraceEvents {
		names[ev.Name] = true
	}
	for _, want := range []string{"prompt", "generation", "gpu"} {
		if !names[want] {
			t.Fatalf("trace missing %q: %+v", want, trace.TraceEvents)
		}
	}
}

func TestChatInputEditing(t *testing.T) {
	c := &chatModel{}
	c.insertRunes([]rune("hello"))
	if string(c.input) != "hello" || c.cursor != 5 {
		t.Fatalf("input = %q cursor=%d", c.input, c.cursor)
	}
	c.update(nil, teaKey("left"))
	c.insertRunes([]rune("X"))
	if string(c.input) != "hellXo" {
		t.Fatalf("input = %q", c.input)
	}
	c.update(nil, teaKey("backspace"))
	if string(c.input) != "hello" {
		t.Fatalf("input = %q", c.input)
	}
}

func TestChatProbeReadsProps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model_alias": "qwen-test.gguf",
				"default_generation_settings": map[string]any{
					"n_ctx": 32768,
					"params": map[string]any{
						"temperature": 0.8, "top_p": 0.95, "top_k": 40,
						"min_p": 0.05, "repeat_penalty": 1.0, "max_tokens": -1,
						"n_predict": 2048,
					},
				},
			})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "qwen-test"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := &chatModel{upstream: srv.URL}
	msg := m.probeCmd()().(chatReadyMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.model != "qwen-test" || msg.ctx != 32768 {
		t.Fatalf("probe = %+v", msg)
	}
	if msg.settings.Temperature == nil || *msg.settings.Temperature != 0.8 {
		t.Fatalf("default settings = %+v", msg.settings)
	}
	if msg.settings.MaxTokens != 2048 {
		t.Fatalf("max tokens = %d, want fallback from n_predict", msg.settings.MaxTokens)
	}
}

func TestApp_OpenChatCreatesModel(t *testing.T) {
	app := testApp("1.0.0")
	cmd := app.home.homeUpdate(app, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("home enter must navigate")
	}
	updated, _ := app.Update(cmd())
	got := updated.(*App)
	if got.screen != screenChat || got.chat == nil {
		t.Fatalf("screen=%v chat=%v", got.screen, got.chat)
	}
	got.chat.close()
}

func teaKey(s string) (k tea.KeyMsg) {
	switch s {
	case "left":
		k.Type = tea.KeyLeft
	case "right":
		k.Type = tea.KeyRight
	case "backspace":
		k.Type = tea.KeyBackspace
	default:
		k.Type = tea.KeyRunes
		k.Runes = []rune(s)
	}
	return k
}
