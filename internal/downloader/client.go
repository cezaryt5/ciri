// Package downloader implements a focused, aria2-like HTTP downloader for
// model weights: multi-connection ranged transfers, resumable state, retries
// and optional SHA-256 verification. It intentionally supports only the
// subset of aria2 needed for Hugging Face model files.
package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultEndpoint is the Hugging Face hub.
const DefaultEndpoint = "https://huggingface.co"

// DefaultIdleTimeout is how long a request may go without receiving a byte
// before it is treated as stalled. It covers both the wait for response
// headers and mid-body stalls.
const DefaultIdleTimeout = 90 * time.Second

// ErrIdleTimeout means the server stopped sending data for longer than the
// client's idle timeout. Partial downloads are resumable.
var ErrIdleTimeout = errors.New("connection stalled: no data received within idle timeout")

// RepoFile is one downloadable file inside a Hugging Face repository.
type RepoFile struct {
	Path   string
	Size   int64
	SHA256 string // set for LFS objects (large model weights)
}

// Client talks to the Hugging Face hub. Endpoint is configurable so tests
// can point at an httptest server.
type Client struct {
	Endpoint    string
	Token       string
	UserAgent   string
	HTTP        *http.Client
	IdleTimeout time.Duration
}

// NewClient returns a client configured from the environment. HF_TOKEN or
// HUGGING_FACE_HUB_TOKEN is used for gated/private repositories.
func NewClient() *Client {
	token := os.Getenv("HF_TOKEN")
	if token == "" {
		token = os.Getenv("HUGGING_FACE_HUB_TOKEN")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	transport.ResponseHeaderTimeout = 30 * time.Second

	return &Client{
		Endpoint:    DefaultEndpoint,
		Token:       token,
		UserAgent:   "ciri",
		HTTP:        &http.Client{Transport: transport},
		IdleTimeout: DefaultIdleTimeout,
	}
}

func (c *Client) base() string {
	if c.Endpoint == "" {
		return DefaultEndpoint
	}
	return strings.TrimRight(c.Endpoint, "/")
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) setHeaders(req *http.Request) {
	ua := c.UserAgent
	if ua == "" {
		ua = "ciri"
	}
	req.Header.Set("User-Agent", ua)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) idleTimeout() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return DefaultIdleTimeout
}

// newRequest creates a request whose context is canceled if no body bytes
// arrive for the client's idle timeout. The returned timer must be stopped
// once the response body is closed.
func (c *Client) newRequest(ctx context.Context, method, rawURL string) (*http.Request, *idleTimer, error) {
	ictx, it := newIdleTimer(ctx, c.idleTimeout())
	req, err := http.NewRequestWithContext(ictx, method, rawURL, nil)
	if err != nil {
		it.stop()
		return nil, nil, err
	}
	return req, it, nil
}

type idleTimer struct {
	timer  *time.Timer
	cancel context.CancelFunc
	period time.Duration
	fired  atomic.Bool
}

func newIdleTimer(parent context.Context, period time.Duration) (context.Context, *idleTimer) {
	ctx, cancel := context.WithCancel(parent)
	it := &idleTimer{cancel: cancel, period: period}
	if period > 0 {
		it.timer = time.AfterFunc(period, func() {
			it.fired.Store(true)
			cancel()
		})
	}
	return ctx, it
}

func (it *idleTimer) touch() {
	if it != nil && it.timer != nil && !it.fired.Load() {
		it.timer.Reset(it.period)
	}
}

func (it *idleTimer) stop() {
	if it == nil {
		return
	}
	if it.timer != nil {
		it.timer.Stop()
	}
	it.cancel()
}

type idleReader struct {
	r  io.Reader
	it *idleTimer
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 && r.it != nil {
		r.it.touch()
	}
	return n, err
}

// sameOrigin reports whether raw is on the same scheme and host as the
// configured endpoint. Pagination links are followed only when this holds.
func (c *Client) sameOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	base, err := url.Parse(c.base())
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host)
}

// ListRepoFiles lists files in a Hugging Face repository revision. It follows
// the hub's Link pagination so large repositories are fully covered.
func (c *Client) ListRepoFiles(ctx context.Context, repo, revision string) ([]RepoFile, error) {
	if revision == "" {
		revision = "main"
	}
	if strings.TrimSpace(repo) == "" {
		return nil, errors.New("empty repository")
	}

	next, err := url.JoinPath(c.base(), "api", "models", repo, "tree", revision)
	if err != nil {
		return nil, err
	}
	next += "?recursive=true&limit=1000"

	var files []RepoFile
	seen := 0
	for next != "" && seen < 100 {
		seen++

		req, it, err := c.newRequest(ctx, http.MethodGet, next)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		c.setHeaders(req)

		resp, err := c.client().Do(req)
		if err != nil {
			it.stop()
			if it.fired.Load() {
				return nil, fmt.Errorf("list %s: %w", repo, ErrIdleTimeout)
			}
			return nil, err
		}
		it.touch()
		body := &idleReader{r: resp.Body, it: it}
		if resp.StatusCode != http.StatusOK {
			msg := readErrorBody(body)
			resp.Body.Close()
			it.stop()
			if it.fired.Load() {
				return nil, fmt.Errorf("list %s: %w", repo, ErrIdleTimeout)
			}
			return nil, fmt.Errorf("list %s: %s", repo, statusError(resp.StatusCode, msg))
		}

		var entries []treeEntry
		decErr := json.NewDecoder(io.LimitReader(body, 32<<20)).Decode(&entries)
		next = nextLink(resp.Header)
		resp.Body.Close()
		it.stop()
		if decErr != nil {
			if it.fired.Load() {
				return nil, fmt.Errorf("list %s: %w", repo, ErrIdleTimeout)
			}
			return nil, fmt.Errorf("list %s: %w", repo, decErr)
		}
		if next != "" {
			u, err := resp.Request.URL.Parse(next)
			if err != nil {
				return nil, fmt.Errorf("list %s: bad pagination link %q: %w", repo, next, err)
			}
			if !c.sameOrigin(u.String()) {
				return nil, fmt.Errorf("list %s: refusing cross-host pagination link %q", repo, u)
			}
			next = u.String()
		}

		for _, e := range entries {
			if e.Type != "file" {
				continue
			}
			size := e.Size
			sha := ""
			if e.LFS != nil {
				if e.LFS.Size > 0 {
					size = e.LFS.Size
				}
				if isSHA256(e.LFS.OID) {
					sha = strings.ToLower(e.LFS.OID)
				}
			}
			files = append(files, RepoFile{Path: e.Path, Size: size, SHA256: sha})
		}
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("repository %s has no downloadable files", repo)
	}
	return files, nil
}

type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	OID  string `json:"oid"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// resolveURL builds the hub resolve URL for one file. url.JoinPath keeps
// repository slashes but escapes spaces and other unsafe characters.
func (c *Client) resolveURL(repo, revision, filePath string) (string, error) {
	if revision == "" {
		revision = "main"
	}
	u, err := url.JoinPath(c.base(), repo, "resolve", revision, filePath)
	if err != nil {
		return "", err
	}
	return u, nil
}

// nextLink extracts the rel="next" target from an RFC 8288 Link header.
func nextLink(h http.Header) string {
	for _, header := range h.Values("Link") {
		for _, part := range strings.Split(header, ",") {
			part = strings.TrimSpace(part)
			open := strings.Index(part, "<")
			close := strings.Index(part, ">")
			if open != 0 || close <= open {
				continue
			}
			rest := part[close+1:]
			if strings.Contains(rest, `rel="next"`) || strings.Contains(rest, "rel=next") {
				return part[open+1 : close]
			}
		}
	}
	return ""
}

func readErrorBody(r io.Reader) string {
	body, _ := io.ReadAll(io.LimitReader(r, 4096))
	return strings.TrimSpace(string(body))
}

func statusError(code int, body string) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("access denied (HTTP %d) — this repo may be gated; set HF_TOKEN", code)
	case http.StatusNotFound:
		return fmt.Errorf("not found (HTTP %d)", code)
	case http.StatusTooManyRequests:
		return fmt.Errorf("rate limited by the hub (HTTP 429)")
	}
	if body != "" {
		return fmt.Errorf("HTTP %d: %s", code, body)
	}
	return fmt.Errorf("HTTP %d", code)
}

// safeRelPath turns an untrusted remote file path into a relative local path.
// Subdirectories are preserved so quant directories cannot collide. Absolute
// paths, traversal segments, backslashes and drive separators are rejected
// rather than guessed at (the last two matter on Windows).
func safeRelPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || strings.ContainsAny(p, `\:`) {
		return "", fmt.Errorf("invalid remote file path %q", p)
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid remote file path %q: absolute path", p)
	}
	var parts []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("invalid remote file path %q: traversal segment", p)
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid remote file path %q", p)
	}
	rel := filepath.Join(parts...)
	if filepath.IsAbs(rel) || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid remote file path %q", p)
	}
	return rel, nil
}

// ModelsRoot returns the root directory where downloaded models are stored.
// It honors CIRI_MODELS_DIR when set and defaults to ~/.ciri/models.
func ModelsRoot() string {
	if env := strings.TrimSpace(os.Getenv("CIRI_MODELS_DIR")); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ciri", "models")
	}
	return "models"
}

// RepoDestDir returns the local directory for a repository (e.g.
// ~/.ciri/models/Qwen/Qwen2.5-7B-Instruct-GGUF), sanitizing path segments.
func RepoDestDir(repo string) string {
	root := ModelsRoot()
	repo = strings.ReplaceAll(strings.TrimSpace(repo), "\\", "/")
	var parts []string
	for _, seg := range strings.Split(repo, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		seg = strings.Map(func(r rune) rune {
			if r == ':' || r == 0 {
				return '_'
			}
			return r
		}, seg)
		if seg == "" {
			continue
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return root
	}
	all := append([]string{root}, parts...)
	return filepath.Join(all...)
}
