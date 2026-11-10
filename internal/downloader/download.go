package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultConnections is the number of parallel range requests. Four is a
	// polite default for shared model CDNs; the CLI/TUI may raise it.
	DefaultConnections = 4
	// DefaultChunkSize is the granularity of resumable work.
	DefaultChunkSize = 16 << 20

	maxAttempts = 4
)

// progressInterval is a variable so tests can make the ticker fire during a
// short checksum verification.
var progressInterval = 400 * time.Millisecond

var (
	ErrAlreadyDownloaded = errors.New("file already downloaded")
	ErrDestinationExists = errors.New("destination file already exists")
	ErrLocalFileMismatch = errors.New("existing file does not match expected checksum")
	ErrChecksumMismatch  = errors.New("sha256 checksum mismatch")
)

// Request describes one file download.
type Request struct {
	Repo     string
	Revision string
	Path     string
	DestDir  string
	SizeHint int64
	SHA256   string
	// Connections is the number of parallel range requests (default 4, max 8).
	Connections int
	// ChunkSize is the resumable unit (default 16 MiB).
	ChunkSize int64
}

// Progress is one snapshot of a download. Callbacks are invoked from a single
// goroutine, at most every progressInterval, plus once when the phase ends.
type Progress struct {
	Dest        string
	File        string
	Downloaded  int64
	Total       int64
	SpeedBps    float64
	ETA         time.Duration
	ChunksDone  int
	ChunksTotal int
	Connections int
	Phase       string // "downloading", "verifying" or "done"
	Verified    int64
}

// Result describes a completed download.
type Result struct {
	Dest             string
	Size             int64
	Resumed          bool
	AlreadyExisted   bool
	ChecksumVerified bool
}

type remoteMeta struct {
	size   int64
	etag   string
	ranges bool
}

// Download fetches one repository file into Request.DestDir. It resumes from
// a .part file when a compatible state file is present. onProgress may be nil.
func (c *Client) Download(ctx context.Context, req Request, onProgress func(Progress)) (Result, error) {
	if strings.TrimSpace(req.Repo) == "" {
		return Result{}, errors.New("empty repository")
	}
	if strings.TrimSpace(req.Path) == "" {
		return Result{}, errors.New("empty file path")
	}
	if req.DestDir == "" {
		return Result{}, errors.New("empty destination directory")
	}
	if req.Revision == "" {
		req.Revision = "main"
	}

	rel, err := safeRelPath(req.Path)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(req.DestDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create destination: %w", err)
	}

	dest := filepath.Join(req.DestDir, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Result{}, fmt.Errorf("create destination: %w", err)
	}
	part := dest + ".part"
	statePath := dest + ".part.ciri.json"

	// Local fast path: a known checksum lets us recognize a complete file
	// without any network round trip. Without one we need the probe's total.
	if req.SizeHint > 0 && req.SHA256 != "" {
		res, ok, err := c.existingFile(ctx, dest, req.SizeHint, req.SHA256)
		if err != nil {
			return Result{}, err
		}
		if ok {
			return res, nil
		}
	}

	rawURL, err := c.resolveURL(req.Repo, req.Revision, req.Path)
	if err != nil {
		return Result{}, err
	}

	meta, err := c.probe(ctx, rawURL)
	if err != nil {
		return Result{}, err
	}
	total := meta.size
	if total <= 0 {
		total = req.SizeHint
	}
	if total <= 0 {
		return c.downloadWhole(ctx, rawURL, req, dest, part, onProgress)
	}

	res, ok, err := c.existingFile(ctx, dest, total, req.SHA256)
	if err != nil {
		return Result{}, err
	}
	if ok {
		return res, nil
	}

	conns := req.Connections
	if conns <= 0 {
		conns = DefaultConnections
	}
	if conns > 8 {
		conns = 8
	}

	chunkSize := chooseChunkSize(total, conns, req.ChunkSize)
	chunks := chunkCount(total, chunkSize)

	var (
		file   *os.File
		st     *resumeState
		resume bool
	)
	if saved, loadErr := loadState(statePath); loadErr == nil && saved.matches(rawURL, total, meta.etag) {
		savedChunks := chunkCount(total, saved.ChunkSize)
		if len(saved.Done) == savedChunks {
			if fi, statErr := os.Stat(part); statErr == nil && fi.Size() == total {
				file, err = os.OpenFile(part, os.O_RDWR, 0o644)
				if err == nil {
					chunkSize = saved.ChunkSize
					chunks = savedChunks
					st = saved
					resume = true
				}
			}
		}
	}

	if !resume {
		file, err = os.OpenFile(part, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return Result{}, fmt.Errorf("open part file: %w", err)
		}
		if err := file.Truncate(total); err != nil {
			file.Close()
			return Result{}, fmt.Errorf("allocate part file: %w", err)
		}
		st = &resumeState{
			URL:       rawURL,
			Size:      total,
			ETag:      meta.etag,
			ChunkSize: chunkSize,
			Done:      make([]bool, chunks),
		}
	}
	defer func() {
		if file != nil {
			_ = file.Sync()
			_ = file.Close()
		}
	}()

	store := &stateStore{path: statePath, state: st}
	store.save()

	done := make([]atomic.Bool, chunks)
	var doneCount int64
	for i, d := range st.Done {
		if i < len(done) && d {
			done[i].Store(true)
			doneCount++
		}
	}

	rep := newProgressReporter(onProgress, part, dest, req.Path, total, chunks, conns)
	for i := 0; i < chunks; i++ {
		if done[i].Load() {
			rep.add(chunkLen(i, chunkSize, total))
		}
	}
	rep.chunkDone.Store(doneCount)
	rep.start()
	defer rep.stop()

	if !meta.ranges {
		// The hub ignored the range probe; fall back to one streamed GET.
		rep.reset()
		if err := c.streamInto(ctx, rawURL, file, total, rep); err != nil {
			return Result{}, err
		}
	} else {
		if err := c.fetchRanges(ctx, rawURL, file, total, chunkSize, chunks, done, store, rep); err != nil {
			return Result{}, err
		}
	}

	if err := file.Sync(); err != nil {
		return Result{}, err
	}

	checksummed := false
	if req.SHA256 != "" {
		rep.setPhase("verifying")
		if err := verifySHA256(ctx, part, req.SHA256, rep); err != nil {
			// Only a real mismatch invalidates the data. Cancellation or a
			// transient read error must keep the partial (I6).
			if errors.Is(err, ErrChecksumMismatch) {
				_ = file.Close()
				file = nil
				_ = os.Remove(part)
				store.remove()
			}
			return Result{}, err
		}
		checksummed = true
	}

	if err := file.Close(); err != nil {
		return Result{}, fmt.Errorf("close part file: %w", err)
	}
	file = nil
	if err := os.Rename(part, dest); err != nil {
		return Result{}, fmt.Errorf("finalize download: %w", err)
	}
	store.remove()

	rep.setPhase("done")
	return Result{
		Dest:             dest,
		Size:             total,
		Resumed:          resume,
		ChecksumVerified: checksummed,
	}, nil
}

func (c *Client) probe(ctx context.Context, rawURL string) (remoteMeta, error) {
	req, it, err := c.newRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return remoteMeta{}, err
	}
	req.Header.Set("Range", "bytes=0-0")
	c.setHeaders(req)

	resp, err := c.client().Do(req)
	if err != nil {
		it.stop()
		if it.fired.Load() {
			return remoteMeta{}, ErrIdleTimeout
		}
		return remoteMeta{}, err
	}
	it.touch()
	defer func() {
		resp.Body.Close()
		it.stop()
	}()
	if _, err := io.Copy(io.Discard, io.LimitReader(&idleReader{r: resp.Body, it: it}, 64<<10)); err != nil && it.fired.Load() {
		return remoteMeta{}, ErrIdleTimeout
	}

	meta := remoteMeta{
		etag: normalizeETag(firstHeader(resp.Header, "X-Linked-Etag", "ETag")),
	}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		meta.ranges = true
		meta.size = contentRangeTotal(resp.Header.Get("Content-Range"))
	case http.StatusOK:
		meta.ranges = false
		meta.size = resp.ContentLength
	default:
		return remoteMeta{}, statusError(resp.StatusCode, "")
	}
	if meta.size <= 0 {
		if n, err := strconv.ParseInt(resp.Header.Get("X-Linked-Size"), 10, 64); err == nil {
			meta.size = n
		}
	}
	if meta.size <= 0 {
		return remoteMeta{}, errors.New("server did not report a file size")
	}
	return meta, nil
}

func (c *Client) fetchRanges(ctx context.Context, rawURL string, file *os.File, total, chunkSize int64, chunks int, done []atomic.Bool, store *stateStore, rep *progressReporter) error {
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := rep.connections
	if workers > chunks {
		workers = chunks
	}
	if workers < 1 {
		workers = 1
	}

	var (
		next     atomic.Int64
		firstErr error
		errOnce  sync.Once
		wg       sync.WaitGroup
	)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= chunks {
					return
				}
				if done[i].Load() {
					continue
				}
				start, end := chunkBounds(i, chunkSize, total)
				if err := c.fetchChunk(fetchCtx, rawURL, file, start, end, total, rep); err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				if err := file.Sync(); err != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("sync chunk: %w", err)
						cancel()
					})
					return
				}
				if done[i].CompareAndSwap(false, true) {
					store.markDone(i)
					rep.chunkFinished()
				}
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (c *Client) fetchChunk(ctx context.Context, rawURL string, file *os.File, start, end, total int64, rep *progressReporter) error {
	var got int64
	for attempt := 0; attempt < maxAttempts; attempt++ {
		wantStart := start + got
		if wantStart >= end {
			return nil
		}

		req, it, err := c.newRequest(ctx, http.MethodGet, rawURL)
		if err != nil {
			return err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", wantStart, end-1))
		c.setHeaders(req)

		resp, err := c.client().Do(req)
		if err != nil {
			it.stop()
			if it.fired.Load() {
				err = ErrIdleTimeout
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < maxAttempts-1 {
				if !sleepOrDone(ctx, backoff(attempt)) {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		it.touch()

		if resp.StatusCode != http.StatusPartialContent {
			body := readErrorBody(&idleReader{r: resp.Body, it: it})
			resp.Body.Close()
			it.stop()
			if retryableStatus(resp.StatusCode) && attempt < maxAttempts-1 {
				if !sleepOrDone(ctx, retryDelay(resp, attempt)) {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("range %d-%d: %s", wantStart, end-1, statusError(resp.StatusCode, body))
		}

		cr := resp.Header.Get("Content-Range")
		if cr == "" {
			resp.Body.Close()
			it.stop()
			return fmt.Errorf("range %d-%d: missing Content-Range", wantStart, end-1)
		}
		crStart, crEnd, ok := contentRangeSpan(cr)
		if !ok || crStart != wantStart || crEnd != end-1 {
			resp.Body.Close()
			it.stop()
			return fmt.Errorf("range %d-%d: unexpected Content-Range %q", wantStart, end-1, cr)
		}
		if crTotal := contentRangeTotal(cr); crTotal != 0 && crTotal != total {
			resp.Body.Close()
			it.stop()
			return fmt.Errorf("range %d-%d: file changed (total %d, want %d)", wantStart, end-1, crTotal, total)
		}

		buf := make([]byte, 64<<10)
		body := io.LimitReader(&idleReader{r: resp.Body, it: it}, end-wantStart)
		var readErr error
		for {
			n, rerr := body.Read(buf)
			if n > 0 {
				if _, werr := file.WriteAt(buf[:n], start+got); werr != nil {
					resp.Body.Close()
					it.stop()
					return fmt.Errorf("write chunk: %w", werr)
				}
				got += int64(n)
				rep.add(int64(n))
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				readErr = rerr
				break
			}
			if err := ctx.Err(); err != nil {
				resp.Body.Close()
				it.stop()
				return err
			}
		}
		resp.Body.Close()
		it.stop()

		if readErr != nil {
			if it.fired.Load() {
				readErr = ErrIdleTimeout
			}
			if attempt < maxAttempts-1 {
				if !sleepOrDone(ctx, backoff(attempt)) {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("read range %d-%d: %w", wantStart, end-1, readErr)
		}
		if got != end-start {
			if attempt < maxAttempts-1 {
				if !sleepOrDone(ctx, backoff(attempt)) {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("short range: got %d of %d bytes", got, end-start)
		}
		return nil
	}
	return errors.New("range download failed after retries")
}

// streamInto is the fallback for servers that ignore Range headers. It cannot
// resume and does not use the state file.
func (c *Client) streamInto(ctx context.Context, rawURL string, file *os.File, total int64, rep *progressReporter) error {
	req, it, err := c.newRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.client().Do(req)
	if err != nil {
		it.stop()
		if it.fired.Load() {
			return ErrIdleTimeout
		}
		return err
	}
	it.touch()
	defer func() {
		resp.Body.Close()
		it.stop()
	}()
	body := &idleReader{r: resp.Body, it: it}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode, readErrorBody(body))
	}

	buf := make([]byte, 128<<10)
	var off int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if total > 0 && off >= total {
			return nil
		}
		limit := len(buf)
		if total > 0 && int64(limit) > total-off {
			limit = int(total - off)
		}
		n, rerr := body.Read(buf[:limit])
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return werr
			}
			off += int64(n)
			rep.add(int64(n))
		}
		if rerr == io.EOF {
			if total > 0 && off != total {
				return fmt.Errorf("short body: got %d of %d bytes", off, total)
			}
			return nil
		}
		if rerr != nil {
			if it.fired.Load() {
				return ErrIdleTimeout
			}
			return rerr
		}
	}
}

func (c *Client) downloadWhole(ctx context.Context, rawURL string, req Request, dest, part string, onProgress func(Progress)) (Result, error) {
	if st, err := os.Stat(dest); err == nil {
		if req.SizeHint > 0 && st.Size() == req.SizeHint {
			return Result{Dest: dest, Size: st.Size(), AlreadyExisted: true}, nil
		}
		return Result{}, fmt.Errorf("%w: %s", ErrDestinationExists, dest)
	}

	file, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

	rep := newProgressReporter(onProgress, part, dest, req.Path, req.SizeHint, 0, 1)
	rep.start()
	defer rep.stop()
	if err := c.streamInto(ctx, rawURL, file, req.SizeHint, rep); err != nil {
		return Result{}, err
	}
	if err := file.Sync(); err != nil {
		return Result{}, err
	}
	if req.SHA256 != "" {
		rep.setPhase("verifying")
		if err := verifySHA256(ctx, part, req.SHA256, rep); err != nil {
			if errors.Is(err, ErrChecksumMismatch) {
				_ = file.Close()
				file = nil
				_ = os.Remove(part)
			}
			return Result{}, err
		}
	}
	if err := file.Close(); err != nil {
		return Result{}, err
	}
	file = nil
	if err := os.Rename(part, dest); err != nil {
		return Result{}, err
	}
	rep.setPhase("done")

	size := req.SizeHint
	if fi, err := os.Stat(dest); err == nil {
		size = fi.Size()
	}
	return Result{Dest: dest, Size: size, ChecksumVerified: req.SHA256 != ""}, nil
}

// ---- progress ----

type progressReporter struct {
	cb          func(Progress)
	part        string
	dest        string
	file        string
	total       int64
	totalChunks int
	connections int

	downloaded atomic.Int64
	verified   atomic.Int64
	chunkDone  atomic.Int64

	phase   atomic.Value
	stopCh  chan struct{}
	stopOne sync.Once
	wg      sync.WaitGroup

	lastBytes int64
	lastTime  time.Time
	speed     float64
}

func newProgressReporter(cb func(Progress), part, dest, file string, total int64, chunks, connections int) *progressReporter {
	r := &progressReporter{
		cb:          cb,
		part:        part,
		dest:        dest,
		file:        file,
		total:       total,
		totalChunks: chunks,
		connections: connections,
		stopCh:      make(chan struct{}),
	}
	r.phase.Store("downloading")
	return r
}

func (r *progressReporter) start() {
	r.lastTime = time.Now()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stopCh:
				return
			case <-ticker.C:
				r.emit()
			}
		}
	}()
}

func (r *progressReporter) stopReporter() {
	r.stopOne.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

// stop stops the ticker and emits one final snapshot.
func (r *progressReporter) stop() {
	r.stopReporter()
	r.emit()
}

func (r *progressReporter) add(n int64) {
	r.downloaded.Add(n)
}

func (r *progressReporter) setVerified(n int64) {
	r.verified.Store(n)
}

func (r *progressReporter) reset() {
	r.downloaded.Store(0)
}

func (r *progressReporter) chunkFinished() {
	r.chunkDone.Add(1)
}

func (r *progressReporter) setPhase(p string) {
	r.phase.Store(p)
}

func (r *progressReporter) emit() {
	if r.cb == nil {
		return
	}
	now := time.Now()
	d := r.downloaded.Load()
	if d < 0 {
		d = 0
	}

	if elapsed := now.Sub(r.lastTime).Seconds(); elapsed > 0 {
		inst := float64(d-r.lastBytes) / elapsed
		if inst < 0 {
			inst = 0
		}
		if r.speed == 0 {
			r.speed = inst
		} else {
			r.speed = r.speed*0.7 + inst*0.3
		}
	}
	r.lastBytes = d
	r.lastTime = now

	phase, _ := r.phase.Load().(string)
	p := Progress{
		Dest:        r.dest,
		File:        r.file,
		Downloaded:  d,
		Total:       r.total,
		SpeedBps:    r.speed,
		ChunksDone:  int(r.chunkDone.Load()),
		ChunksTotal: r.totalChunks,
		Connections: r.connections,
		Phase:       phase,
		Verified:    r.verified.Load(),
	}
	if r.speed > 0 && r.total > d && phase == "downloading" {
		p.ETA = time.Duration(float64(r.total-d)/r.speed) * time.Second
	}
	r.cb(p)
}

// ---- resume state ----

type resumeState struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	ETag      string `json:"etag"`
	ChunkSize int64  `json:"chunk_size"`
	Done      []bool `json:"done"`
}

func (s *resumeState) matches(rawURL string, total int64, etag string) bool {
	if s == nil || s.ChunkSize <= 0 || s.Size != total || s.URL != rawURL {
		return false
	}
	if s.ETag != "" && etag != "" && !strings.EqualFold(s.ETag, etag) {
		return false
	}
	return true
}

type stateStore struct {
	mu    sync.Mutex
	path  string
	state *resumeState
}

func (s *stateStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

func (s *stateStore) saveLocked() {
	data, err := json.Marshal(s.state)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}

func (s *stateStore) markDone(i int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= 0 && i < len(s.state.Done) {
		s.state.Done[i] = true
	}
	s.saveLocked()
}

func (s *stateStore) remove() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.path)
}

func loadState(path string) (*resumeState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st resumeState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ---- helpers ----

func chooseChunkSize(total int64, connections int, requested int64) int64 {
	if connections <= 0 {
		connections = DefaultConnections
	}
	if requested > 0 {
		if total > 0 && requested > total {
			return total
		}
		return requested
	}
	chunkSize := int64(DefaultChunkSize)
	if total <= 0 {
		return chunkSize
	}
	if total < int64(connections)*chunkSize {
		chunkSize = total / int64(connections) / 2
	}
	const minChunk = int64(1 << 20)
	if chunkSize < minChunk {
		chunkSize = minChunk
	}
	if chunkSize > total {
		chunkSize = total
	}
	if chunkSize < 1 {
		chunkSize = 1
	}
	return chunkSize
}

func chunkCount(total, chunkSize int64) int {
	if chunkSize <= 0 || total <= 0 {
		return 0
	}
	return int((total + chunkSize - 1) / chunkSize)
}

func chunkLen(i int, chunkSize, total int64) int64 {
	start, end := chunkBounds(i, chunkSize, total)
	return end - start
}

func chunkBounds(i int, chunkSize, total int64) (start, end int64) {
	start = int64(i) * chunkSize
	end = start + chunkSize
	if end > total {
		end = total
	}
	return start, end
}

// existingFile reports whether path is a complete copy of the requested file.
// A known SHA-256 is always verified; without one only the size can be checked.
func (c *Client) existingFile(ctx context.Context, path string, size int64, sha string) (Result, bool, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if !st.Mode().IsRegular() {
		return Result{}, false, fmt.Errorf("%w: %s is not a regular file", ErrDestinationExists, path)
	}
	if size > 0 && st.Size() != size {
		return Result{}, false, fmt.Errorf("%w: %s", ErrDestinationExists, path)
	}
	if sha != "" {
		if err := verifyFileSHA256(ctx, path, sha); err != nil {
			if errors.Is(err, ErrChecksumMismatch) {
				return Result{}, false, fmt.Errorf("%w: %s: %v", ErrLocalFileMismatch, path, err)
			}
			return Result{}, false, err
		}
	}
	return Result{
		Dest:             path,
		Size:             st.Size(),
		AlreadyExisted:   true,
		ChecksumVerified: sha != "",
	}, true, nil
}

func verifyFileSHA256(ctx context.Context, path, want string) error {
	if !isSHA256(want) {
		return fmt.Errorf("invalid sha256 %q", want)
	}
	got, err := hashFileSHA256(ctx, path, nil)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: got %s, want %s", ErrChecksumMismatch, got, want)
	}
	return nil
}

func hashFileSHA256(ctx context.Context, path string, onProgress func(int64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 1<<20)
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			read += int64(n)
			if onProgress != nil {
				onProgress(read)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifySHA256(ctx context.Context, path, want string, rep *progressReporter) error {
	if !isSHA256(want) {
		return fmt.Errorf("invalid sha256 %q", want)
	}
	got, err := hashFileSHA256(ctx, path, func(n int64) {
		if rep != nil {
			rep.setVerified(n)
		}
	})
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: got %s, want %s", ErrChecksumMismatch, got, want)
	}
	return nil
}

func contentRangeTotal(v string) int64 {
	slash := strings.LastIndex(v, "/")
	if slash < 0 || slash == len(v)-1 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[slash+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func contentRangeSpan(v string) (start, end int64, ok bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToLower(v), "bytes ") {
		return 0, 0, false
	}
	rest := strings.TrimSpace(v[len("bytes "):])
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return 0, 0, false
	}
	span := rest[:slash]
	dash := strings.IndexByte(span, '-')
	if dash <= 0 || dash == len(span)-1 {
		return 0, 0, false
	}
	s, err1 := strconv.ParseInt(strings.TrimSpace(span[:dash]), 10, 64)
	e, err2 := strconv.ParseInt(strings.TrimSpace(span[dash+1:]), 10, 64)
	if err1 != nil || err2 != nil || s < 0 || e < s {
		return 0, 0, false
	}
	return s, e, true
}

func normalizeETag(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "W/")
	return strings.Trim(v, `"`)
}

func firstHeader(h http.Header, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 && secs <= 60 {
			return time.Duration(secs) * time.Second
		}
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << uint(attempt)
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
