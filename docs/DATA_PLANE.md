# Remote Data Plane, Evals & GPU Recipes — Design

**Status:** draft for review
**Owner:** (implementer)
**Reviewer:** (senior)
**Related:** `docs/DOWNLOADS.md` (weights transfer — a separate pipeline), `docs/DOCUMENTAION.md` §2 (current data files)

---

## 1. Goal

Make Ciri's data **fresh without shipping a new binary**, and give it a place to
store things the community produces:

1. **Catalog sync.** Pull new/changed models from the Hugging Face API on a
   schedule, enrich them, publish a versioned snapshot.
2. **One update path.** `ciri data update` (and a row in Settings) fetches the
   snapshot and activates it. Offline is a supported state, not an error.
3. **Evals.** Store published benchmark results per canonical model, with
   provenance (vendor / third-party / community) and citeable sources.
4. **GPU recipes.** Users publish a working configuration — engine, quant, flags,
   measured tok/s, GitHub link — keyed to the GPU they ran it on. Users with the
   same GPU can find them.

Everything above is one system: a **write model in Postgres**, a **read model in
immutable JSON packs**, and a CLI that only ever reads packs.

## 2. Non-goals

- **No self-updating binary and no source-tree rewriting.** `go install` users
  have no source tree; mutating an installed binary is worse.
- **No per-user HF crawling.** The CLI never talks to the Hugging Face API for
  catalog data. One server-side crawler, many readers.
- **No eval framework.** We store benchmark *definitions* and *results*. Running
  evals is out of scope; wrap `lm-evaluation-harness` later if needed.
- **No DB access from the CLI.** Postgres is behind one HTTP API.
- **No accounts in phase 1–2.** Publishing (phase 3) introduces GitHub auth.
- **Weights transfer is not this document.** That is `docs/DOWNLOADS.md`; model
  metadata comes from the company data plane, weights come from HF's CDN.
- **No real-time streaming.** Packs are snapshots. Freshness is hourly, not
  per-request.

## 3. Decision record

**D1 — Serve versioned JSON packs, not API queries, for bulk reads.**
The catalog is 5,348 rows today and will grow. Rendering `Explore` from an API
would put a 5k-row query on Postgres per keystroke. A pack is one `GET`,
cacheable, verifiable, and works offline. Rejected: thin client + fat API.

**D2 — Embedded baseline + overlay cache.**
`data/data.go` keeps embedding the three JSON files. At startup the loader looks
for an activated external pack and uses it if newer and valid. The binary works
on a plane; updates are an enhancement. Rejected: fetch-on-first-run (breaks
offline and first-run UX).

**D3 — Postgres is the write model; packs are the read model.**
Every write (ingest, submission, moderation) lands in Postgres. A publisher job
materialises packs from Postgres. The CLI never sees Postgres. This keeps the
read path cheap and lets us rebuild any pack deterministically. It is CQRS with
one writer, not event sourcing.

**D4 — Build the canonical model graph before evals.**
Today the catalog is 5,348 *quantized repos*, 4,997 of which are `Q4_K_M`.
An eval for `meta-llama/Llama-3.3-70B-Instruct` has nowhere to attach. Evals,
recipes, and runtime benchmarks all key off a canonical model, so the graph is
a prerequisite, not a follow-up.

**D5 — Valkey is ephemeral only.**
Locks, rate limits, ETag caching, short-lived list caches. If Valkey is flushed,
every answer must still be correct, just slower. Nothing in it is authoritative.

**D6 — One Go module, shared wire types.**
`internal/wire` holds pure JSON structs with no external imports. Both
`cmd/ciri` and `cmd/ciri-server` import it, so the contract cannot drift. Server
dependencies (`pgx`, `valkey-go`) are imported only under `cmd/ciri-server` and
`internal/store`, so the CLI binary does not grow. Accepted cost: root `go.mod`
lists those modules and `go mod tidy` pulls them for source builds. Alternative
(two modules) rejected: `replace` directives and duplicated structs are worse.

**D7 — The Go server serves packs itself; no CDN yet.**
`http.ServeContent` gives `Range`, `If-None-Match`, and `Last-Modified` for free.
Measured payloads are small (see §4.1), so a single box plus gzip is enough until
traffic says otherwise. Rejected for now: putting packs in R2/S3 (another system
to operate for a 226 KB file).

## 4. Measured evidence

Every load-bearing assumption was probed before writing this. Re-run the commands
after HF changes anything. Date of measurement: 2026-11-07.

### 4.1 The whole catalog gzips to ~226 KB

```sh
for f in data/*.json; do echo "$f $(stat -c%s $f) $(gzip -9 -c $f | wc -c)"; done
```

| file | raw | gzip | ratio |
|---|---:|---:|---:|
| `hf_models.json` | 4,400,003 | 225,851 | 5.1% |
| `benchmark_cache.json` | 826,579 | 69,396 | 8.3% |
| `gpus.json` | 118,175 | 8,773 | 7.4% |
| `artificial_analysis.json` | 196,864 | 17,243 | 8.7% |

JSON's repeated keys compress brutally. **An update is ~226 KB, not 4.3 MB.**
That is a rounding error against a model download, so "update data" can be
automatic and silent if we choose. It also means no CDN is required (D7).

### 4.2 HF rate limit is 500 requests / 300 s

```sh
curl -s -D- -o /dev/null 'https://huggingface.co/api/models?limit=1&sort=createdAt&direction=-1'
```

```
ratelimit: "api";r=499;t=90
ratelimit-policy: "fixed window";"api";q=500;w=300
etag: W/"154-AIeUqF9k7Vfm94joaPaY1MxnvlQ"
link: <https://huggingface.co/api/models?...&cursor=eyJfaWQi...%3D>; rel="next"
```

Consequences: an incremental crawl at `limit=1000` costs ~6–10 requests/hour
against a 500/5-minute budget. **Server-side is cheap; per-user is abuse.** The
CLI must never do this (D2, §2).

### 4.3 Pagination is cursor-based, and `ETag` exists

Follow `Link: rel="next"` until absent. Do **not** build `?page=N` — the cursor
is opaque and stable under inserts, which matters for an hourly writer.

### 4.4 One request returns params, card data, and the parent model

`expand[]` works on the *list* endpoint, in bulk:

```sh
curl -s 'https://huggingface.co/api/models?limit=3&sort=downloads&direction=-1\
&filter=text-generation&expand[]=safetensors&expand[]=cardData'
```

```
openai-community/gpt2 | params: 137022720  | base: None
Qwen/Qwen3-0.6B      | params: 751632384  | base: ['Qwen/Qwen3-0.6B-Base']
Qwen/Qwen3-14B       | params: 14768307200 | base: ['Qwen/Qwen3-14B-Base']
```

`safetensors.total` is an exact parameter count. This replaces every heuristic
guess in the current catalog.

### 4.5 Quantized repos declare their parent

```sh
curl -s 'https://huggingface.co/api/models/unsloth/Llama-3.3-70B-Instruct-GGUF?expand[]=tags'
```

```
base_model:meta-llama/Llama-3.3-70B-Instruct
base_model:quantized:meta-llama/Llama-3.3-70B-Instruct
```

Confirmed on `unsloth/*`, `bartowski/*`, and `lmstudio-community/*` GGUF repos.
**The parent graph already exists in HF metadata.** We do not need to infer it
from repo names.

### 4.6 `model-index` is effectively empty in the wild

Checked `Qwen/Qwen2.5-72B-Instruct`, `deepseek-ai/DeepSeek-V3`,
`meta-llama/Llama-3.1-8B-Instruct`, `mistralai/Mistral-Large-Instruct-2411`:
**zero** `model-index` entries.

Consequence: vendor-published eval numbers are **not machine-readable**. They are
curated rows with a URL and a date (§7), not a crawler output. Budget effort
accordingly.

### 4.7 What the current catalog actually is

| observation | value | implication |
|---|---|---|
| rows | 5,348 | not 1,000 — README is stale |
| `quantization == Q4_K_M` | 4,997 (93%) | one row per GGUF repo, not per model |
| `release_date` present | 154 (2.9%) | Date column is nearly empty |
| canonical/base field | **absent** | evals cannot attach (D4) |
| `artificial_analysis.json` | present, not embedded | already-paid-for data, unused |
| `net/http` imports in repo | **0** | networking is new ground |

## 5. Architecture

### 5.1 The three planes

```
HF API ──(hourly, 1 crawler)──> ingest ──> Postgres ──> publisher ──> packs on disk
                                                                          │
CLI ──(GET manifest, conditional)──────────────────────────────────────────┘
 │                                                                        
 ├─ embedded baseline (offline fallback, never mutated)                  
 └─ cache dir (activated packs)                                          

CLI ──(POST recipe/eval, GitHub device auth)──> write API ──> Postgres ──> moderation
                                                                    └──> next publish
```

Rules that fall out of this: reads never touch Postgres; writes never touch packs;
packs are immutable; the manifest is written last.

### 5.2 Server layout

```
<dataDir>/
    v1/manifest.json
    v1/packs/catalog/2026.11.07.1.json.gz
    v1/packs/gpus/2026.11.07.1.json.gz
    v1/packs/benchmarks/2026.11.07.1.json.gz
    v1/packs/evals/2026.11.07.1.json.gz
    v1/packs/recipes/2026.11.07.1.json.gz
    tmp/                     staging for atomic rename
```

Endpoints:

| method | path | auth | notes |
|---|---|---|---|
| GET | `/v1/manifest.json` | none | `no-cache`, ETag, 304 |
| GET | `/v1/packs/{name}/{version}.json.gz` | none | `immutable`, `ServeContent` |
| POST | `/v1/recipes` | device token | phase 3 |
| POST | `/v1/benchmarks` | device token | phase 3 |
| POST | `/v1/reports` | none + rate limit | abuse |
| POST | `/v1/auth/github/device` | none | phase 3 |
| GET | `/v1/healthz` | none | liveness + PG/Valkey |

`http.ServeContent` handles `Range`, `If-None-Match`, `If-Modified-Since`.
Do not hand-roll those.

### 5.3 Client cache layout

```
$XDG_CACHE_HOME/ciri/           # %LOCALAPPDATA%\ciri\Cache on Windows
    data/state.json             # active data_version, per-pack version+sha256, manifest etag
    data/catalog/2026.11.07.1.json
    data/evals/2026.11.07.1.json
    data/tmp/                   # downloaded, not yet activated
    data/failed/                # quarantined; deleted on next successful update
```

Override with `CIRI_CACHE_DIR`. `state.json` is written **last** (§10, I6).

### 5.4 Manifest

```json
{
  "schema_version": 1,
  "data_version": "2026.11.07.1",
  "generated_at": "2026-11-07T10:00:00Z",
  "min_cli_version": "1.1.0",
  "packs": {
    "catalog": {
      "version": "2026.11.07.1",
      "path": "/v1/packs/catalog/2026.11.07.1.json.gz",
      "sha256": "…",
      "bytes": 225851,
      "rows": 5348,
      "schema": 2
    }
  }
}
```

`min_cli_version` is checked before any pack is activated. A newer pack with an
older CLI is a *refusal*, not a best-effort decode.

### 5.5 Postgres schema

```sql
-- control / provenance
CREATE TABLE data_version (
  version        text PRIMARY KEY,          -- 2026.11.07.1
  built_at       timestamptz NOT NULL,
  manifest_sha   text NOT NULL,
  status         text NOT NULL              -- building | published | rolled_back
);

CREATE TABLE ingest_run (
  id               bigserial PRIMARY KEY,
  started_at       timestamptz NOT NULL,
  finished_at      timestamptz,
  watermark        timestamptz,             -- last successful last_modified
  repos_seen       int DEFAULT 0,
  models_upserted  int DEFAULT 0,
  error            text
);

-- raw landing: never mutated after write, useful to rebuild the graph
CREATE TABLE hf_repo (
  hf_id         text PRIMARY KEY,
  sha           text,
  created_at    timestamptz,
  last_modified timestamptz,
  downloads     bigint,
  likes         int,
  gated         boolean,
  pipeline_tag  text,
  tags          jsonb,
  raw           jsonb,
  fetched_at    timestamptz NOT NULL
);

-- canonical graph (D4)
CREATE TABLE model (
  id              text PRIMARY KEY,         -- hf_id of the parent, e.g. meta-llama/Llama-3.3-70B-Instruct
  display_name    text NOT NULL,
  provider        text,
  params_total    bigint,
  architecture    text,
  license         text,
  context_length  int,
  release_date    date,
  downloads       bigint,
  likes           int,
  status          text NOT NULL DEFAULT 'active'   -- active | deprecated
);

CREATE TABLE variant (
  id          text PRIMARY KEY,             -- hf_id of the repo, e.g. unsloth/…-GGUF
  model_id    text NOT NULL REFERENCES model(id),
  quant       text,                         -- Q4_K_M, AWQ-4bit, …
  format      text,                         -- gguf | safetensors | mlx
  bytes       bigint,
  files       jsonb,
  source      text
);

-- benchmarks: definitions are data, not code
CREATE TABLE benchmark (
  slug           text PRIMARY KEY,          -- gpqa-diamond
  name           text NOT NULL,
  category       text NOT NULL,             -- knowledge | reasoning | coding | agentic | vision | long-context
  publisher      text,
  harness        text,                      -- lm-eval | swebench | vendor | arena
  harness_version text,
  url            text,
  license        text,
  unit           text NOT NULL,             -- percent | elo | pass@1
  higher_is_better boolean NOT NULL DEFAULT true
);

CREATE TABLE eval_result (
  id                bigserial PRIMARY KEY,
  model_id          text NOT NULL REFERENCES model(id),
  benchmark_slug    text NOT NULL REFERENCES benchmark(slug),
  benchmark_version text NOT NULL,          -- "v1", "2025-09"
  score             numeric NOT NULL,
  unit              text NOT NULL,
  source_kind       text NOT NULL,          -- vendor | third_party | community
  source_url        text,
  evaluator         text NOT NULL,
  measured_at       date,
  method_note       text,
  submitted_by      text,
  verified          boolean NOT NULL DEFAULT false,
  data_version      text,
  UNIQUE (model_id, benchmark_slug, benchmark_version, source_kind, evaluator)
);

-- community runtime measurements (supersedes the scraped benchmark_cache.json)
CREATE TABLE runtime_benchmark (
  id             bigserial PRIMARY KEY,
  gpu_slug       text NOT NULL,
  variant_id     text NOT NULL REFERENCES variant(id),
  engine         text NOT NULL,
  engine_version text,
  quant          text,
  flags          jsonb,
  flags_hash     text NOT NULL,
  tok_s_out      numeric,
  tok_s_prefill  numeric,
  ttft_ms        int,
  vram_peak_gb   numeric,
  ctx_len        int,
  batch_size     int,
  source_kind    text NOT NULL,
  github_url     text,
  submitted_by   text,
  verified       boolean NOT NULL DEFAULT false,
  data_version   text,
  created_at     timestamptz NOT NULL DEFAULT now()
);

-- recipes (phase 3)
CREATE TABLE recipe (
  id             bigserial PRIMARY KEY,
  gpu_slug       text NOT NULL,
  variant_id     text NOT NULL REFERENCES variant(id),
  title          text NOT NULL,
  engine         text NOT NULL,
  engine_version text,
  quant          text,
  flags          jsonb,
  flags_hash     text NOT NULL,
  tok_s_out      numeric,
  ttft_ms        int,
  vram_peak_gb   numeric,
  ctx_len        int,
  github_url     text,                      -- normalised, commit-pinned
  github_commit  text,
  author_handle  text,
  status         text NOT NULL DEFAULT 'pending',  -- pending | published | hidden
  data_version   text,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON recipe (gpu_slug, variant_id, status);
CREATE UNIQUE INDEX ON recipe (gpu_slug, variant_id, engine, flags_hash) WHERE status = 'published';

CREATE TABLE recipe_reproduction (
  id          bigserial PRIMARY KEY,
  recipe_id   bigint NOT NULL REFERENCES recipe(id),
  ok          boolean NOT NULL,
  tok_s_out   numeric,
  reported_by text,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE report (
  id          bigserial PRIMARY KEY,
  subject_kind text NOT NULL,               -- recipe | eval | benchmark
  subject_id  bigint NOT NULL,
  reason      text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);
```

`gpu_slug` is generated from `gpus.json` (`nvidia-rtx-5090`). Phase 0 adds an
explicit `id` field to each GPU entry so the key is stable and reviewable; the
existing `pci_device_ids` stay the matching mechanism. Do **not** key on the
display name — it changes with driver strings.

### 5.6 Valkey keys

| key | type | TTL | purpose |
|---|---|---|---|
| `lock:ingest:hf` | string | 3300 s | single crawler across replicas |
| `lock:publish` | string | 600 s | publish is single-writer |
| `manifest:etag` | string | ∞ (invalidated) | current manifest hash for 304s |
| `ratelimit:<scope>:<id>` | counter | window | write-path limiting |
| `dedupe:<scope>:<hash>` | string | 24 h | double-submit protection |
| `cache:recipes:<gpu_slug>` | string | 60 s | optional hot list |

If Valkey is down: ingest and publish refuse to start (they need the lock), reads
continue from disk, write API returns 503. Reads must never depend on Valkey.

### 5.7 Ingest algorithm

```
1.  SET lock:ingest:hf NX EX 3300    → not acquired: exit 0 (another replica is running)
2.  watermark := SELECT coalesce(max(last_modified), epoch) FROM hf_repo
3.  cursor := ""
    loop:
        GET /api/models?limit=1000&sort=lastModified&direction=-1
            &expand[]=safetensors&expand[]=cardData&expand[]=tags&expand[]=downloads
            &cursor=<cursor>
        if 429: sleep Retry-After; retry (max 5)
        throttle to <= 1.5 req/s (500/300s with headroom)
4.  for each repo in page:
        if last_modified <= watermark: break outer      // incremental, sorted desc
        keep if: has safetensors.total OR tags contain gguf|mlx|awq|gptq|exl2
                 AND pipeline_tag in (text-generation, image-text-to-text, …)
                 AND safetensors.total >= 500e6 (when known)
        UPSERT hf_repo (raw)                             // idempotent
        parent := cardData.base_model
                  || first tag matching `base_model:quantized:<x>`
                  || first tag matching `base_model:<x>`
        parent == nil ? model := repo : model := parent
        UPSERT model / variant
5.  COMMIT per page. A crash resumes from the watermark. Partial pages are safe.
6.  UPDATE ingest_run; watermark := max(last_modified) seen.
```

Step 4 is where the 5,348-row Q4_K_M soup becomes ~600 models with variants.
Step 5 matters: never hold a transaction open across pages.

### 5.8 Publish algorithm

```
1.  acquire lock:publish
2.  version := UTC date + counter ("2026.11.07.1")
3.  for each pack: SELECT … ORDER BY id  → stream JSON → gzip → tmp file + sha256
4.  fsync file; rename tmp → v1/packs/<name>/<version>.json.gz; fsync dir
5.  build manifest from the actual bytes written (never from intent)
6.  write manifest.tmp; fsync; rename over v1/manifest.json
7.  INSERT data_version(status='published'); DEL manifest:etag
8.  delete pack versions older than the last N activations
```

The manifest is last (I7). A client can never observe a version whose packs are
missing. Packs are immutable: a bad row is fixed by publishing a new version,
never by editing an old file.

### 5.9 Client update algorithm

```
1.  state := load state.json (absent → embedded baseline)
2.  GET manifest.json  If-None-Match: state.manifest_etag
        304 → "up to date", done
3.  if manifest.min_cli_version > Version → report "update the CLI" and stop
    if pack.version == state.version && pack.sha256 == state.sha256 → skip
    stream to tmp; hash while streaming; compare sha256 and bytes
    gzip decode + json decode + check schema → else quarantine (I5)
4.  fsync; rename tmp → data/<name>/<version>.json
5.  write state.json atomically (tmp + rename) — LAST
6.  reload in-memory snapshot; if load fails → revert to previous version, report
```

Never delete the previous version during an update; it is the rollback target.
Prune to two versions after a successful activation.

### 5.10 Interfaces

`internal/wire` (no external imports — shared by CLI and server):

```go
type Manifest struct {
    SchemaVersion int              `json:"schema_version"`
    DataVersion   string           `json:"data_version"`
    GeneratedAt   time.Time        `json:"generated_at"`
    MinCLIVersion string           `json:"min_cli_version"`
    Packs         map[string]Pack  `json:"packs"`
}

type Pack struct {
    Version string `json:"version"`
    Path    string `json:"path"`
    SHA256  string `json:"sha256"`
    Bytes   int64  `json:"bytes"`
    Rows    int    `json:"rows"`
    Schema  int    `json:"schema"`
}
```

`internal/remote` (client):

```go
type Client struct { BaseURL string; HTTP *http.Client; UserAgent string }

func (c *Client) Manifest(ctx context.Context, etag string) (*wire.Manifest, error)
    // ErrNotModified on 304
func (c *Client) FetchPack(ctx context.Context, p wire.Pack, dst io.Writer) error
    // streams, caller hashes
```

`internal/dataplane` (CLI-side store):

```go
func Open(dir, baselineVersion string) (*Store, error)
func (s *Store) Snapshot() Snapshot        // active catalog/gpus/benchmarks/evals
func (s *Store) Active() (dataVersion string, at time.Time)
func (s *Store) Activate(p wire.Pack, r io.Reader) error   // verify → atomic rename
```

`cmd/ciri/main.go:17-54` changes from three `embed.ReadFile` calls to
`dataplane.Open(...)` + one `Snapshot`. `internal/model/catalog.go:36` and
`internal/predictor/estimate.go:80` keep their signatures; they just receive
bytes from the snapshot instead of the embed FS.

## 6. Canonical model graph

Grouping rules, in order of confidence:

1. `cardData.base_model` present → that is the parent.
2. Tag `base_model:quantized:<parent>` → parent (highest confidence for GGUF/MLX).
3. Tag `base_model:finetune:<parent>` → parent is a *finetune*; keep it as its own
   model but link `derived_from`. A finetune has its own evals; a quantized repo
   does not.
4. Nothing → the repo is its own canonical model.

Quant and format come from tags/filenames. **Do not replicate the current
`Q4_K_M` default** (`internal/model/catalog.go` has no fallback, so the 4,997
rows came from the scraper). When quant is unknown, store `unknown` and let the
UI show `—`. Wrong is worse than blank, especially for VRAM math.

The pack's `catalog` becomes:

```json
{ "schema": 2,
  "models":   [ { "id": "meta-llama/…", "params_total": 70553706496, "license": "llama3.3", … } ],
  "variants": [ { "id": "unsloth/…-GGUF", "model_id": "meta-llama/…", "quant": "Q4_K_M", "bytes": 40400000000 } ] }
```

The TUI's model list iterates `models`; the Detail screen lists `variants`.
`Explore` gets ~9x shorter, which is the actual user-facing win here.

## 7. Evals

Hard rule: **provenance is a required field, and sources are never mixed in one
ranking.** A vendor number and a third-party number are different measurements
even when they share a benchmark name. `source_kind` is `vendor`,
`third_party`, or `community`; the UI badges all three and never sorts them
together.

Ownership of `benchmark` definitions is ours; rows are seeded from a reviewed
JSON file (not scraped), because §4.6 shows there is nothing machine-readable to
scrape. Each row needs `source_url`, `evaluator`, `measured_at`, and
`harness_version`.

Sources, in order of trust:

| source | kind | how |
|---|---|---|
| vendor model cards / tech reports | `vendor` | curated JSON, cited, dated |
| Artificial Analysis (`data/artificial_analysis.json`, currently unused) | `third_party` | needs licence check before redistribution |
| LMArena | `third_party` | Elo, not accuracy — separate unit, never compared to `percent` |
| community runs | `community` | `verified=false` until reproduced |

`unit` is part of the key. A 62% MMLU-Pro and a 1,290 Elo are not sortable
against each other, and the API must make that impossible rather than relying on
the UI to remember.

## 8. Recipes

A recipe is "what I ran, on what, and what I measured". Reuse the existing
`benchmark_cache.json` shape — `engineFlags` already has `commandSnippet`,
`tensorParallel`, `gpuLayers`, `kvCacheDtype`, `attentionBackend`, `flashAttn`,
`specDecoding`, `mtpEnabled`. Promote it; do not invent a second schema.

- `flags_hash` = sha256 of canonicalised JSON (sorted keys) of engine + flags +
  quant. This is the dedupe key and the reproduction key.
- `gpu_slug` from `gpus.json` `id` (phase 0). Same GPU = same slug, regardless of
  vendor driver string.
- `github_url` normalised to `https://github.com/<owner>/<repo>/blob/<sha>/<path>`.
  Commit-pinned so it does not rot. Host allowlist: `github.com` only. Do not
  fetch it server-side beyond a `HEAD` during moderation.
- Three counts shown, never stored as a score: `reproductions`, `ok`, `fail`.
  A recipe's trust is derived, so it cannot drift.
- "Achievements" are queries, not tables: fastest verified `tok_s_out` for a
  `(gpu_slug, variant_id)`, first verified recipe for a GPU, most reproduced.

## 9. TUI integration

| screen | change |
|---|---|
| Settings | `DATA  2026.11.07.1 · UP TO DATE   [u] UPDATE` (and `EMBEDDED 2026-06-12` as the fallback) |
| Settings | update runs as a `tea.Cmd`, shows progress, `esc` cancels, failure is a row state — never a crash |
| Detail | new `EVALS` block: score + `VENDOR`/`3P`/`COMMUNITY` badge + `source_url` |
| Benchmarks | add `r` → recipes for *this* GPU and model; `o` opens the GitHub URL |
| Home | optional "NEW THIS WEEK" later; not in phase 1 |
| anywhere | `--offline` / `CIRI_OFFLINE=1` suppresses all network calls |

Bubble Tea rules: no blocking I/O in `Update`; only `tea.Cmd`. The repo has zero
`net/http` today, so this is the first network surface — introduce it once, in
`internal/remote`, with an explicit `http.Client{Timeout: …}`.

## 10. Invariants

**I1 — The CLI never mutates its own binary or source tree.** Data lives in the
cache dir; the embed is read-only.

**I2 — The embedded baseline is never written to.** It is the offline floor.

**I3 — A pack version is immutable.** Fixes ship as a new `data_version`.

**I4 — Postgres is the write model, packs are the read model.** The CLI has no
DB credential and no DB code path.

**I5 — Nothing is activated before it verifies.** sha256 + `bytes` + gzip decode
+ JSON decode + `schema` + `min_cli_version`, then atomic rename, then state.

**I6 — `state.json` is written last, atomically.** A crash mid-update leaves the
old active version intact.

**I7 — The manifest is published last.** Packs first, manifest second, always.

**I8 — Every published row carries provenance.** `source_kind` non-null;
`source_url` required for `vendor` and `third_party`.

**I9 — Vendor, third-party, and community scores never share a ranking.**

**I10 — Valkey is never authoritative.** Flush it and the system is slower, not
wrong.

**I11 — Ingest is idempotent and watermark-resumable.** Re-running a page is safe;
a crash never double-counts or skips silently.

**I12 — One crawler, one publisher.** Enforced by Valkey locks, not by hope.

**I13 — Community text is sanitised before rendering.** `internal/tui/benchmarks.go:172`
renders `notes` raw today; a submitted note containing ANSI escapes can rewrite
the terminal. Strip control characters (< 0x20 except newline/tab) at ingest *and*
at render.

**I14 — User URLs are never fetched or executed by the client.** Displaying and
opening a browser link is the entire feature.

## 11. Security & licensing

- **Auth.** GitHub device flow for publishing. Token in the OS keyring where
  available; fallback `0600` file. Never in the repo, never in `argv`, never in a
  URL (same principle as `docs/DOWNLOADS.md` §7).
- **Rate limits.** Per IP on anonymous reads, per user on writes, per token on
  the device-auth endpoints. Valkey counters + `Retry-After`.
- **Moderation.** New recipes/evals land `pending`. Nothing reaches a pack until
  a human or a rule promotes it. `report` is a first-class write.
- **Input caps.** Body size, JSON depth, field lengths, rows per request. Reject
  unknown fields server-side (`DisallowUnknownFields`).
- **SSRF.** The server must not fetch arbitrary community URLs. GitHub `HEAD`
  only, host allowlisted.
- **Licensing / consent.** Existing benchmark rows came from you, friends, and
  online contributors. Before they are republished as a *dataset*, add
  `submitted_by` + `source_url` + an explicit consent checkbox on submission, and
  licence the database (ODbL or CC-BY-4.0 — decide, don't drift). Rows with no
  traceable contributor should stay out of the public pack (open question Q3).
- **Third-party data.** `artificial_analysis.json`'s free tier and the current
  scraped `benchmark_cache.json` need a redistribution check. If the answer is
  "no", the community pack replaces it rather than embedding both (§2).

## 12. Task breakdown

Each step builds and tests independently. Do not start *n+1* before *n* is green.

**Phase 0 — seam (no behaviour change)**
1. Add stable `id` to each `gpus.json` entry; derive `gpu_slug` helper.
2. `internal/wire`: `Manifest`, `Pack`, `Catalog`, `Eval`, `Recipe`. Tests for
   encode/decode + `min_cli_version` compare.
3. `internal/remote`: `Client.Manifest` / `FetchPack`. `httptest` tests: 200, 304,
   500, timeout, truncated body.
4. `internal/dataplane`: `Open`, `Snapshot`, `Activate`, `Active`. Tests for
   verify-fail, schema-mismatch, crash-between-rename-and-state.
5. `cmd/ciri/main.go`: read through `Snapshot`. Embedded baseline only. Behaviour
   must be identical; existing tests still pass unchanged.
6. Settings row + `u` key + `--offline`.
7. `ciri data update` subcommand sharing the same code path as the key binding.

**Phase 1 — catalog**
8. `cmd/ciri-server` skeleton: config, PG pool, Valkey client, `/healthz`.
9. Schema migration for `data_version`, `ingest_run`, `hf_repo`, `model`, `variant`.
10. `internal/ingest`: one page → raw upsert. Then pagination + watermark.
11. Parent resolution (§6) with tests on real payload fixtures (record JSON in
    `testdata/`, do not hit the network in CI).
12. `internal/publish`: build/verify/rename/manifest.
13. Worker loop: hourly ticker + Valkey lock.
14. Serve `/v1/manifest.json` + packs with ETag/Cache-Control.
15. Point the CLI at it; end-to-end: update → Explore shows 600 models with variants.

**Phase 2 — evals**
16. `benchmark` + `eval_result` tables; seed file for ~10 well-known benchmarks.
17. Eval pack builder + Detail screen `EVALS` block with source badges.
18. Curated vendor rows for the top ~30 models (with URLs and dates).
19. Artificial Analysis importer, **only after** the licence question is answered.

**Phase 3 — recipes**
20. `recipe` + `recipe_reproduction` + `report` tables.
21. Submission endpoint + validation + `flags_hash` + GitHub URL normalisation.
22. GitHub device flow + token storage.
23. Recipes pack + Benchmarks `r` tab; "same GPU" filter uses `gpu_slug`.
24. Derived achievements.
25. Moderation view (a Postgres query or a tiny admin page; no framework).

**Phase 4 — custom benchmarks**
26. Local runner (`ciri bench run`) + result submission.
27. Benchmark definition format (dataset URL + scorer + version hash).
28. Mark unverified until reproduced; nothing here is comparable across harness
    versions.

## 13. Test plan

Unit tests against `httptest` and fixtures. **No network in CI.**

| test | asserts |
|---|---|
| `TestManifestNotModified` | 304 path keeps the active pack and does not re-download |
| `TestPackChecksumMismatchQuarantines` | bad sha → old version still active (I5/I6) |
| `TestPackSchemaTooNew` | `min_cli_version` above binary → refusal with a clear message |
| `TestCrashBetweenRenameAndState` | simulate kill; next start recovers to a valid snapshot |
| `TestPartialWriteNeverActivates` | truncated tmp file cannot become active |
| `TestEmbeddedFallbackOffline` | `--offline` + empty cache → baseline loads |
| `TestParentResolution` | GGUF repo maps to parent via `base_model:quantized` |
| `TestFineTuneIsNotAQuant` | `base_model:finetune:` creates a model, not a variant |
| `TestUnknownQuantStaysUnknown` | no `Q4_K_M` default (§6) |
| `TestIngestWatermarkResume` | second run skips rows at/behind the watermark (I11) |
| `TestIngestIdempotent` | running the same page twice changes no row counts |
| `TestPublishManifestLast` | a failure mid-build leaves the old manifest serving |
| `TestVendorAndThirdPartyNotMixed` | one ranking request, one `source_kind` |
| `TestANSISanitisedInNotes` | `\x1b[2J` removed at ingest and render (I13) |
| `TestRateLimitReturnsRetryAfter` | 429 with `Retry-After` on the write path |
| `TestFlagsHashStable` | key-order-independent hash for dedupe |

**Acceptance (manual):** publish a new `data_version`, run `ciri data update`,
confirm Settings shows the new version and Explore reflects the new models.
Then flush Valkey and confirm reads are unaffected (I10).

**Adversarial:** hand-edit `state.json` to claim a newer version than the packs on
disk. The CLI must fall back to the previous/last-good version, not trust the
claim.

## 14. Review checklist

The reviewer will check, in order:

1. Does the CLI still start with no network and an empty cache? (D2, I2)
2. Is `state.json` written after the rename, atomically? (I6)
3. Is the manifest the last artifact written? (I7)
4. Are packs immutable, with fixes only via a new version? (I3)
5. Does any CLI code path import Postgres? (I4)
6. Is `source_kind` non-nullable and enforced in the pack builder? (I8, I9)
7. Is the 5,348 → ~600 grouping computed from HF metadata, not name regex? (§6)
8. Is `unknown` preserved instead of defaulting to `Q4_K_M`? (§6)
9. Is submitted text sanitised in both ingest and render? (I13)
10. Does `Update` ever block on I/O? (Bubble Tea rule, §9)
11. Is the ingest watermark resumable and the run idempotent? (I11)
12. Is Valkey absent from every read path? (I10)

## 15. Open questions

- **Q1 — Where does the server live?** `cmd/ciri-server` in this module is the
  recommendation (D6). If it lives in the website repo instead, `internal/wire`
  becomes a published module and both sides must pin it.
- **Q2 — How do we source vendor eval numbers?** Curated JSON is fine for 30
  models and painful at 300. Decide the review process before the table grows.
- **Q3 — Consent for existing rows.** The current benchmark data came from
  friends/contributors. Do we republish rows without an identity, and under which
  licence? This gates the public pack.
- **Q4 — Artificial Analysis licence.** Free tier redistribution is unconfirmed.
  Until it is answered, AA stays out of packs.
- **Q5 — zstd vs gzip.** gzip is stdlib and gets 226 KB. zstd would save maybe
  40 KB and add a dependency. Start with gzip; revisit only on measured need.
- **Q6 — Multi-GPU.** `predictor.go:30` already has a tech-debt note. Recipes are
  keyed on a single `gpu_slug`; a 2×4090 recipe needs `gpu_count` in the key.
  Decide before phase 3 or the dedupe key is wrong.
- **Q7 — Windows cache path and keyring.** Untested here. Keyring fallback must
  work before publishing ships.
- **Q8 — Pack retention.** How many old versions to keep on the server? Two is
  enough for rollback; more is disk.
