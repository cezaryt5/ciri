package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rangeServer serves payload at /resolve/main/<file> with Range support.
func rangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		rng := r.Header.Get("Range")
		if rng == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			w.Write(payload)
			return
		}
		var start, end int
		spec := strings.TrimPrefix(rng, "bytes=")
		parts := strings.SplitN(spec, "-", 2)
		start, _ = strconv.Atoi(parts[0])
		if len(parts) == 2 && parts[1] != "" {
			end, _ = strconv.Atoi(parts[1])
		} else {
			end = len(payload) - 1
		}
		if end >= len(payload) {
			end = len(payload) - 1
		}
		chunk := payload[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(chunk)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newTestClient(srv *httptest.Server) *Client {
	c := NewClient()
	c.Endpoint = srv.URL
	return c
}

func TestDownload_ParallelRangesAndChecksum(t *testing.T) {
	payload := testPayload(3*1024*1024 + 123)
	srv := rangeServer(t, payload)
	dir := t.TempDir()

	res, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     dir,
		SHA256:      sha(payload),
		Connections: 4,
		ChunkSize:   1 << 20,
	}, nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !res.ChecksumVerified {
		t.Error("expected checksum verification")
	}
	got, err := os.ReadFile(filepath.Join(dir, "model.gguf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes", len(got))
	}
	if _, err := os.Stat(filepath.Join(dir, "model.gguf.part")); !os.IsNotExist(err) {
		t.Error("part file should be renamed away")
	}
}

func TestDownload_ChecksumMismatchRemovesState(t *testing.T) {
	payload := testPayload(64 * 1024)
	srv := rangeServer(t, payload)
	dir := t.TempDir()

	_, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:      "owner/repo",
		Path:      "model.gguf",
		DestDir:   dir,
		SHA256:    strings.Repeat("0", 64),
		ChunkSize: 16 * 1024,
	}, nil)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "model.gguf.part.ciri.json")); !os.IsNotExist(statErr) {
		t.Error("state file should be removed after checksum failure")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "model.gguf.part")); !os.IsNotExist(statErr) {
		t.Error("part file should be removed after checksum failure")
	}
}

func TestDownload_ResumesFromState(t *testing.T) {
	payload := testPayload(256 * 1024)
	srv := rangeServer(t, payload)
	dir := t.TempDir()
	c := newTestClient(srv)
	rawURL, err := c.resolveURL("owner/repo", "main", "model.gguf")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an interrupted run: first chunk written, second chunk missing.
	chunk := int64(64 * 1024)
	part := filepath.Join(dir, "model.gguf.part")
	if err := os.WriteFile(part, make([]byte, len(payload)), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(part, os.O_RDWR, 0o644)
	f.WriteAt(payload[:chunk], 0)
	f.Close()

	st := &resumeState{
		URL:       rawURL,
		Size:      int64(len(payload)),
		ChunkSize: chunk,
		Done:      []bool{true, false, false, false},
	}
	store := &stateStore{path: part + ".ciri.json", state: st}
	store.save()

	res, err := c.Download(context.Background(), Request{
		Repo:      "owner/repo",
		Path:      "model.gguf",
		DestDir:   dir,
		SHA256:    sha(payload),
		ChunkSize: chunk,
	}, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !res.Resumed {
		t.Error("expected resumed download")
	}
	got, _ := os.ReadFile(res.Dest)
	if !bytes.Equal(got, payload) {
		t.Fatal("resumed payload mismatch")
	}
}

func TestDownload_AlreadyDownloaded(t *testing.T) {
	payload := testPayload(10 * 1024)
	srv := rangeServer(t, payload)
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.gguf")
	os.WriteFile(dest, payload, 0o644)

	res, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:    "owner/repo",
		Path:    "model.gguf",
		DestDir: dir,
	}, nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !res.AlreadyExisted {
		t.Error("expected AlreadyExisted")
	}
}

func TestSafeRelPath(t *testing.T) {
	if _, err := safeRelPath("../.."); err == nil {
		t.Error("expected rejection of traversal-only path")
	}
	if _, err := safeRelPath("a/../../b"); err == nil {
		t.Error("expected rejection of nested traversal")
	}
	if _, err := safeRelPath(`..\..\evil`); err == nil {
		t.Error("expected rejection of backslash traversal")
	}
	if _, err := safeRelPath("/etc/passwd"); err == nil {
		t.Error("expected rejection of absolute path")
	}
	if got, err := safeRelPath("sub/dir/model.gguf"); err != nil || got != filepath.Join("sub", "dir", "model.gguf") {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := safeRelPath("a/./b//model.gguf"); err != nil || got != filepath.Join("a", "b", "model.gguf") {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestNextLink(t *testing.T) {
	h := http.Header{}
	h.Add("Link", `<https://x/y?cursor=2>; rel="next", <https://x/y?cursor=0>; rel="prev"`)
	if got := nextLink(h); got != "https://x/y?cursor=2" {
		t.Errorf("nextLink = %q", got)
	}
}

func TestChooseChunkSize_Bounds(t *testing.T) {
	if got := chooseChunkSize(10, 4, 0); got != 10 {
		t.Errorf("tiny file chunk = %d, want clamped to total", got)
	}
	if got := chooseChunkSize(1<<30, 4, 0); got != DefaultChunkSize {
		t.Errorf("large file chunk = %d, want default", got)
	}
}

func TestDownload_ContentRangeMismatch(t *testing.T) {
	payload := testPayload(64 * 1024)
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/repo/resolve/main/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		// Lie about the returned byte offset.
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 16-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[16:])
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	_, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     dir,
		Connections: 1,
		ChunkSize:   int64(len(payload)),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unexpected Content-Range") {
		t.Fatalf("expected unexpected Content-Range error, got %v", err)
	}
}

func TestDownload_RangeUnsupportedFallsBack(t *testing.T) {
	payload := testPayload(32 * 1024)
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/repo/resolve/main/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		// Ignore Range header completely and always return 200 OK.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	res, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:    "owner/repo",
		Path:    "model.gguf",
		DestDir: dir,
		SHA256:  sha(payload),
	}, nil)
	if err != nil {
		t.Fatalf("download fallback: %v", err)
	}
	got, err := os.ReadFile(res.Dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("fallback payload mismatch")
	}
}

func TestDownload_CancelKeepsPartialAndState(t *testing.T) {
	payload := testPayload(128 * 1024)
	ctx, cancel := context.WithCancel(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/owner/repo/resolve/main/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		if strings.HasPrefix(rng, "bytes=0-") {
			chunk := payload[:64*1024]
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(chunk)-1, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(chunk)
			return
		}
		// Cancel during the second chunk.
		cancel()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	_, err := newTestClient(srv).Download(ctx, Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     dir,
		Connections: 1,
		ChunkSize:   64 * 1024,
	}, nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "model.gguf.part")); statErr != nil {
		t.Errorf("expected .part file preserved on cancel: %v", statErr)
	}
	st, loadErr := loadState(filepath.Join(dir, "model.gguf.part.ciri.json"))
	if loadErr != nil {
		t.Fatalf("expected state file preserved on cancel: %v", loadErr)
	}
	if len(st.Done) != 2 || !st.Done[0] || st.Done[1] {
		t.Errorf("unexpected chunk state after cancel: %+v", st.Done)
	}
}

func TestClient_NoAuthHeaderOnCrossDomainRedirect(t *testing.T) {
	payload := testPayload(4096)
	var cdnAuth string

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload)
	}))
	t.Cleanup(cdn.Close)

	var hubAuth string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, cdn.URL+"/signed-weights", http.StatusFound)
	}))
	t.Cleanup(hub.Close)

	// Force a different hostname between hub (127.0.0.1) and cdn (localhost)
	// so net/http treats the redirect as cross-domain.
	cdnURL := strings.Replace(cdn.URL, "127.0.0.1", "localhost", 1)
	hubFixed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, cdnURL+"/signed-weights", http.StatusFound)
	}))
	t.Cleanup(hubFixed.Close)

	c := NewClient()
	c.Endpoint = hubFixed.URL
	c.Token = "hf_secret_token"

	dir := t.TempDir()
	_, err := c.Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     dir,
		Connections: 1,
		ChunkSize:   int64(len(payload)),
	}, nil)
	if err != nil {
		t.Fatalf("download via redirect: %v", err)
	}
	if hubAuth != "Bearer hf_secret_token" {
		t.Errorf("hub did not receive token: %q", hubAuth)
	}
	if cdnAuth != "" {
		t.Errorf("CDN must not receive Authorization header across domains, got %q", cdnAuth)
	}
}

func TestRepoDestDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "models")
	t.Setenv("CIRI_MODELS_DIR", root)

	cases := map[string]string{
		"Qwen/Qwen2.5-7B-Instruct-GGUF": filepath.Join(root, "Qwen", "Qwen2.5-7B-Instruct-GGUF"),
		"../../etc":                     filepath.Join(root, "etc"),
		`..\..\evil/repo`:               filepath.Join(root, "evil", "repo"),
		"owner/re:po":                   filepath.Join(root, "owner", "re_po"),
	}
	for repo, want := range cases {
		if got := RepoDestDir(repo); got != want {
			t.Errorf("RepoDestDir(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestClient_ListRepoFiles(t *testing.T) {
	shaHex := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/owner/repo/tree/main" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[
			{"type":"directory","path":"subdir"},
			{"type":"file","path":"README.md","size":120},
			{"type":"file","path":"model-q4_k_m.gguf","size":10,"lfs":{"oid":%q,"size":4096000}}
		]`, shaHex)
	}))
	t.Cleanup(srv.Close)

	files, err := newTestClient(srv).ListRepoFiles(context.Background(), "owner/repo", "main")
	if err != nil {
		t.Fatalf("ListRepoFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[1].Path != "model-q4_k_m.gguf" || files[1].Size != 4096000 || files[1].SHA256 != shaHex {
		t.Errorf("unexpected LFS file entry: %+v", files[1])
	}
}

func TestDownload_CancelDuringVerifyKeepsPartial(t *testing.T) {
	old := progressInterval
	progressInterval = time.Millisecond
	t.Cleanup(func() { progressInterval = old })

	payload := testPayload(64 << 20)
	srv := rangeServer(t, payload)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once

	_, err := newTestClient(srv).Download(ctx, Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     dir,
		SHA256:      sha(payload),
		Connections: 1,
		ChunkSize:   32 << 20,
	}, func(p Progress) {
		if p.Phase == "verifying" {
			once.Do(cancel)
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	part := filepath.Join(dir, "model.gguf.part")
	if _, err := os.Stat(part); err != nil {
		t.Errorf("part should survive cancel during verify: %v", err)
	}
	if _, err := os.Stat(part + ".ciri.json"); err != nil {
		t.Errorf("state should survive cancel during verify: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "model.gguf")); !os.IsNotExist(err) {
		t.Error("final file must not exist after canceled verification")
	}
}

func TestDownload_OverlongBodyDoesNotCorrupt(t *testing.T) {
	payload := testPayload(128 << 10)
	const chunk = 64 << 10
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/repo/resolve/main/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		spec := strings.TrimPrefix(rng, "bytes=")
		parts := strings.SplitN(spec, "-", 2)
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		// Deliberately advertise and send more bytes than the requested range.
		w.Header().Set("Content-Length", strconv.Itoa(chunk*2))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[start : end+1])
		w.Write(bytes.Repeat([]byte{0xAA}, chunk))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	c := newTestClient(srv)
	rawURL, err := c.resolveURL("owner/repo", "main", "model.gguf")
	if err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(dir, "model.gguf.part")
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(payload[chunk:], int64(chunk)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	st := &resumeState{
		URL:       rawURL,
		Size:      int64(len(payload)),
		ChunkSize: chunk,
		Done:      []bool{false, true},
	}
	(&stateStore{path: part + ".ciri.json", state: st}).save()

	res, err := c.Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     dir,
		SHA256:      sha(payload),
		Connections: 1,
		ChunkSize:   chunk,
	}, nil)
	if err != nil {
		t.Fatalf("download with overlong range body: %v", err)
	}
	got, err := os.ReadFile(res.Dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("overlong range body corrupted an adjacent chunk")
	}
}

func TestDownload_ContentRangeTotalChangeRejected(t *testing.T) {
	payload := testPayload(64 << 10)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)+1))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     t.TempDir(),
		Connections: 1,
		ChunkSize:   int64(len(payload)),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "file changed") {
		t.Fatalf("expected Content-Range total change error, got %v", err)
	}
}

func TestDownload_MissingContentRangeRejected(t *testing.T) {
	payload := testPayload(64 << 10)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     t.TempDir(),
		Connections: 1,
		ChunkSize:   int64(len(payload)),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing Content-Range") {
		t.Fatalf("expected missing Content-Range error, got %v", err)
	}
}

func TestDownload_IdleTimeoutRetries(t *testing.T) {
	payload := testPayload(64 << 10)
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
			return
		}
		if attempts.Add(1) == 1 {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newTestClient(srv)
	c.IdleTimeout = 50 * time.Millisecond
	res, err := c.Download(context.Background(), Request{
		Repo:        "owner/repo",
		Path:        "model.gguf",
		DestDir:     t.TempDir(),
		SHA256:      sha(payload),
		Connections: 1,
		ChunkSize:   int64(len(payload)),
	}, nil)
	if err != nil {
		t.Fatalf("download after idle timeout: %v", err)
	}
	if attempts.Load() < 2 {
		t.Fatalf("expected a retry after the stall, got %d attempts", attempts.Load())
	}
	got, err := os.ReadFile(res.Dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch after idle retry")
	}
}

func TestDownload_ExistingWrongFileRejected(t *testing.T) {
	payload := testPayload(64 << 10)
	dir := t.TempDir()
	wrong := testPayload(len(payload))
	wrong[0] ^= 0xff
	dest := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(dest, wrong, 0o644); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	_, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:     "owner/repo",
		Path:     "model.gguf",
		DestDir:  dir,
		SizeHint: int64(len(payload)),
		SHA256:   sha(payload),
	}, nil)
	if !errors.Is(err, ErrLocalFileMismatch) {
		t.Fatalf("expected local file mismatch, got %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("local checksum mismatch should not hit the network, got %d requests", hits.Load())
	}
	if _, statErr := os.Stat(dest); statErr != nil {
		t.Errorf("existing file should not be deleted: %v", statErr)
	}
}

func TestDownload_AlreadyDownloadedOffline(t *testing.T) {
	payload := testPayload(64 << 10)
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(dest, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	res, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:     "owner/repo",
		Path:     "model.gguf",
		DestDir:  dir,
		SizeHint: int64(len(payload)),
		SHA256:   sha(payload),
	}, nil)
	if err != nil {
		t.Fatalf("already downloaded: %v", err)
	}
	if !res.AlreadyExisted || !res.ChecksumVerified {
		t.Fatalf("unexpected result: %+v", res)
	}
	if hits.Load() != 0 {
		t.Errorf("complete file should be recognized offline, got %d requests", hits.Load())
	}
}

func TestClient_ListRepoFilesRejectsCrossHostLink(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		w.Write([]byte("[]"))
	}))
	t.Cleanup(other.Close)

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", fmt.Sprintf(`<%s/next>; rel="next"`, other.URL))
		fmt.Fprint(w, `[{"type":"file","path":"model.gguf","size":10}]`)
	}))
	t.Cleanup(first.Close)

	_, err := newTestClient(first).ListRepoFiles(context.Background(), "owner/repo", "main")
	if err == nil || !strings.Contains(err.Error(), "cross-host") {
		t.Fatalf("expected cross-host pagination error, got %v", err)
	}
	if otherHits.Load() != 0 {
		t.Errorf("cross-host pagination target must not be requested, got %d hits", otherHits.Load())
	}
}

func TestClient_ListRepoFilesSameHostPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "2" {
			fmt.Fprint(w, `[{"type":"file","path":"b.gguf","size":20}]`)
			return
		}
		w.Header().Set("Link", `</api/models/owner/repo/tree/main?cursor=2>; rel="next"`)
		fmt.Fprint(w, `[{"type":"file","path":"a.gguf","size":10}]`)
	}))
	t.Cleanup(srv.Close)

	files, err := newTestClient(srv).ListRepoFiles(context.Background(), "owner/repo", "main")
	if err != nil {
		t.Fatalf("same-host pagination: %v", err)
	}
	if len(files) != 2 || files[0].Path != "a.gguf" || files[1].Path != "b.gguf" {
		t.Fatalf("unexpected paginated files: %+v", files)
	}
}

func TestDownload_PreservesRemoteSubdirectory(t *testing.T) {
	payload := testPayload(64 << 10)
	srv := rangeServer(t, payload)
	dir := t.TempDir()

	res, err := newTestClient(srv).Download(context.Background(), Request{
		Repo:    "owner/repo",
		Path:    "Q4_K_M/model.gguf",
		DestDir: dir,
		SHA256:  sha(payload),
	}, nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	want := filepath.Join(dir, "Q4_K_M", "model.gguf")
	if res.Dest != want {
		t.Fatalf("dest = %q, want %q", res.Dest, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch in subdirectory")
	}
}
