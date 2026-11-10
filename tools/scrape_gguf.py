#!/usr/bin/env python3
"""Standalone GGUF catalog scraper (stdlib only, no dependencies).

Independent from the Go binary. Scrapes the Hugging Face Hub for GGUF repos
and emits the per-model catalog the TUI needs (schema 4):

    {
      "schema": 4,
      "generated_at": "2026-11-07T...Z",
      "models": [
        {
          "id", "display", "params_total", "downloads", "likes",
          "type":         generative|vlm|diffusion|tts|asr|embedding|audio|other,
          "pipeline_tag": "text-generation",
          "arch":         llama.cpp arch string ("qwen35","qwen4exp","yue2",...),
          "moe": true|false|null, "experts": N|null, "experts_active": N|null,
          "n_layers": N|null, "n_layers_kv": N|null,
          "n_kv_heads": N|null, "head_dim": N|null,
          "kv_bytes_per_token": N|null,   (fp16/bf16 KV assumption, K+V)
          "ctx_train": N|null, "license": "apache-2.0"|null,
          "variants": [
            {"repo": "owner/repo", "publisher": "owner", "downloads": N, "likes": N,
             "files": [{"path","quant","bits","size","sha256"}]}
          ]
        }
      ]
    }

Purity rule: a model's variants gather quant files from every publisher, but
each file is attributed to the model it actually belongs to. cardData.base_model
lists (e.g. audio-cpp/audio.cpp-gguf packs 65 different models) are fuzzy-matched
per file path; files that match nothing are excluded rather than misfiled.

Fit fields: n_layers_kv counts only full-attention layers (hybrid attention via
{arch}.attention.layer_types or {arch}.full_attention_interval); head_dim comes
from {arch}.attention.key_length. kv_bytes_per_token = 2 * n_layers_kv *
n_kv_heads * head_dim * 2 (fp16 K+V), or for MLA archs the compressed cache:
n_layers * (kv_lora_rank + qk_rope_head_dim) * 2. Null when neither the family's
GGUF header nor config.json covered it.

Usage:
    python3 tools/scrape_gguf.py --max-repos 25 --out /tmp/smoke.json
    python3 tools/scrape_gguf.py --out data/gguf_catalog.json            # full
    python3 tools/scrape_gguf.py --repo unsloth/Llama-3.3-70B-Instruct-GGUF --out /tmp/one.json

Request budget per repo: 1 tree page-list; per family: 1 header range-read +
1 model-info GET. ~0.72 s/request (HF budget: 500 req / 300 s, measured).
Resumable: progress saved to <out>.state.json after every finished repo;
Ctrl-C saves state. Network: huggingface.co only. Auth: Bearer $HF_TOKEN.
"""

import argparse
import gzip
import http.client
import json
import os
import re
import signal
import socket
import ssl
import struct
import sys
import time
import urllib.error
import urllib.parse
from datetime import datetime, timezone

API = "https://huggingface.co/api/models"
UA = "ciri-gguf-scraper/2.0"
PACE = 0.72          # seconds between HF requests (~1.4 req/s)
HDR_RANGE = 131071   # 128 KB covers the KV metadata block of real GGUFs
CHECKPOINT_EVERY = 50  # flush the catalog every N repos (hard-kill safety)

# Hosts with a configured but unroutable IPv6 stall urllib (it walks AAAA
# entries first, each paying the full connect timeout). Order IPv4 first so
# dual-stack targets resolve onto the working family immediately.
_getaddrinfo = socket.getaddrinfo
def _ipv4_first(addrinfo):
    return sorted(addrinfo, key=lambda a: a[0] != socket.AF_INET)
socket.getaddrinfo = lambda *a, **k: _ipv4_first(_getaddrinfo(*a, **k))

LINK_RE = re.compile(r'<([^>]+)>\s*;\s*rel="([^"]+)"')

QUANT_BITS = {
    "IQ1_S": 1, "IQ1_M": 1, "IQ1": 1,
    "IQ2_XXS": 2, "IQ2_XS": 2, "IQ2_S": 2, "IQ2_M": 2, "IQ2": 2,
    "Q2_K": 2, "Q2_K_S": 2, "Q2_K_M": 2, "Q2_K_XL": 2,
    "Q2_0": 2, "Q2_1": 2, "Q2K": 2, "IQ2XXS": 2, "TQ1_0": 2, "TQ2_0": 2, "Q2": 2,
    "IQ3_XXS": 3, "IQ3_XS": 3, "IQ3_S": 3, "IQ3_M": 3, "IQ3": 3,
    "Q3_K": 3, "Q3_K_S": 3, "Q3_K_M": 3, "Q3_K_L": 3, "Q3_K_XL": 3, "Q3": 3,
    "TQ3_0": 3,
    "IQ4_XS": 4, "IQ4_NL": 4, "IQ4": 4, "Q4_K": 4, "Q4_0": 4, "Q4_1": 4,
    "Q4_K_S": 4, "Q4_K_M": 4, "Q4_K_XL": 4, "Q4": 4, "Q4_0_4_4": 4,
    "Q4_0_4_8": 4, "Q4_0_8_8": 4, "Q4_0_V2": 4, "INT4": 4,
    "MXFP4": 4, "NVFP4": 4, "FP4": 4, "DQ4_K": 4, "Q4K": 4,
    "Q5_0": 5, "Q5_1": 5, "Q5_K_S": 5, "Q5_K_M": 5, "Q5_K_XL": 5,
    "Q5": 5, "DQ5_K": 5,
    "Q6_K": 6, "Q6_K_M": 6, "Q6_K_L": 6, "Q6_K_XL": 6, "Q6": 6, "DQ6_K": 6,
    "Q8_0": 8, "Q8_1": 8, "Q8_K": 8, "DQ8_K": 8,
    "Q8_K_L": 8, "Q8_K_XL": 8, "FP8": 8, "Q8_0_V2": 8, "INT8": 8, "Q8": 8,
    "BF16": 16, "F16": 16, "FP16": 16, "F32": 32, "FP32": 32,
}
QUANT_RE = re.compile(
    r"(UD-[A-Z0-9_]+|DSPARK-[A-Z0-9_]+|BF16|FP16|F16|FP32|F32|FP8"
    r"|MXFP4|NVFP4|FP4|TQ1_0|TQ2_0|TQ3_0"
    r"|INT[18]"
    r"|DQ[1-8]_[A-Z0-9]+(?:_[A-Z0-9]+)*"
    r"|IQ[1-8]_[A-Z0-9]+(?:_[A-Z0-9]+)*"
    r"|Q[1-8]_[A-Z0-9]+(?:_[A-Z0-9]+)*"
    r"|(?<![A-Z0-9])IQ[1-8](?:XXS|XS|S|M|NL)(?![A-Z0-9])"
    r"|(?<![A-Z0-9])Q[1-8]K(?![A-Z0-9])"
    r"|(?<![A-Z0-9])IQ[1-8](?![A-Z0-9_])"
    r"|(?<![A-Z0-9])Q[1-8](?![A-Z0-9_]))"
)

# Pipeline-tag buckets for the TUI. First match wins; "generative" checked first.
TYPE_RULES = [
    ("diffusion", ("text-to-image", "image-to-image", "unconditional-image-generation",
                   "text-to-video", "image-to-video", "image-editing")),
    ("vlm", ("image-text-to-text", "image-text-to-video", "video-text-to-text",
             "any-to-any", "visual-question-answering", "visual-document-retrieval",
             "document-question-answering", "image-to-text")),
    ("tts", ("text-to-speech", "text-to-audio", "text-to-audio-video",
             "audio-to-audio", "audio-generation", "music-generation",
             "text-to-music", "audio")),
    ("asr", ("automatic-speech-recognition", "speech-translation",
             "speech-recognition")),
    ("embedding", ("feature-extraction", "sentence-similarity",
                   "image-feature-extraction")),
    ("audio", ("audio-classification", "voice-conversion", "speech-separation",
               "speech-diarization", "source-separation")),
]


def log(*a):
    print(*a, file=sys.stderr, flush=True)


def quant_from_filename(path):
    """Parse the quant token from a .gguf filename. Returns (quant, bits)."""
    base = path.rsplit("/", 1)[-1]
    if base.lower().endswith(".gguf"):
        base = base[: -len(".gguf")]
    # Scan the whole path, not just the basename: repos commonly split files
    # into quant-named directories (unsloth's "UD-IQ1_S/split.00001.gguf").
    # ms[-1] means a basename token still wins over a directory token.
    ms = QUANT_RE.findall(path.upper())
    q = ms[-1] if ms else "UNKNOWN"
    core = q
    for pre in ("UD-", "DSPARK-"):
        if core.startswith(pre):
            core = core[len(pre):]
            break
    while core not in QUANT_BITS and "_" in core:
        nxt = core.rsplit("_", 1)[0]
        if re.fullmatch(r"(?:[IQDBTF]{1,2})?[0-9]*", nxt):
            break
        core = nxt
    return q, QUANT_BITS.get(core, 0)


def classify_type(pipeline_tag, tags):
    p = (pipeline_tag or "").strip().lower()
    tset = {x.strip().lower() for x in (tags or []) if isinstance(x, str)}
    if p == "text-generation" or "conversational" in tset:
        return "generative"
    for name, want in TYPE_RULES:
        if p in want or (tset & set(want)):
            return name
    return "other"


def is_sha256(s):
    return bool(s) and len(s) == 64 and all(c in "0123456789abcdefABCDEF" for c in s)


def atomic_write(path, text):
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        f.write(text)
    os.replace(tmp, path)


class SigIntExit(Exception):
    pass


def _sigint(signum, frame):
    raise SigIntExit()


_next_tick = [0.0]
_TLS = ssl.create_default_context()
# One keep-alive connection per (scheme, host, port). The dominant cost of the
# old urllib path was a fresh TLS handshake for every request.
_http_pool = {}


def _conn_for(parts):
    scheme = parts.scheme or "https"
    host = parts.hostname
    if not host:
        raise ValueError(f"URL has no host: {parts.geturl()}")
    port = parts.port or (443 if scheme == "https" else 80)
    key = (scheme, host, port)
    conn = _http_pool.get(key)
    if conn is None:
        if scheme == "https":
            conn = http.client.HTTPSConnection(host, port, timeout=90, context=_TLS)
        else:
            conn = http.client.HTTPConnection(host, port, timeout=90)
        _http_pool[key] = conn
    return key, conn


def _drop_conn(key, conn):
    _http_pool.pop(key, None)
    try:
        conn.close()
    except Exception:
        pass


def _read_body(resp, range_end):
    """Read one response body. Returns (body, connection_is_dirty)."""
    if range_end is None:
        return resp.read(), False
    limit = range_end + 1
    body = resp.read(limit)
    # If the server ignored Range (200) or sent more than we asked for, the
    # rest of the body is still on the socket. Never drain a multi-GB file.
    dirty = False
    if resp.length is not None:
        dirty = resp.length > 0
    else:
        dirty = len(body) >= limit
    return body, dirty


def _request_once(url, headers, range_end):
    for _ in range(10):
        parts = urllib.parse.urlsplit(url)
        key, conn = _conn_for(parts)
        path = parts.path or "/"
        if parts.query:
            path += "?" + parts.query
        try:
            conn.request("GET", path, headers=headers)
            resp = conn.getresponse()
        except (http.client.RemoteDisconnected, http.client.BadStatusLine,
                http.client.CannotSendRequest, BrokenPipeError,
                ConnectionResetError):
            # A pooled keep-alive socket was closed by the peer. Reconnect
            # once immediately; the outer retry loop handles real failures.
            _drop_conn(key, conn)
            key, conn = _conn_for(parts)
            conn.request("GET", path, headers=headers)
            resp = conn.getresponse()

        status = resp.status
        location = resp.getheader("Location")
        if status in (301, 302, 303, 307, 308) and location:
            try:
                resp.read()
            except Exception:
                _drop_conn(key, conn)
            url = urllib.parse.urljoin(url, location)
            new = urllib.parse.urlsplit(url)
            if (new.scheme, new.hostname, new.port) != (parts.scheme, parts.hostname, parts.port):
                # Never forward the hub token to the CDN (same policy as Go).
                headers = {k: v for k, v in headers.items()
                           if k.lower() != "authorization"}
            continue

        body, dirty = _read_body(resp, range_end)
        if dirty:
            _drop_conn(key, conn)
        return status, body, resp.headers
    raise RuntimeError(f"too many redirects: {url}")


def http_get(url, extra_headers=None, range_end=None):
    now = time.monotonic()
    wait = _next_tick[0] - now
    if wait > 0:
        time.sleep(wait)
    _next_tick[0] = max(time.monotonic(), _next_tick[0]) + PACE
    headers = {
        "User-Agent": UA,
        "Accept-Encoding": "identity" if range_end is not None else "gzip",
    }
    token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGING_FACE_HUB_TOKEN")
    if token:
        headers["Authorization"] = "Bearer " + token
    if range_end is not None:
        headers["Range"] = f"bytes=0-{range_end}"
    if extra_headers:
        headers.update(extra_headers)
    wait = 0.0
    for attempt in range(7):
        if wait > 0:
            time.sleep(min(wait, 60.0))
            log(f"  retry {attempt + 1}/7 in {min(wait, 60.0):.0f}s")
        try:
            status, body, resp_headers = _request_once(url, headers, range_end)
        except (OSError, http.client.HTTPException, ssl.SSLError):
            wait = 5.0 * (2 ** attempt)
            continue
        if status == 429:
            ra = (resp_headers.get("Retry-After") or "").strip()
            if ra.replace(".", "").isdigit() and 0 < float(ra) <= 300:
                wait = float(ra)
            else:
                wait = 5.0 * (2 ** attempt)
            continue
        if status in (408, 425, 500, 502, 503, 504):
            wait = 5.0 * (2 ** attempt)
            continue
        if (resp_headers.get("Content-Encoding") or "").lower() == "gzip":
            try:
                body = gzip.decompress(body)
            except OSError:
                wait = 5.0 * (2 ** attempt)
                continue
        return status, body, resp_headers
    raise RuntimeError(f"GET failed after retries: {url}")


def api_json(url):
    """GET a URL and parse JSON; returns (status, obj|None)."""
    try:
        status, body, _ = http_get(url)
    except urllib.error.HTTPError as e:
        return e.code, None
    if status != 200:
        return status, None
    try:
        return status, json.loads(body.decode("utf-8"))
    except (ValueError, UnicodeDecodeError):
        return status, None


def next_link(headers):
    for raw in headers.get_all("Link") or []:
        for m in LINK_RE.finditer(raw):
            if m.group(2) == "next":
                return m.group(1)
    return None


def list_gguf(limit=1000, min_downloads=0, cap=0):
    url = API + "?filter=gguf&sort=downloads&direction=-1" + \
        "&expand[]=tags&expand[]=cardData&expand[]=safetensors&expand[]=gguf"
    if limit:
        url += f"&limit={limit}"
    entries, pages = [], 0
    while url and pages < 200:
        pages += 1
        status, body, headers = http_get(url)
        if status != 200:
            raise RuntimeError(f"list page {pages}: HTTP {status}")
        page = json.loads(body.decode("utf-8"))
        if isinstance(page, dict):
            page = page.get("models") or []
        kept = 0
        for e in page:
            if isinstance(e, dict) and e.get("id"):
                if min_downloads and (e.get("downloads") or 0) < min_downloads:
                    continue
                entries.append(e)
                kept += 1
        log(f"list page {pages}: +{kept} (total {len(entries)})")
        if cap and len(entries) >= cap:
            break
        url = next_link(headers)
    return entries


def tree_gguf(repo):
    """All .gguf files of a repo with exact size + sha256 (lfs.oid)."""
    files = []
    url = f"{API}/{urllib.parse.quote(repo, safe='/')}/tree/main?recursive=true&limit=1000"
    page_no = 0
    while url and page_no < 50:
        page_no += 1
        status, body, headers = http_get(url)
        if status != 200:
            raise RuntimeError(f"tree {repo}: HTTP {status}")
        items = json.loads(body.decode("utf-8"))
        if not isinstance(items, list):
            raise RuntimeError(f"tree {repo}: unexpected payload")
        for it in items:
            if not isinstance(it, dict) or it.get("type") != "file":
                continue
            p = it.get("path") or ""
            if not p.lower().endswith(".gguf"):
                continue
            size = int(it.get("size") or 0)
            sha = ""
            lfs = it.get("lfs") or {}
            if isinstance(lfs, dict):
                if lfs.get("size"):
                    size = int(lfs["size"])
                if is_sha256(lfs.get("oid") or ""):
                    sha = (lfs["oid"] or "").lower()
            q, bits = quant_from_filename(p)
            files.append({"path": p, "quant": q, "bits": bits, "size": size, "sha256": sha})
        url = next_link(headers)
    return files


# ---------------------------------------------------------------------------
# GGUF file-header parsing (Range GET on the resolve URL)
# ---------------------------------------------------------------------------

def parse_gguf_header(buf):
    """Parse the KV metadata section of a GGUF header. Partial buffers okay."""
    if len(buf) < 24 or buf[:4] != b"GGUF":
        return {}
    _, _, n_kv = struct.unpack_from("<IQQ", buf, 4)
    pos = 24

    def r_str(p):
        if p + 8 > len(buf):
            raise EOFError()
        (ln,) = struct.unpack_from("<Q", buf, p)
        p += 8
        if p + ln > len(buf):
            raise EOFError()
        return buf[p:p + ln].decode("utf-8", "replace"), p + ln

    SCALARS = {
        0: ("<B", 1), 1: ("<b", 1), 2: ("<H", 2), 3: ("<h", 2),
        4: ("<I", 4), 5: ("<i", 4), 6: ("<f", 4), 7: ("<?", 1),
        10: ("<Q", 8), 11: ("<q", 8), 12: ("<d", 8),
    }

    def r_val(t, p):
        if t in SCALARS:
            fmt, sz = SCALARS[t]
            if p + sz > len(buf):
                raise EOFError()
            return struct.unpack_from(fmt, buf, p)[0], p + sz
        if t == 8:
            return r_str(p)
        if t == 9:
            if p + 12 > len(buf):
                raise EOFError()
            et, cnt = struct.unpack_from("<IQ", buf, p)
            p += 12
            if et == 8:
                # small string arrays matter (attention.layer_types); vocab-sized
                # ones abort here — keys parsed before them are already stored
                if cnt > 1024:
                    raise EOFError()
                arr = []
                for _ in range(cnt):
                    s, p = r_str(p)
                    arr.append(s)
                return arr, p
            if et not in SCALARS:
                raise EOFError()
            sz = SCALARS[et][1]
            if p + sz * cnt > len(buf):
                return None, len(buf)   # truncated numeric array: done reading
            return [struct.unpack_from(SCALARS[et][0], buf, p + i * sz)[0]
                    for i in range(cnt)], p + sz * cnt
        raise EOFError()

    out = {}
    try:
        for _ in range(n_kv):
            k, pos = r_str(pos)
            if pos + 4 > len(buf):
                break
            (vt,) = struct.unpack_from("<I", buf, pos)
            pos += 4
            v, pos = r_val(vt, pos)
            if v is None:
                break
            out[k] = v
            if pos >= len(buf):
                break
    except EOFError:
        pass
    return out


BAD_HEADER_RE = re.compile(
    r"mmproj|imatrix|(?<![a-z])mtp|draft|projector|adapter|tokenizer|"
    r"control-?v?net|edges|vae|vocoder|(?<!\w)vision|guard|reward", re.I)

SPLIT_RE = re.compile(r"00001-of-\d+")


def header_candidates(files):
    """Order a family's files by likelihood their header carries arch params."""
    def rank(f):
        p = f["path"]
        return (
            BAD_HEADER_RE.search(p) is not None,   # clean model files first
            SPLIT_RE.search(p) is None,            # shard 1 of splits next
            f["quant"] == "UNKNOWN",               # parsed quant above unknown
            len(p),
        )
    return sorted(files, key=rank)


def fetch_header(repo, path):
    """Range-read + parse a .gguf header. Returns dict or None."""
    if not path:
        return None
    url = (f"https://huggingface.co/{urllib.parse.quote(repo, safe='/')}"
           f"/resolve/main/{urllib.parse.quote(path)}")
    try:
        status, buf, _ = http_get(url, range_end=HDR_RANGE)
    except urllib.error.HTTPError:
        return None
    if status not in (200, 206):
        return None
    return parse_gguf_header(buf[:HDR_RANGE + 1])


def as_int(v):
    if isinstance(v, bool) or not isinstance(v, int):
        return None
    return v


def kv_fit_from_header(kv):
    """Extract family fit fields from parsed GGUF header KV metadata."""
    out = {
        "arch": None, "moe": None, "experts": None, "experts_active": None,
        "n_layers": None, "n_layers_kv": None, "n_kv_heads": None,
        "head_dim": None, "kv_bytes_per_token": None, "kv_dtype": "fp16",
        "ctx_train": None,
    }
    arch = str(kv.get("general.architecture") or "").strip()
    if not arch or not re.fullmatch(r"[A-Za-z0-9_-]{1,40}", arch):
        return out

    def g(sfx):
        return kv.get(f"{arch}.{sfx}")

    out["arch"] = arch
    n_layers = as_int(g("block_count"))
    n_kv_heads = as_int(g("attention.head_count_kv"))
    key_len = as_int(g("attention.key_length"))
    val_len = as_int(g("attention.value_length"))
    head_dim = key_len or val_len
    n_full = n_layers
    lt = g("attention.layer_types")
    lt = lt if isinstance(lt, list) else None
    if lt:
        n_full = sum(1 for t in lt if isinstance(t, str) and t.startswith("full"))
    else:
        ivl = as_int(g("full_attention_interval"))
        if ivl and ivl > 1 and n_layers:
            n_full = (n_layers + ivl // 2) // ivl
    kv_lora = as_int(g("attention.kv_lora_rank"))
    rope = as_int(g("attention.qk_rope_head_dim"))
    if head_dim and n_kv_heads:
        out["head_dim"] = head_dim
        out["n_kv_heads"] = n_kv_heads
        out["n_layers"] = n_layers
        out["n_layers_kv"] = n_full if n_full is not None else n_layers
        if kv_lora:  # MLA: compressed shared KV (deepseek family)
            out["kv_bytes_per_token"] = n_layers * (kv_lora + (rope or 0)) * 2
        else:
            out["kv_bytes_per_token"] = 2 * (n_full or n_layers) * n_kv_heads * head_dim * 2
    out["ctx_train"] = as_int(g("context_length"))
    exp = as_int(g("expert_count"))
    act = as_int(g("expert_used_count"))
    if exp:
        out["moe"] = exp > 1
        out["experts"] = exp
        out["experts_active"] = act
    elif re.search(r"moe|experts?", arch):
        out["moe"] = True
    return out


# ---------------------------------------------------------------------------
# per-file family attribution
# ---------------------------------------------------------------------------

_CAMEL_RE = re.compile(r"([a-z0-9])([A-Z])")
_TOKEN_RE = re.compile(r"[a-z]+[0-9]*|[0-9]+[a-z]*")
_TAIL_RE = re.compile(r"^([a-z]{3,})(\d+)$")
_NOISE = {"gguf", "ggml", "model", "models", "file", "files", "the", "and"}


def norm_tokens(s):
    """Lowercase token set with camelCase boundaries and digit-tail splits."""
    s = _CAMEL_RE.sub(r"\1 \2", str(s))
    s = s.lower()
    s = re.sub(r"[^a-z0-9]+", " ", s)
    toks = []
    for t in _TOKEN_RE.findall(s):
        if t in _NOISE:
            continue
        m = _TAIL_RE.match(t)
        if m:
            toks.append(m.group(1))
            toks.append(m.group(2))
        else:
            toks.append(t)
    return frozenset(toks) if toks else frozenset()


def family_candidates(entry):
    """Candidate canonical model ids for a repo, most-trusted first."""
    card = entry.get("cardData") or {}
    bm = card.get("base_model")
    if isinstance(bm, str):
        bm = [bm]
    cands = []
    if isinstance(bm, list):
        cands.extend(x for x in bm if isinstance(x, str) and x.strip() and "/" in x)
    for tag in entry.get("tags") or []:
        if isinstance(tag, str) and tag.startswith("base_model:quantized:"):
            cands.append(tag[len("base_model:quantized:"):].strip())
    if not cands:
        rid = entry.get("id")
        if rid:
            cands = [rid]
    rid = entry.get("id")
    seen, out = set(), []
    for x in cands:
        if x not in seen:
            seen.add(x)
            out.append(x)
    # the repo as its own family is the last resort; real parents win
    if len(out) > 1 and rid in out:
        out = [x for x in out if x != rid]
    return out


def attribute_files(files, cands):
    """Group files by the canonical model each path most plausibly belongs to.

    Returns ({mid: [files]}, [unmatched_files]). Unmatched files are excluded
    from the catalog instead of misfiled (variant-purity rule).
    """
    scored = []
    for mid in cands:
        mt = norm_tokens(mid.rsplit("/", 1)[-1])
        if len(mt) >= 2:
            slug = re.sub(r"[^a-z0-9]", "", mid.rsplit("/", 1)[-1].lower())
            scored.append((mt, mid, slug))
    groups, leftovers = {}, []
    for f in files:
        pt = norm_tokens(f["path"])
        pslug = re.sub(r"[^a-z0-9]", "", f["path"].lower())
        best, best_key = None, None
        for mt, mid, slug in scored:
            inter = len(pt & mt)
            if slug and len(slug) >= 4 and slug in pslug:
                inter = max(inter, 2)
            need = 2 if len(mt) >= 3 else 1
            if inter < need:
                continue
            # canonical (shorter) names win equal-score ties
            key = (inter, -len(mt))
            if best_key is None or key > best_key:
                best, best_key = mid, key
        if best:
            groups.setdefault(best, []).append(f)
        else:
            leftovers.append(f)
    return groups, leftovers


def display_of(mid):
    name = (mid.rsplit("/", 1)[-1] or mid)
    name = re.sub(r"-GGUF$", "", name, flags=re.I)
    return re.sub(r"[-_]+", " ", name).strip()


# ---------------------------------------------------------------------------
# family enrichment (HF model info + GGUF header + config.json fallback)
# ---------------------------------------------------------------------------

CONFIG_LAYERS = ("num_hidden_layers", "n_layer")
CONFIG_KVHEADS = ("num_key_value_heads", "num_attention_heads", "n_head_kv", "n_head")
CONFIG_EXPERTS = ("num_local_experts", "n_routed_experts", "num_experts",
                  "num_routed_experts", "num_experts_total")
CONFIG_EXPERTS_ACTIVE = ("num_experts_per_tok", "num_active_experts",
                         "num_experts_used", "num_experts_per_token")
MLA_KEYS = ("kv_lora_rank",)


def cfg_find(cfg, names):
    """Depth-first search for the first present key in nested HF config dicts."""
    stack = [cfg]
    while stack:
        d = stack.pop()
        if not isinstance(d, dict):
            continue
        for n in names:
            v = d.get(n)
            if isinstance(v, int) and not isinstance(v, bool) and v > 0:
                return v
        stack.extend(v for v in d.values() if isinstance(v, dict))
    return None


def cfg_find_head_dim(cfg):
    stack = [(cfg, None)]
    while stack:
        d, scope = stack.pop()
        if not isinstance(d, dict):
            continue
        hd, hs, nh = d.get("head_dim"), d.get("hidden_size"), d.get("num_attention_heads")
        if isinstance(hd, int) and hd > 0:
            return hd
        if isinstance(hs, int) and hs > 0 and isinstance(nh, int) and nh > 0:
            return hs // nh
        stack.extend((v, scope) for v in d.values() if isinstance(v, dict))
    return None


def fill_from_config(m, cfg):
    n_layers = cfg_find(cfg, CONFIG_LAYERS)
    kv_heads = cfg_find(cfg, CONFIG_KVHEADS)
    head_dim = cfg_find_head_dim(cfg)
    experts = cfg_find(cfg, CONFIG_EXPERTS)
    active = cfg_find(cfg, CONFIG_EXPERTS_ACTIVE)
    if n_layers and not m.get("n_layers"):
        m["n_layers"] = n_layers
        m["n_layers_kv"] = m.get("n_layers_kv") or n_layers
    if kv_heads and not m.get("n_kv_heads"):
        m["n_kv_heads"] = kv_heads
    if head_dim and not m.get("head_dim"):
        m["head_dim"] = head_dim
    if experts:
        m["moe"] = experts > 1
        if not m.get("experts"):
            m["experts"] = experts
            m["experts_active"] = active
    if n_layers and kv_heads and head_dim and not m.get("kv_bytes_per_token"):
        m["kv_bytes_per_token"] = 2 * m["n_layers_kv"] * kv_heads * head_dim * 2


def fetch_config(mid):
    url = (f"https://huggingface.co/{urllib.parse.quote(mid, safe='/')}"
           f"/resolve/main/config.json")
    status, body, _ = http_get(url)
    if status != 200:
        return None
    try:
        return json.loads(body.decode("utf-8"))
    except (ValueError, UnicodeDecodeError):
        return None


def enrich_family(m, entry, repo, fam_files):
    """Fill canonical params/downloads/likes/type + fit fields for one family."""
    mid = m["id"]
    status, info = api_json(f"{API}/{urllib.parse.quote(mid, safe='/')}")
    card = (info or {}).get("cardData") or {}
    st = (info or {}).get("safetensors")
    if isinstance(st, dict) and isinstance(st.get("total"), int) and st["total"] > 0:
        m["params_total"] = st["total"]
    dl = (info or {}).get("downloads")
    if isinstance(dl, int) and dl > m["downloads"]:
        m["downloads"] = dl
    lk = (info or {}).get("likes")
    if isinstance(lk, int) and lk > m["likes"]:
        m["likes"] = lk
    pipeline = (info or {}).get("pipeline_tag") or card.get("pipeline_tag")
    if pipeline:
        m["pipeline_tag"] = pipeline
    lic = card.get("license")
    if isinstance(lic, str) and lic:
        m["license"] = lic
    m["type"] = classify_type(pipeline, card.get("tags") or entry.get("tags") or [])

    archev = None
    for f in header_candidates(fam_files)[:3]:
        kv = fetch_header(repo, f["path"])
        if not kv:
            continue
        archev = kv
        gname = kv.get("general.name")
        if isinstance(gname, str) and gname.strip():
            d = re.sub(r"[-_]+", " ", gname.strip())
            if d and d.lower() not in ("unknown",):
                m["display"] = d
        if not m.get("license"):
            lic = kv.get("general.license")
            if isinstance(lic, str) and lic:
                m["license"] = lic
        fit = kv_fit_from_header(kv)
        for k, v in fit.items():
            if v is not None and m.get(k) is None:
                m[k] = v
        if m.get("moe") is None:
            m["moe"] = False
        break
    if archev is None:
        cfg = None
        try:
            cfg = fetch_config(mid)
        except (urllib.error.HTTPError, RuntimeError):
            cfg = None
        if isinstance(cfg, dict):
            m["arch"] = m.get("arch") or None
            fill_from_config(m, cfg)
            if m.get("moe") is None:
                m["moe"] = bool(m.get("experts"))


def ensure_family(mid, entry, repo, fam_files, models):
    m = models.get(mid)
    if m is None:
        m = models[mid] = {
            "id": mid,
            "display": display_of(mid),
            "params_total": None, "downloads": 0, "likes": 0,
            "type": "other", "pipeline_tag": None, "arch": None,
            "moe": None, "experts": None, "experts_active": None,
            "n_layers": None, "n_layers_kv": None, "n_kv_heads": None,
            "head_dim": None, "kv_bytes_per_token": None, "kv_dtype": "fp16",
            "ctx_train": None, "license": None, "_enriched": False,
            "variants": [],
        }
    if not m.get("_enriched"):
        m["_enriched"] = True
        try:
            enrich_family(m, entry, repo, fam_files)
        except (SigIntExit, KeyboardInterrupt):
            raise
        except Exception as ex:
            log(f"  enrich {mid}: {ex}")
    return m


def process_repo(entry, rid, models):
    """Tree-scan a repo, attribute files to families, upsert model variants."""
    files = tree_gguf(rid) or []
    groups, leftovers = attribute_files(files, family_candidates(entry))
    dl = int(entry.get("downloads") or 0)
    lk = int(entry.get("likes") or 0)
    g = entry.get("gguf")
    gtotal = g.get("total") if isinstance(g, dict) else None
    out_mids = []
    for mid, gf in groups.items():
        m = ensure_family(mid, entry, rid, gf, models)
        if m.get("params_total") is None and mid == rid and isinstance(gtotal, int) and gtotal > 0:
            m["params_total"] = gtotal
        if dl > m["downloads"]:
            m["downloads"] = dl
        if lk > m["likes"]:
            m["likes"] = lk
        vr = {"repo": rid, "publisher": rid.split("/", 1)[0],
              "downloads": dl, "likes": lk, "files": gf}
        m["variants"] = [v for v in m["variants"] if v.get("repo") != rid] + [vr]
        out_mids.append(mid)
    unmatched_n = len(leftovers)
    if unmatched_n:
        log(f"  {rid}: {unmatched_n} unmatched files excluded")
    return {"id": rid, "models": out_mids, "files": len(files), "unmatched": unmatched_n}


def restore_catalog(out, done):
    """Reload models + variants from a schema-4 catalog of already-done repos."""
    if not os.path.exists(out):
        return {}, 0
    try:
        old = json.load(open(out))
    except Exception as e:
        log(f"could not reload existing catalog ({e}); starting fresh")
        return {}, 0
    if old.get("schema") != 4:
        return {}, 0
    models = {}
    restored = 0
    for m in old.get("models") or []:
        mid = m.get("id")
        if not mid or not isinstance(m, dict):
            continue
        m = dict(m)
        variants = [v for v in m.get("variants") or [] if isinstance(v, dict) and v.get("repo")]
        # repos fully done keep their variants; partial repos are reprocessed
        keep = [v for v in variants if v.get("repo") in done]
        m["variants"] = keep
        restored += len(keep)
        # re-enrich only families whose earlier run produced no fit fields
        m["_enriched"] = bool(m.get("arch") or m.get("kv_bytes_per_token") is not None)
        models[mid] = m
    return models, restored


def save_catalog(out, models):
    mlist = []
    for m in models.values():
        mm = {k: v for k, v in m.items() if not k.startswith("_")}
        mm["variants"] = sorted(mm.get("variants") or [],
                                key=lambda v: -(v.get("downloads") or 0))
        mlist.append(mm)
    mlist.sort(key=lambda x: -(x.get("downloads") or 0))
    doc = {
        "schema": 4,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "models": mlist,
    }
    atomic_write(out, json.dumps(doc, indent=1))
    nfiles = sum(len(v.get("files") or []) for m in mlist for v in m["variants"])
    return len(mlist), sum(len(m["variants"]) for m in mlist), nfiles


def save_state(state_path, done):
    atomic_write(state_path, json.dumps(sorted(done)))


def main():
    ap = argparse.ArgumentParser(description="Scrape GGUF catalog (stdlib only).")
    ap.add_argument("--out", default="data/gguf_catalog.json")
    ap.add_argument("--state", default="", help="resume state file (default <out>.state.json)")
    ap.add_argument("--max-repos", type=int, default=0, help="stop after N repos (0 = all)")
    ap.add_argument("--min-downloads", type=int, default=0)
    ap.add_argument("--limit", type=int, default=1000, help="page size for /api/models (max 1000)")
    ap.add_argument("--repo", action="append", default=[], help="scrape only these repo ids; skips list phase")
    args = ap.parse_args()

    state_path = args.state or (args.out + ".state.json")
    empty_path = args.out + ".empty.json"
    signal.signal(signal.SIGINT, _sigint)
    signal.signal(signal.SIGTERM, _sigint)

    done = set()
    if os.path.exists(state_path):
        try:
            done = set(json.load(open(state_path)))
            if done:
                log(f"resume: {len(done)} repos already done")
        except Exception as e:
            log(f"could not read state ({e}); starting fresh")

    empty = set()
    if os.path.exists(empty_path):
        try:
            empty = set(json.load(open(empty_path)))
            if empty:
                log(f"resume: {len(empty)} known-empty repos skipped")
        except Exception as e:
            log(f"could not read empty list ({e}); ignoring it")

    models, restored_v = restore_catalog(args.out, done)
    if models:
        log(f"restored {len(models)} models, {restored_v} variants from {args.out}")
    # State is written after every repo, catalog only at checkpoints. Drop
    # done-repos that are in neither the catalog nor the zero-variant list so
    # they are re-scraped.
    present = {v.get("repo") for m in models.values()
               for v in m.get("variants") or []}
    stale = done - present - empty
    if stale:
        log(f"{len(stale)} state repos missing from catalog/empty list; "
            f"reprocessing them")
        done -= stale

    try:
        if args.repo:
            entries = [{"id": r, "tags": [], "cardData": {}, "safetensors": None,
                        "gguf": None, "downloads": None, "likes": None} for r in args.repo]
            log(f"single-repo mode: {len(entries)}")
        else:
            log("phase 1: listing GGUF repos...")
            entries = list_gguf(limit=args.limit, min_downloads=args.min_downloads,
                                cap=args.max_repos)
            log(f"listed {len(entries)} repos")
            if args.max_repos:
                entries = entries[: args.max_repos]

        since_checkpoint = 0
        for i, e in enumerate(entries):
            rid = e.get("id") or ""
            if not rid or "/" not in rid or rid in done or rid in empty:
                continue
            try:
                info = process_repo(e, rid, models)
            except (KeyboardInterrupt, SigIntExit):
                raise
            except Exception as ex:
                log(f"[{i + 1}/{len(entries)}] {rid}: FAILED: {ex}")
                continue
            done.add(rid)
            if not info["models"]:
                empty.add(rid)
                atomic_write(empty_path, json.dumps(sorted(empty)))
            save_state(state_path, done)
            since_checkpoint += 1
            if since_checkpoint >= CHECKPOINT_EVERY:
                nm, nv, nf = save_catalog(args.out, models)
                log(f"checkpoint: {nm} models, {nv} variants, {nf} files")
                since_checkpoint = 0
            nmodels = sum(len(m["variants"]) for m in models.values())
            log(f"[{i + 1}/{len(entries)}] {rid}: {info['files']} files "
                f"-> {len(info['models'])} models, {info['unmatched']} excluded "
                f"(families {len(models)}, variants {nmodels})")

    except (KeyboardInterrupt, SigIntExit):
        log("interrupted: saving state")
        save_state(state_path, done)
        atomic_write(empty_path, json.dumps(sorted(empty)))
        nm, nv, nf = save_catalog(args.out, models)
        log(f"partial catalog saved: {nm} models, {nv} variants, {nf} files")
        sys.exit(130)

    save_state(state_path, done)
    atomic_write(empty_path, json.dumps(sorted(empty)))
    nm, nv, nf = save_catalog(args.out, models)
    log(f"wrote {args.out}: {nm} models, {nv} variants, {nf} files")


if __name__ == "__main__":
    main()
