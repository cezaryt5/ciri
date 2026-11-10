package inference

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyRecordsNonStreamingTimings(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120},
			"timings":{"cache_n":40,"prompt_n":60,"prompt_ms":100,"prompt_per_second":600,
				"predicted_n":20,"predicted_ms":500,"predicted_per_second":40}
		}`)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	body := `{"model":"test","temperature":0.7,"max_tokens":32,"stream":false}`
	resp, err := http.Post(proxy.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("response was not proxied: %s", data)
	}

	records := proxy.RecordsSince(time.Time{})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.PromptTokens != 100 || rec.CachedTokens != 40 || rec.ProcessedPrompt != 60 {
		t.Fatalf("prompt accounting: %+v", rec)
	}
	if rec.CompletionTokens != 20 || rec.PromptTPS != 600 || rec.GenerationTPS != 40 {
		t.Fatalf("throughput accounting: %+v", rec)
	}
	if rec.FinishReason != "stop" || rec.Status != "completed" {
		t.Fatalf("status accounting: %+v", rec)
	}
	if rec.Settings.Temperature == nil || *rec.Settings.Temperature != 0.7 {
		t.Fatalf("settings not captured: %+v", rec.Settings)
	}
}

func TestProxyRecordsStreamingChunks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{
			`{"choices":[{"delta":{"content":"Hel"}}],"tokens":[1,2]}`,
			`{"choices":[{"delta":{"content":"lo"}}],"tokens":[3]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":3},"timings":{"prompt_n":10,"cache_n":40,"prompt_ms":20,"prompt_per_second":500,"predicted_n":3,"predicted_ms":100,"predicted_per_second":30}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	body := `{"model":"test","max_tokens":32,"stream":true}`
	resp, err := http.Post(proxy.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	records := proxy.RecordsSince(time.Time{})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.PromptTokens != 50 || rec.CachedTokens != 40 || rec.CompletionTokens != 3 {
		t.Fatalf("token accounting: %+v", rec)
	}
	if rec.TTFTMS <= 0 {
		t.Fatalf("TTFT was not recorded: %+v", rec)
	}
	if rec.Status != "completed" {
		t.Fatalf("status = %q", rec.Status)
	}
}

func TestProxyRecordsUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	resp, err := http.Post(proxy.URL()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"test","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	records := proxy.RecordsSince(time.Time{})
	if len(records) != 1 || records[0].Status != "error" {
		t.Fatalf("failure not recorded: %+v", records)
	}
}

func TestProxyExposesActiveRequest(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}],\"timings\":{\"prompt_n\":10,\"prompt_ms\":10,\"prompt_per_second\":1000,\"predicted_n\":1,\"predicted_ms\":10,\"predicted_per_second\":100}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	done := make(chan struct{})
	go func() {
		resp, err := http.Post(proxy.URL()+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"test","stream":true}`))
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		close(done)
	}()

	var snap []Snapshot
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap = proxy.ActiveSince(time.Time{})
		if len(snap) > 0 && snap[0].CompletionTokens > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(snap) != 1 {
		close(release)
		<-done
		t.Fatalf("active snapshots = %d", len(snap))
	}
	if snap[0].CompletionTokens < 1 {
		t.Fatalf("live snapshot not updating: %+v", snap[0])
	}
	close(release)
	<-done
	if got := proxy.ActiveSince(time.Time{}); len(got) != 0 {
		t.Fatalf("active request not cleared: %+v", got)
	}
	if records := proxy.RecordsSince(time.Time{}); len(records) != 1 {
		t.Fatalf("completed records = %d", len(records))
	}
}

func TestSettingsPointersSurviveJSON(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"temperature":0,"top_p":0.95,"top_k":20}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Temperature == nil || *s.Temperature != 0 {
		t.Fatalf("zero temperature must remain distinguishable: %+v", s)
	}
	if s.TopP == nil || *s.TopP != 0.95 || s.TopK == nil || *s.TopK != 20 {
		t.Fatalf("settings decode: %+v", s)
	}
}
