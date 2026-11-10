package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/inference"
)

const (
	chatDefaultServer   = "http://127.0.0.1:8081"
	chatDefaultContext  = 32768
	chatDefaultMaxOut   = 4096
	chatAnalysisPeriod  = 500 * time.Millisecond
	chatPreferredDshVer = "0.2.2-rc.1"
)

type chatRole int

const (
	chatUser chatRole = iota
	chatAssistant
	chatThinking
	chatTool
	chatError
	chatSystem
)

type chatMessage struct {
	role   chatRole
	text   string
	tool   string
	callID string
	status string
	result string
	at     time.Time
}

type chatReadyMsg struct {
	model    string
	ctx      int
	settings inference.Settings
	err      error
}

type chatEventMsg struct {
	event dshEvent
}

type chatDoneMsg struct {
	err    error
	stderr string
}

type chatAnalysisTickMsg struct {
	gen int
}

type dshReason struct {
	Kind string `json:"kind"`
}

type dshUsage struct {
	InputTokens     int `json:"inputTokens"`
	OutputTokens    int `json:"outputTokens"`
	CacheReadTokens int `json:"cacheReadTokens"`
}

type dshEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Cwd       string          `json:"cwd"`
	Phase     string          `json:"phase"`
	Turn      int             `json:"turn"`
	Step      int             `json:"step"`
	Text      string          `json:"text"`
	Tool      string          `json:"tool"`
	CallID    string          `json:"callId"`
	Input     json.RawMessage `json:"input"`
	Status    string          `json:"status"`
	Result    string          `json:"result"`
	Reason    *dshReason      `json:"reason"`
	Message   string          `json:"message"`
	Usage     *dshUsage       `json:"usage"`
}

type chatModel struct {
	upstream  string
	proxy     *inference.Proxy
	gpu       *hardware.GPU
	workspace string

	ready         bool
	serverErr     string
	modelName     string
	contextWindow int
	defaults      inference.Settings

	messages []chatMessage
	input    []rune
	cursor   int
	history  []string
	histIdx  int

	running    bool
	cancelling bool
	status     string
	sessionID  string

	records    []*inference.Record
	active     []inference.Snapshot
	allRecords []*inference.Record
	turnStart  time.Time
	analysisOn bool
	scroll     int
	tickGen    int

	events chan tea.Msg
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func newChatModel(gpu *hardware.GPU) *chatModel {
	upstream := strings.TrimSpace(os.Getenv("CIRI_CHAT_SERVER"))
	if upstream == "" {
		upstream = chatDefaultServer
	}
	workspace := strings.TrimSpace(os.Getenv("CIRI_CHAT_WORKSPACE"))
	if workspace == "" {
		home, _ := os.UserHomeDir()
		workspace = filepath.Join(home, ".ciri", "chat")
	}
	_ = os.MkdirAll(workspace, 0o755)

	m := &chatModel{
		upstream:      upstream,
		gpu:           gpu,
		workspace:     workspace,
		status:        "CONNECTING",
		contextWindow: chatDefaultContext,
		histIdx:       -1,
	}
	proxy, err := inference.NewProxy(upstream, gpu)
	if err != nil {
		m.serverErr = err.Error()
		m.status = "NO SERVER"
	} else {
		m.proxy = proxy
	}
	return m
}

func (c *chatModel) initCmd() tea.Cmd {
	return c.probeCmd()
}

func (c *chatModel) probeCmd() tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 2500 * time.Millisecond}
		ctx := int(0)
		model := ""
		var defaults inference.Settings
		var errs []string
		ok := false
		if resp, err := client.Get(c.upstream + "/props"); err == nil {
			defer resp.Body.Close()
			var props struct {
				ModelPath  string `json:"model_path"`
				ModelAlias string `json:"model_alias"`
				NCTX       int    `json:"n_ctx"`
				Default    struct {
					NCTX   int `json:"n_ctx"`
					Params struct {
						Temperature   *float64 `json:"temperature"`
						TopP          *float64 `json:"top_p"`
						TopK          *float64 `json:"top_k"`
						MinP          *float64 `json:"min_p"`
						RepeatPenalty *float64 `json:"repeat_penalty"`
						MaxTokens     int      `json:"max_tokens"`
						NPredict      int      `json:"n_predict"`
					} `json:"params"`
				} `json:"default_generation_settings"`
			}
			if json.NewDecoder(resp.Body).Decode(&props) == nil {
				ok = true
				ctx = props.Default.NCTX
				if ctx == 0 {
					ctx = props.NCTX
				}
				if props.ModelAlias != "" {
					model = filepath.Base(props.ModelAlias)
				} else if props.ModelPath != "" {
					model = filepath.Base(props.ModelPath)
				}
				maxTokens := props.Default.Params.MaxTokens
				if maxTokens <= 0 {
					maxTokens = props.Default.Params.NPredict
				}
				defaults = inference.Settings{
					Temperature:   props.Default.Params.Temperature,
					TopP:          props.Default.Params.TopP,
					TopK:          props.Default.Params.TopK,
					MinP:          props.Default.Params.MinP,
					RepeatPenalty: props.Default.Params.RepeatPenalty,
					MaxTokens:     maxTokens,
				}
			}
		} else {
			errs = append(errs, err.Error())
		}
		if resp, err := client.Get(c.upstream + "/v1/models"); err == nil {
			defer resp.Body.Close()
			var models struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.NewDecoder(resp.Body).Decode(&models) == nil {
				ok = true
				if len(models.Data) > 0 && models.Data[0].ID != "" {
					model = models.Data[0].ID
				}
			}
		} else {
			errs = append(errs, err.Error())
		}
		if !ok {
			return chatReadyMsg{err: errors.New(strings.Join(errs, "; "))}
		}
		if ctx <= 0 {
			if env := strings.TrimSpace(os.Getenv("CIRI_CHAT_CONTEXT")); env != "" {
				if v, err := strconv.Atoi(env); err == nil {
					ctx = v
				}
			}
		}
		if ctx <= 0 {
			ctx = chatDefaultContext
		}
		if model == "" {
			model = "local"
		}
		return chatReadyMsg{model: model, ctx: ctx, settings: defaults}
	}
}

func (c *chatModel) waitEvent() tea.Cmd {
	ch := c.events
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (c *chatModel) analysisTick() tea.Cmd {
	gen := c.tickGen
	return tea.Tick(chatAnalysisPeriod, func(time.Time) tea.Msg {
		return chatAnalysisTickMsg{gen: gen}
	})
}

func (c *chatModel) handleMsg(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case chatReadyMsg:
		if msg.err != nil {
			c.ready = false
			c.serverErr = msg.err.Error()
			c.status = "NO SERVER"
			return nil
		}
		c.ready = true
		c.serverErr = ""
		c.modelName = msg.model
		c.contextWindow = msg.ctx
		c.defaults = msg.settings
		if c.running {
			c.status = "RUNNING"
		} else {
			c.status = "READY"
		}
		return nil

	case chatEventMsg:
		c.handleEvent(msg.event)
		if c.running {
			return c.waitEvent()
		}
		return nil

	case chatDoneMsg:
		c.running = false
		c.events = nil
		c.cancel = nil
		c.cmd = nil
		if c.cancelling || errors.Is(msg.err, context.Canceled) {
			c.cancelling = false
			c.status = "CANCELLED"
		} else if msg.err != nil {
			text := strings.TrimSpace(msg.stderr)
			if text == "" {
				text = msg.err.Error()
			}
			if len(c.messages) == 0 || c.messages[len(c.messages)-1].role != chatError {
				c.appendMessage(chatError, text)
			}
			c.status = "ERROR"
		} else {
			c.status = "READY"
		}
		c.refreshRecords()
		c.scroll = 0
		return nil

	case chatAnalysisTickMsg:
		if msg.gen != c.tickGen {
			return nil
		}
		c.refreshRecords()
		if c.running {
			return c.analysisTick()
		}
		return nil
	}
	return nil
}

func (c *chatModel) handleEvent(ev dshEvent) {
	switch ev.Type {
	case "session":
		c.sessionID = ev.SessionID
	case "status":
		switch ev.Phase {
		case "turn_start":
			c.status = "THINKING"
		case "step_start":
			c.status = fmt.Sprintf("STEP %d", ev.Step)
		case "turn_end":
			if ev.Reason != nil && ev.Reason.Kind != "" && ev.Reason.Kind != "completed" {
				c.status = strings.ToUpper(ev.Reason.Kind)
				c.appendMessage(chatSystem, "Turn ended: "+ev.Reason.Kind)
			}
		}
	case "thinking":
		if strings.TrimSpace(ev.Text) != "" {
			c.appendMessage(chatThinking, ev.Text)
			c.status = "THINKING"
		}
	case "text":
		if strings.TrimSpace(ev.Text) != "" {
			c.appendMessage(chatAssistant, ev.Text)
			c.status = "WRITING"
		}
	case "tool_call":
		args := "{}"
		if len(ev.Input) > 0 {
			args = string(ev.Input)
		}
		c.messages = append(c.messages, chatMessage{
			role:   chatTool,
			text:   args,
			tool:   ev.Tool,
			callID: ev.CallID,
			status: "running",
			at:     time.Now(),
		})
		c.status = "TOOL " + strings.ToUpper(ev.Tool)
	case "tool_result":
		for i := len(c.messages) - 1; i >= 0; i-- {
			if c.messages[i].role == chatTool && c.messages[i].callID == ev.CallID {
				c.messages[i].status = ev.Status
				c.messages[i].result = ev.Result
				break
			}
		}
	case "error":
		c.appendMessage(chatError, ev.Message)
		c.status = "ERROR"
	case "final":
		if ev.SessionID != "" {
			c.sessionID = ev.SessionID
		}
	}
	c.scroll = 0
}

func (c *chatModel) appendMessage(role chatRole, text string) {
	c.messages = append(c.messages, chatMessage{role: role, text: strings.TrimRight(text, "\n"), at: time.Now()})
	c.scroll = 0
}

func (c *chatModel) refreshRecords() {
	if c.proxy == nil {
		return
	}
	c.allRecords = c.proxy.RecordsSince(time.Time{})
	if !c.turnStart.IsZero() {
		c.records = c.proxy.RecordsSince(c.turnStart)
		c.active = c.proxy.ActiveSince(c.turnStart)
	}
}

func (c *chatModel) update(a *App, msg tea.KeyMsg) tea.Cmd {
	if c.running {
		switch msg.String() {
		case "esc":
			c.cancelRun()
			c.status = "CANCELLING"
		case "pgup":
			c.scroll += 5
		case "pgdown":
			c.scroll -= 5
			if c.scroll < 0 {
				c.scroll = 0
			}
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		if c.analysisOn {
			c.analysisOn = false
			return nil
		}
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	case "enter":
		return c.sendCmd()
	case "ctrl+o":
		c.analysisOn = !c.analysisOn
		c.scroll = 0
	case "ctrl+n":
		c.newSession()
	case "ctrl+s":
		return c.exportCmd()
	case "ctrl+r":
		c.status = "CONNECTING"
		return c.probeCmd()
	case "pgup":
		c.scroll += 5
	case "pgdown":
		c.scroll -= 5
		if c.scroll < 0 {
			c.scroll = 0
		}
	case "up":
		c.historyPrev()
	case "down":
		c.historyNext()
	case "left":
		if c.cursor > 0 {
			c.cursor--
		}
	case "right":
		if c.cursor < len(c.input) {
			c.cursor++
		}
	case "home", "ctrl+a":
		c.cursor = 0
	case "end", "ctrl+e":
		c.cursor = len(c.input)
	case "backspace":
		if c.cursor > 0 {
			c.input = append(c.input[:c.cursor-1], c.input[c.cursor:]...)
			c.cursor--
		}
	case "delete":
		if c.cursor < len(c.input) {
			c.input = append(c.input[:c.cursor], c.input[c.cursor+1:]...)
		}
	case "ctrl+u":
		c.input = nil
		c.cursor = 0
	case "ctrl+w":
		for c.cursor > 0 && c.input[c.cursor-1] == ' ' {
			c.input = append(c.input[:c.cursor-1], c.input[c.cursor:]...)
			c.cursor--
		}
		for c.cursor > 0 && c.input[c.cursor-1] != ' ' {
			c.input = append(c.input[:c.cursor-1], c.input[c.cursor:]...)
			c.cursor--
		}
	case "space", " ":
		c.insertRunes([]rune{' '})
	default:
		if msg.Type == tea.KeyRunes {
			c.insertRunes(msg.Runes)
		}
	}
	return nil
}

func (c *chatModel) insertRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	clean := make([]rune, 0, len(rs))
	for _, r := range rs {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r < 0x20 {
			continue
		}
		clean = append(clean, r)
	}
	if len(clean) == 0 {
		return
	}
	next := make([]rune, 0, len(c.input)+len(clean))
	next = append(next, c.input[:c.cursor]...)
	next = append(next, clean...)
	next = append(next, c.input[c.cursor:]...)
	c.input = next
	c.cursor += len(clean)
}

func (c *chatModel) historyPrev() {
	if len(c.history) == 0 {
		return
	}
	if c.histIdx < 0 {
		c.histIdx = len(c.history) - 1
	} else if c.histIdx > 0 {
		c.histIdx--
	}
	c.setInput(c.history[c.histIdx])
}

func (c *chatModel) historyNext() {
	if c.histIdx < 0 {
		return
	}
	if c.histIdx < len(c.history)-1 {
		c.histIdx++
		c.setInput(c.history[c.histIdx])
		return
	}
	c.histIdx = -1
	c.setInput("")
}

func (c *chatModel) setInput(s string) {
	c.input = []rune(s)
	c.cursor = len(c.input)
}

func (c *chatModel) newSession() {
	if c.running {
		return
	}
	c.messages = nil
	c.records = nil
	c.sessionID = ""
	c.turnStart = time.Time{}
	c.scroll = 0
	c.status = "READY"
	if !c.ready {
		c.status = "NO SERVER"
	}
}

func (c *chatModel) cancelRun() {
	c.cancelling = true
	if c.cancel != nil {
		c.cancel()
	}
	if c.cmd != nil {
		killChatProcess(c.cmd)
	}
}

func (c *chatModel) close() {
	c.cancelRun()
	if c.proxy != nil {
		_ = c.proxy.Close()
	}
}

func (c *chatModel) exportCmd() tea.Cmd {
	if c.running {
		return nil
	}
	if c.proxy == nil {
		c.appendMessage(chatError, "analysis proxy is unavailable")
		return nil
	}
	records := c.proxy.RecordsSince(time.Time{})
	if len(records) == 0 {
		c.appendMessage(chatSystem, "No analysis to export yet.")
		return nil
	}
	base := filepath.Join(c.workspace, "ciri-chat-"+time.Now().Format("20060102-150405"))
	analysisPath := base + "-analysis.json"
	tracePath := base + "-trace.json"

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		c.appendMessage(chatError, "encode analysis: "+err.Error())
		return nil
	}
	if err := os.WriteFile(analysisPath, data, 0o600); err != nil {
		c.appendMessage(chatError, "write analysis: "+err.Error())
		return nil
	}
	trace, err := json.MarshalIndent(buildTrace(records), "", "  ")
	if err != nil {
		c.appendMessage(chatError, "encode trace: "+err.Error())
		return nil
	}
	if err := os.WriteFile(tracePath, trace, 0o600); err != nil {
		c.appendMessage(chatError, "write trace: "+err.Error())
		return nil
	}
	c.appendMessage(chatSystem, "Analysis: "+analysisPath+"\nTrace: "+tracePath)
	return nil
}

type traceEvent struct {
	Name string         `json:"name"`
	Ph   string         `json:"ph"`
	Ts   int64          `json:"ts"`
	Dur  int64          `json:"dur,omitempty"`
	Pid  int            `json:"pid"`
	Tid  int            `json:"tid"`
	Args map[string]any `json:"args,omitempty"`
}

type traceFile struct {
	TraceEvents []traceEvent `json:"traceEvents"`
}

func buildTrace(records []*inference.Record) traceFile {
	events := []traceEvent{
		{Name: "process_name", Ph: "M", Pid: 1, Args: map[string]any{"name": "ciri chat"}},
		{Name: "thread_name", Ph: "M", Pid: 1, Tid: 1, Args: map[string]any{"name": "model calls"}},
		{Name: "thread_name", Ph: "M", Pid: 1, Tid: 2, Args: map[string]any{"name": "hardware"}},
	}
	for _, rec := range records {
		if rec.Started.IsZero() {
			continue
		}
		args := map[string]any{
			"model":             rec.Model,
			"prompt_tokens":     rec.PromptTokens,
			"cached_tokens":     rec.CachedTokens,
			"completion_tokens": rec.CompletionTokens,
			"prompt_tps":        rec.PromptTPS,
			"generation_tps":    rec.GenerationTPS,
			"ttft_ms":           rec.TTFTMS,
			"answer_ttft_ms":    rec.AnswerTTFTMS,
			"finish_reason":     rec.FinishReason,
			"status":            rec.Status,
		}
		if rec.Settings.Temperature != nil {
			args["temperature"] = *rec.Settings.Temperature
		}
		if rec.Settings.TopP != nil {
			args["top_p"] = *rec.Settings.TopP
		}
		start := rec.Started.UnixMicro()
		if rec.PromptMS > 0 {
			events = append(events, traceEvent{
				Name: "prompt", Ph: "X", Ts: start,
				Dur: int64(rec.PromptMS * 1000),
				Pid: 1, Tid: 1, Args: args,
			})
		}
		genStart := start + int64(rec.TTFTMS*1000)
		if rec.GenerationMS > 0 {
			events = append(events, traceEvent{
				Name: "generation", Ph: "X", Ts: genStart,
				Dur: int64(rec.GenerationMS * 1000),
				Pid: 1, Tid: 1, Args: args,
			})
		}
		for i, m := range rec.GPU {
			if i >= len(rec.GPUTimes) || rec.GPUTimes[i].IsZero() {
				continue
			}
			events = append(events, traceEvent{
				Name: "gpu", Ph: "C", Ts: rec.GPUTimes[i].UnixMicro(),
				Pid: 1, Tid: 2,
				Args: map[string]any{
					"util_pct": m.GPUUtilPct, "mem_util_pct": m.GPUMemUtilPct,
					"vram_gb": m.VRAMUsedGB, "temp_c": m.GPUTempC,
					"power_w": m.GPUPowerW, "sm_mhz": m.GPUSMClockMHz,
				},
			})
		}
	}
	return traceFile{TraceEvents: events}
}

func (c *chatModel) sendCmd() tea.Cmd {
	prompt := strings.TrimSpace(string(c.input))
	if prompt == "" {
		return nil
	}
	if !c.ready {
		msg := c.serverErr
		if msg == "" {
			msg = "llama-server is not reachable at " + c.upstream
		}
		c.appendMessage(chatError, msg)
		return nil
	}
	if c.proxy == nil {
		c.appendMessage(chatError, "inference proxy is unavailable")
		return nil
	}
	bin, args, err := c.dshCommand()
	if err != nil {
		c.appendMessage(chatError, err.Error())
		return nil
	}
	patch, err := c.writePatch()
	if err != nil {
		c.appendMessage(chatError, "write dsh patch: "+err.Error())
		return nil
	}

	c.appendMessage(chatUser, prompt)
	c.history = append(c.history, prompt)
	c.histIdx = -1
	c.input = nil
	c.cursor = 0

	c.running = true
	c.cancelling = false
	c.status = "STARTING"
	c.turnStart = time.Now()
	c.records = nil
	c.scroll = 0
	c.tickGen++
	c.events = make(chan tea.Msg, 256)

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	fullArgs := append([]string{}, args...)
	fullArgs = append(fullArgs, "--profile", "headless", "--patch", patch, "--json")
	if c.sessionID != "" {
		fullArgs = append(fullArgs, "--session-id", c.sessionID)
	}
	fullArgs = append(fullArgs, prompt)

	cmd := exec.CommandContext(ctx, bin, fullArgs...)
	cmd.Dir = c.workspace
	cmd.Env = append(os.Environ(),
		"DSH_PERMISSION_MODE=workspace-write",
		"NO_COLOR=1",
	)
	setChatProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.appendMessage(chatError, err.Error())
		c.running = false
		return nil
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.appendMessage(chatError, err.Error())
		c.running = false
		return nil
	}
	if err := cmd.Start(); err != nil {
		c.appendMessage(chatError, "start dsh: "+err.Error())
		c.running = false
		return nil
	}
	c.cmd = cmd
	events := c.events
	var readers sync.WaitGroup
	readers.Add(2)

	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var ev dshEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			events <- chatEventMsg{event: ev}
		}
	}()

	var stderrText strings.Builder
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 16*1024), 256*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if stderrText.Len() < 8192 {
				stderrText.WriteString(line)
				stderrText.WriteByte('\n')
			}
		}
	}()

	go func() {
		err := cmd.Wait()
		readers.Wait()
		events <- chatDoneMsg{err: err, stderr: strings.TrimSpace(stderrText.String())}
		close(events)
	}()

	return tea.Batch(c.waitEvent(), c.analysisTick())
}

func (c *chatModel) dshCommand() (string, []string, error) {
	if override := strings.TrimSpace(os.Getenv("CIRI_DSH_BIN")); override != "" {
		return override, nil, nil
	}
	if path, err := exec.LookPath("dsh"); err == nil {
		return path, nil, nil
	}
	if path := cachedDshBin(); path != "" {
		return path, nil, nil
	}
	if path, err := exec.LookPath("npx"); err == nil {
		version := strings.TrimSpace(os.Getenv("CIRI_DSH_VERSION"))
		if version == "" {
			version = chatPreferredDshVer
		}
		return path, []string{"--yes", "@deepseek-ai/dsh@" + version}, nil
	}
	return "", nil, errors.New("dsh is not installed: install @deepseek-ai/dsh or set CIRI_DSH_BIN")
}

// cachedDshBin finds a dsh entry point already materialized by npx. Invoking
// it directly avoids npm's registry revalidation on every chat turn.
func cachedDshBin() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".npm", "_npx", "*", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"))
	sort.SliceStable(matches, func(i, j int) bool {
		si, _ := os.Stat(matches[i])
		sj, _ := os.Stat(matches[j])
		if si == nil || sj == nil {
			return matches[i] < matches[j]
		}
		return si.ModTime().After(sj.ModTime())
	})
	for _, path := range matches {
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func (c *chatModel) writePatch() (string, error) {
	path := filepath.Join(c.workspace, ".ciri-dsh-patch.yml")
	maxOut := chatDefaultMaxOut
	if c.contextWindow > 0 && maxOut > c.contextWindow/2 {
		maxOut = c.contextWindow / 2
	}
	if maxOut < 1024 {
		maxOut = 1024
	}
	model := c.modelName
	if model == "" {
		model = "local"
	}
	body := fmt.Sprintf(`# Generated by ciri Chat. Not a user profile.
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      ciri-local:
        displayName: CIRI Chat
        api: openai-completions
        baseURL: %q
        headers:
          Authorization: Bearer llama.cpp
        compat:
          maxTokensField: max_tokens
          supportsStore: false
        models:
          - id: %q
            name: %q
            contextWindow: %d
            maxTokens: %d
            input: [text]
            reasoningEfforts: false
- id: agent-default-model
  name: '@deepseek-ai/dsh-agent-default-model'
  config:
    provider: ciri-local
    model: %q
`, c.proxy.URL()+"/v1", model, model+" (local)", c.contextWindow, maxOut, model)

	for _, id := range []string{
		"session-title-llm",
		"jobs", "tool-jobs",
		"skill", "skill-filesystem", "tool-skill",
		"goal", "goal-round-driver", "command-goal", "tool-goal",
		"plan-mode",
		"subagent", "subagent-spawn-in-process", "subagent-fork-in-process",
		"tool-subagent-control", "tool-subagent-list-agents", "tool-subagent", "tool-subagent-fork",
		"ptc-runtime", "workflow-ptc", "tool-workflow", "tool-todo", "tool-ralph",
		"web", "web-search-deepseek", "web-fetch-http", "web-deepseek-api", "tool-web",
		"mcp-resources",
	} {
		body += "- id: " + id + "\n  disabled: true\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (c *chatModel) view(a *App) string {
	inner := a.width - 4
	if inner < 24 {
		inner = 24
	}
	innerH := a.height - 9
	if innerH < 8 {
		innerH = 8
	}
	bodyH := innerH - 3
	if bodyH < 1 {
		bodyH = 1
	}

	header := alignLine("  "+Wordmark.Render("CHAT")+"  "+Tagline.Render(c.headerInfo()), c.statusStyle().Render(c.status), inner)
	var b strings.Builder
	b.WriteString(header + "\n")
	b.WriteString("  " + Rule.Render(strings.Repeat("\u2500", inner)) + "\n")

	if a.width >= 110 && !c.analysisOn {
		leftW := (inner - 3) * 3 / 5
		rightW := inner - 3 - leftW
		left := c.transcriptLines(leftW)
		right := c.analysisLines(rightW)
		left = windowLines(left, bodyH, c.scroll)
		right = topLines(right, bodyH)
		for i := 0; i < bodyH; i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			b.WriteString(padRight(l, leftW) + Faint.Render(" \u2502 ") + padRight(r, rightW) + "\n")
		}
	} else {
		var lines []string
		if c.analysisOn {
			lines = c.analysisLines(inner)
			lines = windowLines(lines, bodyH, c.scroll)
		} else {
			lines = c.transcriptLines(inner)
			lines = windowLines(lines, bodyH, c.scroll)
		}
		for i := 0; i < bodyH; i++ {
			if i < len(lines) {
				b.WriteString(lines[i] + "\n")
			} else {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("  " + c.inputView(inner-2) + "\n")
	return b.String()
}

func (c *chatModel) headerInfo() string {
	parts := []string{c.modelName}
	if c.contextWindow > 0 {
		parts = append(parts, formatContext(c.contextWindow)+" CTX")
	}
	if c.sessionID != "" {
		id := c.sessionID
		if len(id) > 14 {
			id = id[len(id)-12:]
		}
		parts = append(parts, "SESSION "+id)
	}
	parts = append(parts, "WORKSPACE "+c.workspace)
	return strings.Join(parts, "  \u00b7  ")
}

func (c *chatModel) statusStyle() lipgloss.Style {
	switch c.status {
	case "ERROR", "NO SERVER", "CANCELLED":
		return StatusAlert
	case "READY":
		return FitPerfect
	case "CONNECTING", "STARTING", "CANCELLING":
		return ValueDim
	default:
		return Value
	}
}

func (c *chatModel) transcriptLines(width int) []string {
	if !c.ready {
		title := "LLAMA-SERVER OFFLINE"
		style := StatusAlert
		if c.status == "CONNECTING" {
			title = "PROBING LLAMA-SERVER\u2026"
			style = ValueDim
		}
		lines := []string{
			"",
			"  " + style.Render(title),
			"  " + Tagline.Render(truncate(c.upstream, width-4)),
			"",
			"  " + Value.Render("Start llama-server, or set CIRI_CHAT_SERVER to its base URL."),
			"  " + Tagline.Render("CTRL+R  retry probe"),
		}
		if c.serverErr != "" {
			lines = append(lines, "", "  "+Faint.Render(truncate(c.serverErr, width-4)))
		}
		return lines
	}
	if len(c.messages) == 0 {
		return []string{
			"",
			"  " + Value.Render("Chat with the local model through DeepSeek Harness."),
			"  " + Tagline.Render("Tools run in the dsh workspace. Analysis appears on the right."),
			"",
			"  " + Label.Render("ENTER SEND") + "   " + Label.Render("CTRL+O ANALYSIS") + "   " + Label.Render("CTRL+N NEW SESSION"),
		}
	}
	var lines []string
	for _, m := range c.messages {
		switch m.role {
		case chatUser:
			lines = append(lines, "  "+Heading.Render("YOU"))
			lines = append(lines, wrapBlock(m.text, width-2, Value)...)
		case chatAssistant:
			lines = append(lines, "  "+Heading.Render("CIRI"))
			lines = append(lines, wrapBlock(m.text, width-2, Value)...)
		case chatThinking:
			lines = append(lines, "  "+Label.Render("THINKING"))
			lines = append(lines, wrapBlock(clipText(m.text, 6, 1200), width-2, Tagline)...)
		case chatTool:
			status := m.status
			if status == "" {
				status = "running"
			}
			lines = append(lines, "  "+Label.Render("TOOL")+"  "+Value.Render(strings.ToUpper(m.tool))+"  "+Tagline.Render("[ "+status+" ]"))
			args := clipText(toolArgsSummary(m.tool, m.text), 6, 1200)
			lines = append(lines, wrapBlock(args, width-2, ValueDim)...)
			if m.result != "" {
				lines = append(lines, wrapBlock(clipText(m.result, 10, 2000), width-2, Tagline)...)
			}
		case chatError:
			lines = append(lines, "  "+StatusAlert.Render("ERROR"))
			lines = append(lines, wrapBlock(m.text, width-2, StatusAlert)...)
		case chatSystem:
			lines = append(lines, "  "+Tagline.Render(m.text))
		}
		lines = append(lines, "")
	}
	return lines
}

func wrapBlock(text string, width int, style lipgloss.Style) []string {
	if width < 4 {
		width = 4
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	wrapped := ansi.Wordwrap(text, width, "")
	parts := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, "  "+style.Render(p))
	}
	return out
}

func compactJSON(s string) string {
	var v interface{}
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	enc, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(enc)
}

func toolArgsSummary(tool, raw string) string {
	var v map[string]interface{}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	str := func(k string) string {
		if s, ok := v[k].(string); ok {
			return s
		}
		return ""
	}
	switch strings.ToLower(tool) {
	case "bash":
		if cmd := str("command"); cmd != "" {
			return "$ " + cmd
		}
	case "read", "read_image":
		if p := str("file_path"); p != "" {
			return p
		}
	case "write":
		if p := str("file_path"); p != "" {
			return p + "  (" + formatBytes(int64(len(str("content")))) + ")"
		}
	case "edit":
		if p := str("file_path"); p != "" {
			return p
		}
	case "glob", "grep":
		parts := []string{}
		if p := str("pattern"); p != "" {
			parts = append(parts, p)
		}
		if p := str("path"); p != "" {
			parts = append(parts, "in "+p)
		}
		if len(parts) > 0 {
			return strings.Join(parts, "  ")
		}
	}
	return compactJSON(raw)
}

func clipText(s string, maxLines, maxChars int) string {
	s = strings.TrimRight(s, "\n")
	if maxChars > 0 {
		if r := []rune(s); len(r) > maxChars {
			s = string(r[:maxChars]) + "\n\u2026"
		}
	}
	lines := strings.Split(s, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		return strings.Join(lines, "\n") + "\n\u2026"
	}
	return strings.Join(lines, "\n")
}

func (c *chatModel) analysisLines(width int) []string {
	lines := []string{"  " + Label.Render("ANALYSIS")}
	if c.proxy == nil {
		lines = append(lines, "  "+StatusAlert.Render("PROXY UNAVAILABLE"))
		return lines
	}
	records := c.records
	if c.analysisOn {
		records = c.allRecords
	}
	if len(records) == 0 && len(c.active) == 0 {
		lines = append(lines, "")
		if c.running {
			lines = append(lines, "  "+Value.Render("MODEL CALL STARTING\u2026"))
		} else {
			lines = append(lines,
				"  "+Tagline.Render("No completed model calls in this turn."),
				"  "+Tagline.Render("Send a message to start collecting timing,"),
				"  "+Tagline.Render("KV, and hardware data."),
			)
		}
		return lines
	}

	for _, live := range c.active {
		lines = append(lines, "  "+StatusOn.Render("\u25a0")+" "+Value.Render("LIVE  "+truncate(live.Model, width-14)))
		if live.PromptTokens == 0 && live.CompletionTokens == 0 {
			lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("PROMPT PROCESSING  %s",
				ms(float64(time.Since(live.Started).Milliseconds())))))
		} else {
			lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("P %s %st/s  G %s %st/s",
				formatNum(live.PromptTokens), tps(live.PromptTPS),
				formatNum(live.CompletionTokens), tps(live.GenerationTPS))))
			ttft := fmt.Sprintf("TTFT %s", ms(live.TTFTMS))
			if live.AnswerTTFTMS > 0 {
				ttft += fmt.Sprintf("  ANS %s", ms(live.AnswerTTFTMS))
			}
			if live.ReasoningTokens > 0 {
				ttft += fmt.Sprintf("  think~%d", live.ReasoningTokens)
			}
			lines = append(lines, "  "+Tagline.Render(ttft))
			if live.ContextUsed > 0 && c.contextWindow > 0 {
				lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("KV %s/%s %.0f%%",
					formatNum(live.ContextUsed), formatContext(c.contextWindow),
					float64(live.ContextUsed)*100/float64(c.contextWindow))))
			}
		}
		if len(live.GPU) > 0 {
			avg, max, mem, temp, power, vram := gpuSummary(live.GPU)
			memPart := ""
			if mem > 0 {
				memPart = fmt.Sprintf("  mem %.0f%%", mem)
			}
			lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("GPU %.0f/%.0f%%%s  %.1fG  %.0f\u00b0C  %.0fW  %s",
				avg, max, memPart, vram, temp, power, sparkline(live.GPU))))
		}
		if settings := c.settingsLines(live.Settings, width); len(settings) > 0 {
			lines = append(lines, settings...)
		}
		lines = append(lines, "")
	}

	if len(records) == 0 {
		return lines
	}

	totalPrompt, totalGen, totalWall := 0, 0, 0.0
	for _, rec := range records {
		totalPrompt += rec.PromptTokens
		totalGen += rec.CompletionTokens
		totalWall += rec.TotalMS()
	}
	label := "TURN"
	if c.analysisOn {
		label = "ALL"
	}
	lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("%s  %d CALLS  P %s  G %s  %s",
		label, len(records), formatNum(totalPrompt), formatNum(totalGen), ms(totalWall))))
	lines = append(lines, "  "+Faint.Render("temp/top_p change output, not kernel t/s"))

	start := 0
	if !c.analysisOn && len(records) > 2 {
		start = len(records) - 2
	}
	if c.analysisOn && len(records) > 10 {
		start = len(records) - 10
	}
	for i := start; i < len(records); i++ {
		rec := records[i]
		lines = append(lines, "  "+Label.Render(fmt.Sprintf("REQ %d", i+1))+"  "+
			Value.Render(truncate(rec.Model, width-12))+"  "+Tagline.Render(rec.FinishReason))

		cache := ""
		if rec.CachedTokens > 0 {
			pct := 0
			if rec.PromptTokens > 0 {
				pct = rec.CachedTokens * 100 / rec.PromptTokens
			}
			cache = fmt.Sprintf("  cache %s %d%%", formatNum(rec.CachedTokens), pct)
		}
		lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("P %s %st/s  G %s %st/s",
			formatNum(rec.PromptTokens), tps(rec.PromptTPS),
			formatNum(rec.CompletionTokens), tps(rec.GenerationTPS))))
		think := ""
		if rec.ReasoningTokens > 0 {
			think = fmt.Sprintf("  think~%d", rec.ReasoningTokens)
		}
		ttft := fmt.Sprintf("TTFT %s", ms(rec.TTFTMS))
		if rec.AnswerTTFTMS > 0 {
			ttft += fmt.Sprintf("  ANS %s", ms(rec.AnswerTTFTMS))
		}
		lines = append(lines, "  "+Tagline.Render(ttft+cache+think))

		tok := ""
		if rec.InterP50MS > 0 {
			tok = fmt.Sprintf("  tok %s/%s", ms(rec.InterP50MS), ms(rec.InterP95MS))
		}
		kv := ""
		if rec.ContextUsed > 0 && c.contextWindow > 0 {
			kv = fmt.Sprintf("KV %s/%s %.0f%%", formatNum(rec.ContextUsed),
				formatContext(c.contextWindow), float64(rec.ContextUsed)*100/float64(c.contextWindow))
		}
		lines = append(lines, "  "+Tagline.Render(strings.TrimSpace(kv+tok)))

		if len(rec.GPU) > 0 {
			avg, max, mem, temp, power, vram := gpuSummary(rec.GPU)
			spark := sparkline(rec.GPU)
			memPart := ""
			if mem > 0 {
				memPart = fmt.Sprintf("  mem %.0f%%", mem)
			}
			lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("GPU %.0f/%.0f%%%s  %.1fG  %.0f\u00b0C  %.0fW  %s",
				avg, max, memPart, vram, temp, power, spark)))
			if c.analysisOn {
				lines = append(lines, "  "+Tagline.Render("GPU  "+sparkline(rec.GPU)))
				lines = append(lines, "  "+Tagline.Render("VRAM "+sparklineVRAM(rec.GPU)))
				lines = append(lines, "  "+Tagline.Render("TEMP "+sparklineTemp(rec.GPU)))
				last := rec.GPU[len(rec.GPU)-1]
				if last.GPUSMClockMHz > 0 || last.GPUMemClockMHz > 0 {
					lines = append(lines, "  "+Tagline.Render(fmt.Sprintf("CLK SM %.0f / MEM %.0f MHz",
						last.GPUSMClockMHz, last.GPUMemClockMHz)))
				}
				if last.CPUPercentKnown || last.MemTotalGB > 0 {
					cpu := ""
					if last.CPUPercentKnown {
						cpu = fmt.Sprintf("CPU %.0f%%  ", last.CPUPercent)
					}
					ram := ""
					if last.MemTotalGB > 0 {
						ram = fmt.Sprintf("RAM %.1f/%.1fG", last.MemUsedGB, last.MemTotalGB)
					}
					lines = append(lines, "  "+Tagline.Render(strings.TrimSpace(cpu+ram)))
				}
				if thr := gpuThrottle(rec.GPU); thr != "" {
					lines = append(lines, "  "+StatusAlert.Render("THROTTLE "+thr))
				}
			}
		}
		settings := c.settingsLines(rec.Settings, width)
		lines = append(lines, settings...)
		lines = append(lines, "")
	}
	return lines
}

func windowLines(lines []string, height, scroll int) []string {
	if len(lines) <= height {
		return lines
	}
	maxScroll := len(lines) - height
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := len(lines) - scroll
	start := end - height
	if start < 0 {
		start = 0
	}
	return lines[start:end]
}

func topLines(lines []string, height int) []string {
	if len(lines) <= height {
		return lines
	}
	return lines[:height]
}

func (c *chatModel) inputView(width int) string {
	if width < 8 {
		width = 8
	}
	before := string(c.input[:c.cursor])
	after := string(c.input[c.cursor:])
	// Keep the cursor visible when the input grows past the line.
	maxBefore := width - 6
	if maxBefore < 1 {
		maxBefore = 1
	}
	if r := []rune(before); len(r) > maxBefore {
		before = string(r[len(r)-maxBefore:])
	}
	cursor := Cursor.Render("\u2588")
	line := HintKey.Render(">") + " " + Value.Render(before) + cursor + Value.Render(after)
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, "\u2026")
	}
	return line
}

func tps(v float64) string {
	if v <= 0 {
		return "\u2014"
	}
	return fmt.Sprintf("%.1f", v)
}

func ms(v float64) string {
	if v <= 0 {
		return "\u2014"
	}
	if v >= 1000 {
		return fmt.Sprintf("%.1fs", v/1000)
	}
	return fmt.Sprintf("%.0fms", v)
}

func (c *chatModel) settingsLines(rec inference.Settings, width int) []string {
	s := rec
	if s.Temperature == nil {
		s.Temperature = c.defaults.Temperature
	}
	if s.TopP == nil {
		s.TopP = c.defaults.TopP
	}
	if s.TopK == nil {
		s.TopK = c.defaults.TopK
	}
	if s.MinP == nil {
		s.MinP = c.defaults.MinP
	}
	if s.RepeatPenalty == nil {
		s.RepeatPenalty = c.defaults.RepeatPenalty
	}
	if s.MaxTokens == 0 {
		s.MaxTokens = c.defaults.MaxTokens
	}
	var parts []string
	if s.Temperature != nil {
		parts = append(parts, fmt.Sprintf("temp %.2f", *s.Temperature))
	}
	if s.TopP != nil {
		parts = append(parts, fmt.Sprintf("top_p %.2f", *s.TopP))
	}
	if s.TopK != nil {
		parts = append(parts, fmt.Sprintf("top_k %.0f", *s.TopK))
	}
	if s.MinP != nil {
		parts = append(parts, fmt.Sprintf("min_p %.2f", *s.MinP))
	}
	if s.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("max %d", s.MaxTokens))
	}
	if len(parts) == 0 {
		return []string{"  " + Tagline.Render("settings: server defaults")}
	}
	full := strings.Join(parts, "  ")
	if lipgloss.Width(full)+2 <= width {
		return []string{"  " + Tagline.Render(full)}
	}
	split := 3
	if split > len(parts) {
		split = len(parts)
	}
	var out []string
	if a := strings.Join(parts[:split], "  "); a != "" {
		out = append(out, "  "+Tagline.Render(truncate(a, width-2)))
	}
	if b := strings.Join(parts[split:], "  "); b != "" {
		out = append(out, "  "+Tagline.Render(truncate(b, width-2)))
	}
	return out
}

func gpuSummary(samples []hardware.Metrics) (avg, max, mem, temp, power, vram float64) {
	for _, m := range samples {
		if m.GPUUtilKnown {
			avg += m.GPUUtilPct
			if m.GPUUtilPct > max {
				max = m.GPUUtilPct
			}
		}
		if m.GPUMemUtilKnown {
			mem += m.GPUMemUtilPct
		}
		if m.GPUTempC > temp {
			temp = m.GPUTempC
		}
		if m.GPUPowerW > power {
			power = m.GPUPowerW
		}
		if m.VRAMUsedGB > vram {
			vram = m.VRAMUsedGB
		}
	}
	if len(samples) > 0 {
		avg /= float64(len(samples))
		mem /= float64(len(samples))
	}
	return
}

func gpuThrottle(samples []hardware.Metrics) string {
	for i := len(samples) - 1; i >= 0; i-- {
		s := strings.TrimSpace(samples[i].GPUThrottle)
		if s == "" {
			continue
		}
		hex := strings.TrimPrefix(strings.ToLower(s), "0x")
		if hex == "" {
			continue
		}
		if v, err := strconv.ParseUint(hex, 16, 64); err == nil && v != 0 {
			return s
		}
	}
	return ""
}

func sparkline(samples []hardware.Metrics) string {
	return sparklineWith(samples, func(m hardware.Metrics) float64 {
		if !m.GPUUtilKnown {
			return 0
		}
		return m.GPUUtilPct
	}, 100)
}

func sparklineVRAM(samples []hardware.Metrics) string {
	max := 1.0
	for _, m := range samples {
		if m.VRAMTotalGB > max {
			max = m.VRAMTotalGB
		}
		if m.VRAMUsedGB > max {
			max = m.VRAMUsedGB
		}
	}
	return sparklineWith(samples, func(m hardware.Metrics) float64 {
		return m.VRAMUsedGB
	}, max)
}

func sparklineTemp(samples []hardware.Metrics) string {
	return sparklineWith(samples, func(m hardware.Metrics) float64 {
		return m.GPUTempC
	}, 100)
}

func sparklineWith(samples []hardware.Metrics, value func(hardware.Metrics) float64, max float64) string {
	if len(samples) == 0 || max <= 0 {
		return ""
	}
	runes := []rune("\u2581\u2582\u2583\u2584\u2585\u2586\u2587\u2588")
	step := len(samples) / 24
	if step < 1 {
		step = 1
	}
	var b strings.Builder
	for i := 0; i < len(samples); i += step {
		v := value(samples[i])
		idx := int(v * float64(len(runes)-1) / max)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(runes) {
			idx = len(runes) - 1
		}
		b.WriteRune(runes[idx])
	}
	return b.String()
}
