# Resumable Model Downloads — Design

**Status:** shipped in `internal/downloader` (as-built notes below)
**Owner:** (implementer)
**Reviewer:** (senior)
**Supersedes:** the `hf download` delegation described in `docs/LOCAL_MODELS.md`

> **As-built note (2026-11-08).** The transfer engine ships as
> `internal/downloader`, not `internal/hub/fetch.go`. It uses chunk-granular
> resume state (`<file>.part.ciri.json`) rather than the byte-offset segment list
> sketched in §5.2, and the TUI downloads screen lives in `internal/tui/download.go`.
> Remote subdirectories are preserved under the repo directory, requests have a
> 90 s idle watchdog, existing files are checksum-verified when a SHA is known,
> and cross-host pagination links are rejected. Sections 5–10 retain the original
> design for context; where they disagree with the code, the code is authoritative.
> Deferred design items are called out in §6.

---

## 1. Goal

Add downloading and managing/deleting of local LLM artifacts to Ciri, with
**segmented, resumable downloads** — if the network drops mid-transfer, the next
attempt continues from the byte offset already on disk instead of restarting.

## 2. Non-goals

- No external CLI binaries. No `hf`, no `aria2c`, no `wget`. Discovery, transfer,
  verification, and deletion are all native Go + `net/http`.
- No BitTorrent, Metalink, or multi-mirror source switching (aria2c features we
  deliberately do not need — single-origin CDN).
- Ollama integration is **out of scope for this workstream**; `internal/hub/ollama.go`
  is left as-is and revisited later.

## 3. Decision record

**Decision: implement a native Go range-based downloader.**

The alternative was shelling out to `aria2c` (installed, v1.37.0). Rejected because:

1. **Progress fidelity.** The TUI needs byte-accurate progress. `exec` gives you a
   text stream to scrape, not bytes.
2. **Token security is free in stdlib.** Go's `http.Client` strips `Authorization`
   on cross-domain redirects (verified below). Shelling out would have required
   hand-rolled redirect pre-resolution to avoid leaking the HF token to the CDN.
3. **No new dependency class.** Removing `exec` also makes the feature work on
   Windows for free, which matters — `internal/hardware/tools_windows.go` exists.
4. **Testability.** An in-process downloader can be unit-tested against `httptest`
   servers. A subprocess cannot, realistically.

`aria2c`'s headline feature is *parallel connections*. That is a **throughput**
optimization, not a resumability requirement. Resumability is a **correctness**
property that needs only: a partial file on disk, a `Range` request, and a
validator. Build correctness first; parallelism is a knob on top.

## 4. Measured evidence

Every load-bearing assumption below was verified empirically before writing this
doc. Reproduce with the commands given. Do not trust, re-verify after HF changes.

### 4.1 Native discovery gives size **and** sha256 (no CLI needed)

```sh
curl -s "https://huggingface.co/api/models/Qwen/Qwen2.5-0.5B-Instruct-GGUF/tree/main?recursive=true"
```

Returns, per file:

| field | example (q4_k_m.gguf) | meaning |
|---|---|---|
| `size` | `491400032` | exact byte length |
| `lfs.oid` | `74a4da8c9fdbcd15bd1f6d01d621410d31c6fc00986f5eb687824e7b93d7a9db` | **real SHA-256 of the file** |
| `xetHash` | `0663b826e09042345d6e2069d9655cd3ae84812d8f39a5894bed7b24fc0e86cc` | Xet reconstruction hash |

### 4.2 Range requests work through the redirect chain

```sh
curl -sL -o /dev/null -D - -H 'Range: bytes=1000000-1000099' \
  "https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf"
```

```
HTTP/2 302                 <- redirect to CDN
HTTP/2 206                 <- partial content
content-range: bytes 1000000-1000099/491400032
```

`content-range` total (`491400032`) matches the API `size` **exactly**, so segment
boundaries can be computed before any bytes move. Segmented download is viable.

### 4.3 Go strips `Authorization` on the cross-domain hop

`$GOROOT/src/net/http/client.go:1005`:

```go
func shouldCopyHeaderOnRedirect(initial, dest *url.URL) bool {
	ihost := idnaASCIIFromURL(initial)
	dhost := idnaASCIIFromURL(dest)
	return isDomainOrSubdomain(dhost, ihost)
}
```

`Authorization`/`Cookie` are only copied when the destination is the same domain
or a subdomain. Measured redirect targets:

| file kind | redirect target | auth forwarded? |
|---|---|---|
| small/non-LFS (`config.json`) | `307` -> `huggingface.co/api/resolve-cache/...` (same host) | yes — HF's own host, fine |
| LFS/Xet (`.gguf`) | `302` -> `us.aws.cdn.hf.co/xet-bridge-us/...?Policy=...&Signature=...&Expires=...` | **no — cross-domain, stripped** |

The CDN URL carries its own CloudFront signature, so the token is not needed there.
**Consequence: set `Authorization` once on the `huggingface.co` request and let the
stdlib handle the rest.** Do not pre-resolve redirects manually.

### 4.4 `If-Range` does NOT protect resume on this CDN

```sh
curl -sL -o /dev/null -D - -H 'Range: bytes=0-0' \
  -H 'If-Range: "0000000000000000000000000000000000000000000000000000000000000000"' \
  "https://huggingface.co/.../resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf"
```

```
HTTP/2 206
content-range: bytes 0-0/491400032
```

A **bogus** validator still produced `206`. The server ignored `If-Range` (whether
because it was stripped in the redirect or simply unimplemented is not determined —
the design conclusion is the same either way). **Do not rely on `If-Range` for
resume validity.**

*This finding invalidated an earlier design assumption. It is the reason this
section exists.*

### 4.5 The CDN `ETag` is not the checksum

Measured CDN `etag` = `0663b826...` = the **`xetHash`**, not `lfs.oid` (`74a4da8c...`).
**Verify against `lfs.oid`. Never treat `ETag` as a content checksum.**

### 4.6 Out-of-range yields `416`

A request at offset `1_200_000_000` against a `491_400_032`-byte file returned
`416 Range Not Satisfiable`. Handle as "stored size is stale -> re-probe", not fatal.

### 4.7 Concurrent `WriteAt` at distinct offsets is safe

`$GOROOT/src/internal/poll/fd_unix.go:405` — `Pwrite` takes `incref`, **not**
`writeLock`, explicitly because `pwrite` specifies its own offset. So N goroutines
may write disjoint ranges into one shared `*os.File` with no mutex and no
corruption. This is what makes the single-sparse-file layout possible.

## 5. Architecture

### 5.1 On-disk layout

```
~/.ciri/models/                      # CIRI_MODELS_DIR overrides this root
    <owner>/<repo>/
        [<remote subdirs>/]                            # preserved, sanitized
            qwen2.5-0.5b-instruct-q4_k_m.gguf.part         sparse, truncated to full size
            qwen2.5-0.5b-instruct-q4_k_m.gguf.part.ciri.json  resume state (aria2c `.aria2` equivalent)
            qwen2.5-0.5b-instruct-q4_k_m.gguf              final name — appears ONLY after sha256 verifies
```

Segment data goes into **one sparse file** via `WriteAt`, not N `.part.N` files.
A concatenation pass would double the I/O for a multi-GB model for no benefit.

### 5.2 Control file (the resume state)

The shipped state is chunk-granular. Each entry in `Done` covers
`ChunkSize` bytes starting at `index * ChunkSize`; the final chunk may be short.

```go
type resumeState struct {
    URL       string `json:"url"`        // ORIGINAL huggingface.co resolve URL
    Size      int64  `json:"size"`
    ETag      string `json:"etag"`       // CDN validator; not a checksum
    ChunkSize int64  `json:"chunk_size"`
    Done      []bool `json:"done"`       // one flag per chunk
}
```

The control file is the single source of truth for completion. A state file is
only reused when the URL, total size, and (when both are present) ETag match.
A checksum is not persisted because it comes from `lfs.oid` on every invocation.

### 5.3 Interfaces

Shipped package: `internal/downloader`.

```go
// ListRepoFiles lists files in a Hugging Face repository revision, following
// Link pagination and extracting LFS size + sha256.
func (c *Client) ListRepoFiles(ctx context.Context, repo, revision string) ([]RepoFile, error)

// Download fetches one file into Request.DestDir, resuming from a .part +
// state file when compatible. onProgress may be nil.
func (c *Client) Download(ctx context.Context, req Request, onProgress func(Progress)) (Result, error)

type Request struct {
    Repo, Revision, Path string
    DestDir              string
    SizeHint             int64
    SHA256               string
    Connections          int   // default 4, capped at 8
    ChunkSize            int64 // default 16 MiB
}

// RepoDestDir resolves ~/.ciri/models/<owner>/<repo> (or CIRI_MODELS_DIR).
func RepoDestDir(repo string) string
```

`internal/tui/download.go` consumes these types through `tea.Cmd`; `Update` never
blocks on network I/O. The old `HFClient.Download` / `hf` subprocess path does
not exist in the shipped tree.

### 5.4 Algorithm (as shipped)

```
1.  validate repo/path/dest; derive a safe relative path; mkdir dest + subdirs
2.  local fast path: if SizeHint + SHA256 are known and the final file exists,
        verify its checksum -> AlreadyExisted (no network)
3.  probe: GET resolve URL with "Range: bytes=0-0"
        206 -> total := parse Content-Range
        200 -> total := Content-Length, server ignores ranges => single-stream mode
        other -> status error (401/403/404/429 mapped to readable messages)
4.  if final file exists: compare size and, when known, checksum
        match -> AlreadyExisted
        stale/invalid -> ErrDestinationExists / ErrLocalFileMismatch (never overwrite)
5.  choose chunk size; compute chunk count
6.  load state file; reuse only if URL, size and (when present) ETag match
        reuse -> open existing .part
        else  -> truncate .part to total and write a fresh state file
7.  spin up min(Connections, chunks) workers pulling chunk indexes atomically
        per attempt: GET Range from start+got
        require 206 + Content-Range span and total checks
        cap the read at the remaining range (never write into the next chunk)
        transient error -> backoff and retry from the in-memory offset
        after the chunk completes: f.Sync(), then mark it done in state
8.  if the probe returned 200: stream the whole body once, capped at total
9.  f.Sync()
10. if SHA256 is known: stream .part through sha256
        only a real mismatch deletes .part + state; cancel/I/O keeps it (I6)
11. os.Rename(.part, final); remove state; emit "done"
```

`Content-Range` is required on every 206 and verified before any bytes are
written, so a server that returns a shifted range cannot corrupt the file (I3).
All requests also carry a 90 s idle watchdog (headers and body); a stalled CDN
surfaces as `ErrIdleTimeout` and is retried/kept resumable.

### 5.5 Chunk sizing

```go
const (
    DefaultConnections = 4
    DefaultChunkSize   = 16 << 20 // 16 MiB
)
```

An explicit `Request.ChunkSize` is honored (clamped to the file size). Otherwise
the default is used; for files smaller than `connections * chunkSize` the chunk
is halved per connection, with a 1 MiB floor. Sub-1 MiB files therefore use a
single chunk.

### 5.6 Retry / backoff

This retry loop **is** the "network cut off" feature.

- 4 attempts per chunk, base `500ms`, doubling, capped at `8s`.
- Retry on: connection reset, EOF mid-body, timeout, 408, 425, 429, 5xx.
- Honour `Retry-After` on retryable statuses (capped at 60s).
- Do **not** retry on: 401/403 (auth), 404 (missing), checksum mismatch.
- Every retry resumes from the in-memory offset; retries never reset `Done`.

### 5.7 Concurrency, no new dependencies

`golang.org/x/sync/errgroup` is **not** in `go.mod` and is not needed. Workers
share an atomic index into the chunk list and a `sync.Once` first-error capture;
the first fatal error cancels the child context and stops the other workers.
Chunks write with `WriteAt` at disjoint offsets, which is safe without a mutex.

### 5.8 Progress

`atomic.Int64` accumulates downloaded and verified bytes. A `400ms` ticker
emits `Progress` with bytes, total, smoothed speed, ETA, chunk counts, phase
(`downloading`, `verifying`, `done`) and connection count. Final snapshots are
emitted when the reporter stops. The TUI forwards these through a buffered
channel into `tea.Cmd`; it never polls the file or blocks `Update`.

## 6. Invariants (each one is a bug if violated)

**As-built status.** Implemented: I1, I2, I3, I5, I6, I7, I9. Partially
implemented: I4 (resume state is invalidated on URL/size/ETag mismatch, but the
revision is not pinned to an immutable commit SHA). Deferred: I8 (no lockfile;
the TUI is currently the only writer) and I10 (URLs use `main`, not a pinned
SHA). Treat the text below as the design intent; the deferred items are known
gaps, not claims about the shipped code.

**I1 — The control file is the only completion signal.**
`f.Truncate(size)` at step 5 means the `.part` file reports its *full size* in
`stat`/`du` from second zero. Any "is it done?" check based on file size is wrong.

**I2 — Order is: write -> `f.Sync()` -> record in control.**
If the control file claims bytes that were never flushed, resume writes at a wrong
offset and silently corrupts. Cost of the strict order is re-fetching up to
`syncInterval` worth of bytes after a hard crash — acceptable, and must be
documented in the code. Always sync on cancel.

**I3 — Verify `Content-Range`, never blind-append.**
Parse the returned start from `Content-Range: bytes S-E/T`. If `S != Start+Done`,
seek to `S` or abort. Do not assume the server honoured the exact range.

**I4 — Revision change invalidates the partial.**
Commit SHA mismatch, size mismatch, or checksum mismatch vs the control file =>
discard the partial. A partial from a different revision is garbage.

**I5 — Never write the manifest before the checksum passes.**
The previous `hf`-based path set `Installed = true` immediately after the child
exited. That is how a truncated 400 MB GGUF becomes permanently advertised as
installed. Verify, *then* rename, *then* `WriteManifest`.

**I6 — Cancel keeps the partial.**
On `ctx` cancellation: sync, persist control, return. Do **not** clean up `.part`
or the control file on cancel — that silently destroys the feature. Only delete
partials on revision/checksum invalidation, or on explicit user "delete".

**I7 — Degrade gracefully when ranges are refused.**
If the probe returns `200`, the origin does not support ranges: download
single-stream, still atomic-rename, but record no resumability. Do not fail.

**I8 — One writer per artifact.**
Two concurrent downloads into one directory corrupt each other. Take a lockfile
(`O_CREATE|O_EXCL`) keyed on artifact ID. A held lock is a normal UI state
("already downloading"), not an error.

**I9 — Re-resolve; never persist the signed URL.**
The CDN URL has `Expires=`. Persist the original `huggingface.co/.../resolve/<sha>/...`
URL and let Go re-follow on every request. Caching the signed URL yields
inexplicable 403s an hour later.

**I10 — Pin the commit SHA, not `main`.**
Because `If-Range` is not enforced (4.4), immutability of the ref is the *only*
thing making resume safe. Resolve `main` -> SHA once, then build URLs from the SHA.

## 7. Security

- `HF_TOKEN` is applied **only** to the `huggingface.co` request. The stdlib strips
  it on the cross-domain CDN hop (4.3). Pagination `Link` targets are followed
  only when they resolve to the configured endpoint host, so a foreign `next`
  URL cannot receive the token.
- Never log the token, never place it in a URL, never pass it in `argv`. Native Go
  gets this last one for free.
- Filenames from the API are untrusted: `safeRelPath` rejects absolute paths,
  `..` segments, backslashes, and drive separators before touching the
  filesystem, and preserves sanitized remote subdirectories.
- `DeleteManaged` already guards against escaping `modelsDir` and requires a
  validated manifest. Do not weaken it.

## 8. Task breakdown

**As-built note.** The shipped work did not follow this exact sequence. The
engine landed as `internal/downloader` (probe, parallel chunks, resume state,
retry, checksum, atomic rename) with unit tests, and the TUI download screen is
a manager: downloads start from model detail, the queue supports sequential or
concurrent transfer, and `Esc` leaves the screen without cancelling. The older
`BrowseModels -> BrowseFiles -> Downloading -> Done` state machine has been
replaced.

Each step below is the original plan, kept for context. Do not start step *n+1*
before step *n* compiles and its tests pass.

1. `git stash pop`; confirm `go build ./...` and `go test ./...` are green.
2. `internal/hub/fetch.go`: `ResolveRevision` only. Test against the live API
   (build-tagged or `testing.Short()` guarded).
3. Probe + single-segment, non-resumable download with atomic rename. No resume yet.
4. Control file: write/load/validate. Add `TestRevisionChangeDiscardsPartial`.
5. Real resume: multi-segment, `WriteAt`, per-segment `Done`.
6. Retry/backoff.
7. sha256 verification + publish ordering (I5).
8. Lockfile (I8).
9. Replace `HFClient.Download` body; delete `Command`, `exec.LookPath`.
10. Wire `internal/tui/download.go` via `tea.Cmd`. **Never block `Update`.**
11. `internal/tui/local.go`: `Inventory()` list; `d` -> `DeleteManaged` behind a
    confirm prompt, refusing while a lock is held.
12. Update `docs/LOCAL_MODELS.md` — it currently claims downloads delegate to
    `hf download`, which is now false.

Ask the reviewer before step 11; the delete path needs the lock check from I8.

## 9. Test plan

Unit tests against `httptest` servers. No network in CI.

**As-built coverage.** Passing in `internal/downloader`:
`TestDownload_ParallelRangesAndChecksum`, `TestDownload_ChecksumMismatchRemovesState`,
`TestDownload_ResumesFromState`, `TestDownload_AlreadyDownloaded`,
`TestDownload_ContentRangeMismatch`, `TestDownload_RangeUnsupportedFallsBack`,
`TestDownload_CancelKeepsPartialAndState`, `TestDownload_CancelDuringVerifyKeepsPartial`,
`TestDownload_OverlongBodyDoesNotCorrupt`, `TestDownload_MissingContentRangeRejected`,
`TestDownload_ContentRangeTotalChangeRejected`, `TestDownload_IdleTimeoutRetries`,
`TestDownload_ExistingWrongFileRejected`,
`TestDownload_AlreadyDownloadedOffline`, `TestDownload_PreservesRemoteSubdirectory`,
`TestClient_NoAuthHeaderOnCrossDomainRedirect`, `TestClient_ListRepoFiles`,
`TestClient_ListRepoFilesSameHostPagination`, `TestClient_ListRepoFilesRejectsCrossHostLink`,
`TestChooseChunkSize_Bounds`, `TestNextLink`, `TestSafeRelPath`. TUI state and
layout coverage lives in `internal/tui/download_test.go`.

| test | asserts | status |
|---|---|---|
| `TestNoAuthHeaderOnCrossDomainRedirect` | server A 302s to a different host; **server B never sees `Authorization`**. Security test — not optional | passing |
| `TestDownload_ResumesFromState` | reuse of a compatible state file; final bytes match | passing |
| `TestDownload_ParallelRangesAndChecksum` | multi-chunk reassembly + sha256 | passing |
| `TestDownload_ContentRangeMismatch` | server returns a shifted range => error, no corruption (I3) | passing |
| `TestDownload_RangeUnsupportedFallsBack` | `200` => single-stream success (I7) | passing |
| `TestDownload_CancelKeepsPartialAndState` | `.part` + state survive `ctx` cancel (I6) | passing |
| `TestDownload_ChecksumMismatchRemovesState` | mismatch removes `.part` + state (I5) | passing |
| `TestDownload_CancelDuringVerifyKeepsPartial` | cancel during sha256 keeps `.part` + state (I6) | passing |
| `TestDownload_OverlongBodyDoesNotCorrupt` | a range response longer than requested cannot overwrite the next chunk | passing |
| `TestDownload_MissingContentRangeRejected` | `206` without `Content-Range` is rejected before writing (I3) | passing |
| `TestDownload_ContentRangeTotalChangeRejected` | a changed total is rejected instead of mixing revisions | passing |
| `TestDownload_IdleTimeoutRetries` | stalled response is retried, partial stays resumable | passing |
| `TestDownload_ExistingWrongFileRejected` | same-size but wrong local file is not reported as already downloaded | passing |
| `TestDownload_AlreadyDownloadedOffline` | complete local file + known SHA needs no network | passing |
| `TestDownload_PreservesRemoteSubdirectory` | `Q4_K_M/model.gguf` stays under that subdirectory | passing |
| `TestClient_ListRepoFiles` | pagination-ready listing, LFS size + oid | passing |
| `TestClient_ListRepoFilesRejectsCrossHostLink` | cross-host `Link` is rejected before the token can be sent | passing |
| `TestConcurrentDownloadLocked` | second downloader refuses (I8) | deferred (no lockfile) |
| `Test416TriggersReprobe` | stale state => re-probe | deferred |
| `TestRevisionChangeDiscardsPartial` | (I4) | deferred (URL/size/ETag invalidation only) |
| `TestSmallFileIsNotSegmented` | small file uses one chunk | covered by `TestChooseChunkSize_Bounds` |

**Acceptance test (manual, required before merge):** start a full GGUF download,
kill the network mid-flight, restart. The byte counter must continue from where it
stopped, **not zero**. Record the before/after offsets in the PR.

**Adversarial test:** hand-edit the control file to claim 50% done against a
mostly-empty `.part`. Must produce a checksum failure and must **not** publish a
manifest.

## 10. Review checklist

The reviewer will check, in order:

1. Does `Update` ever block? (step 10)
2. Is `f.Sync()` strictly before marking a chunk done in the state file? (I2)
3. Is resume validity independent of `If-Range` (which the CDN ignores, §4.4)?
4. Is the persisted URL the original, not the signed one? (I9)
5. Is the commit SHA pinned? (I10) — **currently deferred; URLs use `main`**
6. Is the file published only after the checksum? (I5)
7. Does cancel preserve partials, including during checksum verification? (I6)
8. Does the auth test exist and pass, and are pagination `Link`s host-checked? (7)
9. Are `ETag` and `lfs.oid` correctly distinguished? (4.5)
10. Is the lock released on every error path, including panic? (I8) —
    **currently deferred; no lockfile**

## 11. Open questions

- **Xet throughput.** Downloads go via the `xet-bridge` CloudFront path. Plain HTTP
  ranges are confirmed working, but whether parallel ranges actually improve
  throughput on Xet-backed repos (vs a native xet client) is unmeasured. Measure in
  step 5 and record real numbers; if parallelism shows no gain, set `maxSegments = 1`
  and keep the code simple.
- **`maxSegments` default.** 8 is a starting guess. Tune against real CDN numbers,
  not intuition.
- **Non-LFS repos.** Only Xet/LFS-backed files were measured. Confirm the same
  `206` semantics on a small non-LFS repo during step 3.
