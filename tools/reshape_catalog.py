#!/usr/bin/env python3
"""Reshape scrape schema 4 (canonical models -> repos -> files) into the
model -> quant -> source hierarchy the redesigned TUI consumes (schema 5).

The scrape stays cheap: this runs locally on the finished catalog and does
not touch the network.
"""

import argparse
import json
import os
import re
import sys

OCR_RE = re.compile(
    r"(?:^|[/_.-])(ocr|olmocr|dots?ocr|paddleocr|surya|doctr|docling|"
    r"document|layout|table|pdf)(?:$|[/_.-])",
    re.I,
)
IMATRIX_RE = re.compile(r"imatrix", re.I)
MMPROJ_RE = re.compile(r"mmproj", re.I)
AUX_RE = re.compile(r"(?:^|[/_.-])(vocoder|codec|audio|spk|speaker)(?:$|[/_.-])", re.I)


def display_from_id(mid):
    name = (mid or "").rsplit("/", 1)[-1]
    name = re.sub(r"-GGUF$", "", name, flags=re.I)
    # Repackager repos often embed the original owner: "01-ai_-_Yi-6B-Chat".
    name = re.sub(r"^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*?_-_", "", name)
    name = re.sub(r"[-_]+", " ", name)
    return name.strip() or mid


def human_params(n):
    if not isinstance(n, int) or n <= 0:
        return ""
    units = [(1_000_000_000_000, "T"), (1_000_000_000, "B"),
             (1_000_000, "M"), (1_000, "K")]
    for div, suffix in units:
        if n >= div:
            v = n / div
            return f"{v:.1f}".rstrip("0").rstrip(".") + suffix
    return str(n)


def classify_type(m):
    t = (m.get("type") or "other").strip().lower()
    text = f"{m.get('id', '')} {m.get('display', '')}"
    if t in ("vlm", "other") and OCR_RE.search(text):
        t = "ocr"
    return t


def classify_density(m):
    moe = m.get("moe")
    if moe is True:
        return "moe"
    if moe is False:
        return "dense"
    return "unknown"


def file_role(path, quant):
    lower = path.lower()
    if IMATRIX_RE.search(lower):
        return "imatrix"
    if MMPROJ_RE.search(lower):
        return "mmproj"
    if quant == "UNKNOWN" or AUX_RE.search(lower):
        return "aux"
    return "weights"


def source_from_variant(variant, model_quant_bits):
    repo = variant.get("repo") or ""
    publisher = variant.get("publisher") or repo.split("/", 1)[0]
    files = [f for f in variant.get("files") or [] if isinstance(f, dict)]
    if not repo or not files:
        return None

    quant_map = {}
    for f in files:
        path = f.get("path") or ""
        q = (f.get("quant") or "UNKNOWN").strip() or "UNKNOWN"
        size = int(f.get("size") or 0)
        role = file_role(path, q)
        if role == "weights":
            entry = quant_map.setdefault(q, {"weights": [], "aux": [], "other": []})
            entry["weights"].append(f)
        elif role in ("mmproj", "aux"):
            # Repo-level files needed to run a quant (vision projector, codec).
            # They are attached to every quant from this repo.
            entry = quant_map.setdefault("__aux__", {"weights": [], "aux": [], "other": []})
            entry["aux"].append(f)
        else:
            entry = quant_map.setdefault("__other__", {"weights": [], "aux": [], "other": []})
            entry["other"].append(f)

    aux_files = quant_map.get("__aux__", {}).get("aux", [])
    other_files = quant_map.get("__other__", {}).get("other", [])
    out = {}
    for q, groups in quant_map.items():
        if q.startswith("__"):
            continue
        weights = groups["weights"]
        if not weights:
            continue
        weights_bytes = sum(int(f.get("size") or 0) for f in weights)
        aux_bytes = sum(int(f.get("size") or 0) for f in aux_files)
        bits = max((int(f.get("bits") or 0) for f in weights), default=model_quant_bits.get(q, 0))
        out[q] = {
            "repo": repo,
            "publisher": publisher,
            "downloads": int(variant.get("downloads") or 0),
            "likes": int(variant.get("likes") or 0),
            "weights_bytes": weights_bytes,
            "download_bytes": weights_bytes + aux_bytes,
            "file_count": len(weights),
            "shards": len(weights),
            "bits": bits,
            "files": weights,
            "aux_files": aux_files,
            "other_files": other_files,
        }
    return out



QUANT_OWNERS = {
    "richarderkhov", "bartowski", "unsloth", "mradermacher",
    "lmstudio-community", "thebloke", "ggml-org", "second-state",
    "leliuga", "compendiumlabs", "maziyarpanahi", "quantfactory",
    "tensorblock", "prunaai", "33grmoss", "telegraphista", "modelzone",
    "gpustack",
}
QUANT_TOKEN_RE = re.compile(r"(q[2-8]|iq[1-4]|nvfp4|mxfp4|fp4|awq|gptq|int[248]|bf16|f16|fp16|fp32)")
NOISE_RE = re.compile(
    r"gguf|ggml|i1|imatrix|static|dynamic|quant|quants|"
    r"nvfp4|mxfp4|fp4|awq|gptq|int[248]|bf16|f16|fp16|fp32|"
    r"q[2-8](?:[a-z0-9]+)?|iq[1-4](?:[a-z0-9]+)?|smashed"
)
TEST_RE = re.compile(
    r"(test-model|test model|test_model|tiny_random|stories15m|"
    r"tinylaya-for-testing|dpotrain_testver|testmodelrouter)"
)


def norm_key(s):
    s = (s or "").lower()
    s = re.sub(r"-gguf$", "", s)
    return re.sub(r"[^a-z0-9]+", "", s)


def owner_of(model):
    return (model.get("provider") or (model.get("id") or "").split("/", 1)[0]).lower()


def is_test_model(model):
    hay = ((model.get("id") or "") + " " + (model.get("display") or "")).lower()
    return bool(TEST_RE.search(hay))


def allowed_remainder(rem, base):
    if not rem:
        return True
    cand_owner = norm_key((base.get("id") or "").split("/", 1)[0])
    if rem in (cand_owner, cand_owner + "gguf"):
        return True
    return NOISE_RE.sub("", rem) == ""


def merge_quant_sources(base_q, extra_q):
    known = {s.get("repo") for s in base_q.get("sources") or []}
    for src in extra_q.get("sources") or []:
        if src.get("repo") not in known:
            base_q["sources"].append(src)
            known.add(src.get("repo"))
    sources = base_q.get("sources") or []
    sources.sort(key=lambda s: (-int(s.get("downloads") or 0),
                                -int(s.get("likes") or 0),
                                -int(s.get("file_count") or 0),
                                (s.get("repo") or "").lower()))
    for i, src in enumerate(sources):
        src["preferred"] = i == 0
    if sources:
        pref = sources[0]
        base_q["weights_bytes"] = pref.get("weights_bytes", 0)
        base_q["download_bytes"] = pref.get("download_bytes", 0)
        base_q["file_count"] = pref.get("file_count", 0)
        base_q["shards"] = pref.get("shards", 0)
        base_q["bits"] = pref.get("bits", base_q.get("bits", 0))
    base_q["min_weights_bytes"] = min((s.get("weights_bytes", 0) for s in sources), default=0)
    base_q["max_weights_bytes"] = max((s.get("weights_bytes", 0) for s in sources), default=0)
    return base_q


def merge_repacks(models):
    bases = [m for m in models if owner_of(m) not in QUANT_OWNERS]
    merged = set()
    for m in models:
        if owner_of(m) in QUANT_OWNERS:
            dn = norm_key((m.get("id") or "").split("/", 1)[-1])
            disp = ((m.get("display") or "") + " " + (m.get("id") or "")).lower()
            best = None
            best_key = None
            for b in bases:
                if b is m:
                    continue
                bn = norm_key((b.get("id") or "").split("/", 1)[-1])
                if not bn or bn not in dn:
                    continue
                if m.get("arch") and b.get("arch") and m["arch"] != b["arch"]:
                    continue
                if m.get("params_total") and b.get("params_total") and m["params_total"] != b["params_total"]:
                    if not (QUANT_TOKEN_RE.search(disp) and dn.startswith(bn)):
                        continue
                if not b.get("params_total"):
                    continue
                rem = dn.replace(bn, "", 1)
                if not allowed_remainder(rem, b):
                    continue
                key = (len(bn), int(b.get("downloads") or 0), -len(b.get("id") or ""))
                if best_key is None or key > best_key:
                    best, best_key = b, key
            if best is None:
                continue
            by_label = {q.get("label"): q for q in best.get("quants") or []}
            for q in m.get("quants") or []:
                label = q.get("label")
                if label in by_label:
                    merge_quant_sources(by_label[label], q)
                else:
                    best.setdefault("quants", []).append(q)
            best["quants"].sort(key=lambda q: (-int(q.get("bits") or 0), q.get("label") or ""))
            best["downloads"] = max(int(best.get("downloads") or 0), int(m.get("downloads") or 0))
            best["likes"] = max(int(best.get("likes") or 0), int(m.get("likes") or 0))
            merged.add(id(m))
    return [m for m in models if id(m) not in merged]


def reshape(src, out_path):
    with open(src, "r") as f:
        doc = json.load(f)
    if doc.get("schema") != 4:
        raise SystemExit(f"{src}: expected schema 4, got {doc.get('schema')!r}")

    models = []
    for m in doc.get("models") or []:
        if not isinstance(m, dict):
            continue
        quants = {}
        for variant in m.get("variants") or []:
            if not isinstance(variant, dict):
                continue
            qmap = source_from_variant(variant, {})
            if not qmap:
                continue
            for q, src in qmap.items():
                quants.setdefault(q, []).append(src)

        if not quants:
            continue

        quant_list = []
        for label, sources in quants.items():
            sources.sort(key=lambda s: (-s["downloads"], -s["likes"], -s["file_count"], s["repo"].lower()))
            preferred = sources[0]
            for i, s in enumerate(sources):
                s["preferred"] = i == 0
            quant_list.append({
                "label": label,
                "bits": preferred["bits"],
                "weights_bytes": preferred["weights_bytes"],
                "download_bytes": preferred["download_bytes"],
                "min_weights_bytes": min(s["weights_bytes"] for s in sources),
                "max_weights_bytes": max(s["weights_bytes"] for s in sources),
                "file_count": preferred["file_count"],
                "shards": preferred["shards"],
                "sources": sources,
            })
        # Highest quality first; unknown bits last.
        quant_list.sort(key=lambda q: (-q["bits"], q["label"]))

        params = m.get("params_total")
        model = {
            "id": m.get("id") or "",
            "display": display_from_id(m.get("id") or m.get("display") or ""),
            "provider": (m.get("id") or "").split("/", 1)[0],
            "params_total": params if isinstance(params, int) else None,
            "parameter_count": human_params(params),
            "type": classify_type(m),
            "density": classify_density(m),
            "pipeline_tag": m.get("pipeline_tag"),
            "arch": m.get("arch"),
            "moe": m.get("moe"),
            "experts": m.get("experts"),
            "experts_active": m.get("experts_active"),
            "n_layers": m.get("n_layers"),
            "n_layers_kv": m.get("n_layers_kv"),
            "n_kv_heads": m.get("n_kv_heads"),
            "head_dim": m.get("head_dim"),
            "kv_bytes_per_token": m.get("kv_bytes_per_token"),
            "kv_dtype": m.get("kv_dtype"),
            "ctx_train": m.get("ctx_train"),
            "license": m.get("license"),
            "downloads": int(m.get("downloads") or 0),
            "likes": int(m.get("likes") or 0),
            "quants": quant_list,
        }
        models.append(model)

    models = [m for m in models if not is_test_model(m)]
    models = merge_repacks(models)
    models.sort(key=lambda x: (-x["downloads"], x["display"].lower()))
    out = {
        "schema": 5,
        "generated_at": doc.get("generated_at"),
        "models": models,
    }
    tmp = out_path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(out, f, separators=(",", ":"))
    os.replace(tmp, out_path)
    nq = sum(len(m["quants"]) for m in models)
    ns = sum(len(q["sources"]) for m in models for q in m["quants"])
    nf = sum(len(s["files"]) for m in models for q in m["quants"] for s in q["sources"])
    print(f"wrote {out_path}: {len(models)} models, {nq} quants, {ns} sources, {nf} weight files, "
          f"{os.path.getsize(out_path)} bytes", file=sys.stderr)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--source", default="data/gguf_catalog.json")
    ap.add_argument("--out", default="data/catalog_v2.json")
    args = ap.parse_args()
    reshape(args.source, args.out)


if __name__ == "__main__":
    main()
