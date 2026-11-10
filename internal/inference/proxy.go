package inference

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cezaryt5/ciri/internal/hardware"
)

// Settings is the sampling configuration a client sent to llama-server.
// Pointers distinguish "not sent" from a real zero.
type Settings struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	TopK            *float64 `json:"top_k,omitempty"`
	MinP            *float64 `json:"min_p,omitempty"`
	RepeatPenalty   *float64 `json:"repeat_penalty,omitempty"`
	Seed            *float64 `json:"seed,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	Stream          bool     `json:"stream,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

// Record is one model request observed by the proxy.
type Record struct {
	ID      int
	Model   string
	Started time.Time
	Ended   time.Time

	Settings Settings
	Streamed bool

	PromptTokens     int
	CachedTokens     int
	ProcessedPrompt  int
	CompletionTokens int

	PromptMS        float64
	PromptTPS       float64
	GenerationMS    float64
	GenerationTPS   float64
	TTFTMS          float64
	AnswerTTFTMS    float64
	InterP50MS      float64
	InterP95MS      float64
	ReasoningTokens int

	FinishReason string
	Status       string
	Err          string
	ContextUsed  int

	GPU      []hardware.Metrics
	GPUTimes []time.Time

	mu    sync.Mutex
	times []time.Time
}

// Snapshot is an immutable view of a request that is still running.
type Snapshot struct {
	ID               int
	Model            string
	Started          time.Time
	Settings         Settings
	Streamed         bool
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
	PromptTPS        float64
	GenerationTPS    float64
	TTFTMS           float64
	AnswerTTFTMS     float64
	ReasoningTokens  int
	FinishReason     string
	ContextUsed      int
	GPU              []hardware.Metrics
	GPUTimes         []time.Time
}

func (r *Record) snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	gpu := append([]hardware.Metrics(nil), r.GPU...)
	gpuTimes := append([]time.Time(nil), r.GPUTimes...)
	used := r.ContextUsed
	if used == 0 {
		used = r.PromptTokens + r.CompletionTokens
	}
	return Snapshot{
		ID:               r.ID,
		Model:            r.Model,
		Started:          r.Started,
		Settings:         r.Settings,
		Streamed:         r.Streamed,
		PromptTokens:     r.PromptTokens,
		CachedTokens:     r.CachedTokens,
		CompletionTokens: r.CompletionTokens,
		PromptTPS:        r.PromptTPS,
		GenerationTPS:    r.GenerationTPS,
		TTFTMS:           r.TTFTMS,
		AnswerTTFTMS:     r.AnswerTTFTMS,
		ReasoningTokens:  r.ReasoningTokens,
		FinishReason:     r.FinishReason,
		ContextUsed:      used,
		GPU:              gpu,
		GPUTimes:         gpuTimes,
	}
}

func (r *Record) TotalMS() float64 {
	if r.Started.IsZero() || r.Ended.IsZero() {
		return 0
	}
	return float64(r.Ended.Sub(r.Started)) / float64(time.Millisecond)
}

// Proxy is a transparent OpenAI-compatible reverse proxy in front of
// llama-server. It records request settings, response timings, streaming token
// pacing, and GPU samples without changing the bytes seen by the client.
type Proxy struct {
	upstream *url.URL
	listener net.Listener
	server   *http.Server
	client   *http.Client
	reverse  *httputil.ReverseProxy
	gpu      *hardware.GPU

	mu      sync.Mutex
	records []*Record
	active  map[int]*Record
	nextID  int
}

func NewProxy(upstream string, gpu *hardware.GPU) (*Proxy, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(upstream), "/"))
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", upstream, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("upstream %q needs a scheme and host", upstream)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	p := &Proxy{
		upstream: u,
		listener: ln,
		gpu:      gpu,
		active:   make(map[int]*Record),
		client: &http.Client{
			Transport: transport,
		},
	}
	p.reverse = httputil.NewSingleHostReverseProxy(u)
	p.reverse.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("inference upstream: %v", err), http.StatusBadGateway)
	}
	p.server = &http.Server{Handler: p}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}

func (p *Proxy) URL() string {
	return "http://" + p.listener.Addr().String()
}

func (p *Proxy) Close() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions") {
		p.handleChat(w, r)
		return
	}
	p.reverse.ServeHTTP(w, r)
}

func (p *Proxy) RecordsSince(t time.Time) []*Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*Record, 0, len(p.records))
	for _, rec := range p.records {
		if !rec.Started.Before(t) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

func (p *Proxy) AllRecords() []*Record {
	return p.RecordsSince(time.Time{})
}

// ActiveSince returns immutable snapshots of requests that are still running.
func (p *Proxy) ActiveSince(t time.Time) []Snapshot {
	p.mu.Lock()
	recs := make([]*Record, 0, len(p.active))
	for _, rec := range p.active {
		if !rec.Started.Before(t) {
			recs = append(recs, rec)
		}
	}
	p.mu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].Started.Before(recs[j].Started) })
	out := make([]Snapshot, 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec.snapshot())
	}
	return out
}

type chatRequest struct {
	Model           string          `json:"model"`
	Temperature     *float64        `json:"temperature"`
	TopP            *float64        `json:"top_p"`
	TopK            *float64        `json:"top_k"`
	MinP            *float64        `json:"min_p"`
	RepeatPenalty   *float64        `json:"repeat_penalty"`
	Seed            *float64        `json:"seed"`
	MaxTokens       int             `json:"max_tokens"`
	Stream          bool            `json:"stream"`
	ReasoningEffort string          `json:"reasoning_effort"`
	Raw             json.RawMessage `json:"-"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptDetails    *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type timings struct {
	CacheN             int     `json:"cache_n"`
	PromptN            int     `json:"prompt_n"`
	PromptMS           float64 `json:"prompt_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int     `json:"predicted_n"`
	PredictedMS        float64 `json:"predicted_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage   *usage   `json:"usage"`
	Timings *timings `json:"timings"`
	Tokens  []int    `json:"tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage   *usage   `json:"usage"`
	Timings *timings `json:"timings"`
}

func (p *Proxy) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		http.Error(w, "read request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req chatRequest
	_ = json.Unmarshal(body, &req)
	req.Raw = body

	rec := &Record{
		Model:    req.Model,
		Started:  time.Now(),
		Settings: settingsOf(req),
		Streamed: req.Stream,
		Status:   "running",
	}
	p.mu.Lock()
	rec.ID = p.nextID
	p.nextID++
	p.active[rec.ID] = rec
	p.mu.Unlock()
	stopSampler := p.startSampler(rec)
	finish := func(status string, err error) {
		stopSampler()
		p.finish(rec, status, err)
	}
	defer stopSampler()

	// Ask llama-server for per-chunk timings when it understands the extension.
	if len(body) > 0 {
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil && fields != nil {
			fields["timings_per_token"] = json.RawMessage("true")
			if next, err := json.Marshal(fields); err == nil {
				body = next
			}
		}
	}

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		p.upstream.String()+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		finish("error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(upReq.Header, r.Header)
	upReq.Header.Del("Host")
	upReq.Header.Del("Content-Length")
	upReq.Header.Set("Accept-Encoding", "identity")

	resp, err := p.client.Do(upReq)
	if err != nil {
		finish("error", err)
		http.Error(w, "inference upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.Header().Del("Content-Length")
	w.Header().Del("Transfer-Encoding")
	w.WriteHeader(resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_, _ = w.Write(data)
		finish("error", fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data))))
		return
	}

	if req.Stream || strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		p.stream(w, resp.Body, rec, finish)
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		finish("error", err)
		return
	}
	_, _ = w.Write(data)
	p.parseResponse(data, rec)
	finish("completed", nil)
}

func (p *Proxy) stream(w http.ResponseWriter, body io.Reader, rec *Record, finish func(string, error)) {
	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReaderSize(body, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(line); werr != nil {
				finish("cancelled", werr)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			if payload, ok := ssePayload(line); ok {
				p.parseStreamPayload(payload, rec)
			}
		}
		if err != nil {
			if err == io.EOF {
				finish("completed", nil)
			} else {
				finish("cancelled", err)
			}
			return
		}
	}
}

func ssePayload(line []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, false
	}
	payload := bytes.TrimSpace(trimmed[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return nil, false
	}
	return payload, true
}

func (p *Proxy) parseStreamPayload(payload []byte, rec *Record) {
	var chunk chatChunk
	if json.Unmarshal(payload, &chunk) != nil {
		return
	}
	now := time.Now()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if chunk.Usage != nil {
		applyUsage(rec, chunk.Usage)
	}
	if chunk.Timings != nil {
		applyTimings(rec, chunk.Timings)
	}
	if len(chunk.Tokens) > 0 {
		rec.CompletionTokens = maxInt(rec.CompletionTokens, len(chunk.Tokens))
	}
	content := false
	reasoning := false
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" || choice.Message.Content != "" {
			content = true
		}
		if choice.Delta.ReasoningContent != "" {
			reasoning = true
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			rec.FinishReason = *choice.FinishReason
		}
	}
	if content || reasoning {
		if reasoning && rec.TTFTMS == 0 {
			rec.TTFTMS = float64(now.Sub(rec.Started)) / float64(time.Millisecond)
		}
		if content && rec.AnswerTTFTMS == 0 {
			rec.AnswerTTFTMS = float64(now.Sub(rec.Started)) / float64(time.Millisecond)
		}
		if reasoning {
			rec.ReasoningTokens++
		}
		rec.tokenAt(now)
	}
}

func (p *Proxy) parseResponse(data []byte, rec *Record) {
	var resp chatResponse
	if json.Unmarshal(data, &resp) != nil {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if resp.Usage != nil {
		applyUsage(rec, resp.Usage)
	}
	if resp.Timings != nil {
		applyTimings(rec, resp.Timings)
	}
	for _, choice := range resp.Choices {
		if choice.FinishReason != "" {
			rec.FinishReason = choice.FinishReason
		}
	}
	if rec.TTFTMS == 0 {
		rec.TTFTMS = rec.PromptMS
	}
}

func (r *Record) tokenAt(t time.Time) {
	r.times = append(r.times, t)
	if len(r.times) > 4096 {
		r.times = r.times[len(r.times)-4096:]
	}
}

var _ = sort.Ints

func (p *Proxy) startSampler(rec *Record) func() {
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sample := func() {
			m := hardware.Sample(p.gpu)
			now := time.Now()
			rec.mu.Lock()
			if len(rec.GPU) < 2000 {
				rec.GPU = append(rec.GPU, m)
				rec.GPUTimes = append(rec.GPUTimes, now)
			}
			rec.mu.Unlock()
		}
		sample()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
}

func (p *Proxy) finish(rec *Record, status string, err error) {
	rec.mu.Lock()
	rec.Ended = time.Now()
	rec.Status = status
	if err != nil {
		rec.Err = err.Error()
	}
	rec.mu.Unlock()
	rec.finalize()
	p.mu.Lock()
	delete(p.active, rec.ID)
	p.records = append(p.records, rec)
	if len(p.records) > 200 {
		p.records = p.records[len(p.records)-200:]
	}
	p.mu.Unlock()
}

func (r *Record) finalize() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.PromptTokens == 0 && r.ProcessedPrompt > 0 {
		r.PromptTokens = r.ProcessedPrompt + r.CachedTokens
	}
	if r.ProcessedPrompt == 0 && r.PromptTokens > r.CachedTokens {
		r.ProcessedPrompt = r.PromptTokens - r.CachedTokens
	}
	if r.CompletionTokens == 0 && len(r.times) > 1 {
		r.CompletionTokens = len(r.times)
	}
	if r.GenerationMS == 0 && len(r.times) > 1 {
		r.GenerationMS = float64(r.times[len(r.times)-1].Sub(r.times[0])) / float64(time.Millisecond)
	}
	if r.GenerationTPS == 0 && r.GenerationMS > 0 && r.CompletionTokens > 0 {
		r.GenerationTPS = float64(r.CompletionTokens) / (r.GenerationMS / 1000)
	}
	if r.PromptTPS == 0 && r.PromptMS > 0 && r.ProcessedPrompt > 0 {
		r.PromptTPS = float64(r.ProcessedPrompt) / (r.PromptMS / 1000)
	}
	if r.TTFTMS == 0 && r.PromptMS > 0 {
		r.TTFTMS = r.PromptMS
	}
	if len(r.times) > 1 {
		deltas := make([]float64, 0, len(r.times)-1)
		for i := 1; i < len(r.times); i++ {
			deltas = append(deltas, float64(r.times[i].Sub(r.times[i-1]))/float64(time.Millisecond))
		}
		sort.Float64s(deltas)
		r.InterP50MS = percentile(deltas, 0.50)
		r.InterP95MS = percentile(deltas, 0.95)
	}
	if r.PromptTokens > 0 || r.CompletionTokens > 0 {
		r.ContextUsed = r.PromptTokens + r.CompletionTokens
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func applyUsage(rec *Record, u *usage) {
	if u.PromptTokens > rec.PromptTokens {
		rec.PromptTokens = u.PromptTokens
	}
	if u.CompletionTokens > rec.CompletionTokens {
		rec.CompletionTokens = u.CompletionTokens
	}
	if u.PromptDetails != nil && u.PromptDetails.CachedTokens > rec.CachedTokens {
		rec.CachedTokens = u.PromptDetails.CachedTokens
	}
}

func applyTimings(rec *Record, t *timings) {
	if t.CacheN > rec.CachedTokens {
		rec.CachedTokens = t.CacheN
	}
	if t.PromptN > 0 {
		rec.ProcessedPrompt = t.PromptN
		if total := t.PromptN + rec.CachedTokens; total > rec.PromptTokens {
			rec.PromptTokens = total
		}
	}
	if t.PromptMS > 0 {
		rec.PromptMS = t.PromptMS
		rec.PromptTPS = t.PromptPerSecond
	}
	if t.PredictedN > 0 {
		rec.CompletionTokens = maxInt(rec.CompletionTokens, t.PredictedN)
	}
	if t.PredictedMS > 0 {
		rec.GenerationMS = t.PredictedMS
		rec.GenerationTPS = t.PredictedPerSecond
	}
}

func settingsOf(req chatRequest) Settings {
	return Settings{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		TopK:            req.TopK,
		MinP:            req.MinP,
		RepeatPenalty:   req.RepeatPenalty,
		Seed:            req.Seed,
		MaxTokens:       req.MaxTokens,
		Stream:          req.Stream,
		ReasoningEffort: req.ReasoningEffort,
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
			"te", "trailer", "transfer-encoding", "upgrade", "content-length":
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
