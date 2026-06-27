# CIRI — Can I Run It? — System Documentation

> **Note:** The canonical name is `ciri`. The directory is `DOCUMENTAION.md` (historical typo retained).

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Data Files & Relationships](#2-data-files--relationships)
3. [GPU Database Loading & Normalization](#3-gpu-database-loading--normalization)
4. [Hardware Detection Flow](#4-hardware-detection-flow)
5. [GPU Matching Strategies](#5-gpu-matching-strategies)
6. [Model Catalog & Categorization](#6-model-catalog--categorization)
7. [Prediction Engine](#7-prediction-engine)
8. [Speed Estimation](#8-speed-estimation)
9. [TUI Components](#9-tui-components)
10. [Function Reference](#10-function-reference)

---

## 1. Architecture Overview

![Overall System Flow](Over_All_System_FLow.png)

`ciri` is a terminal-based tool that answers: **which open-source LLMs can run on my machine, and how fast?**

The system is organised into four packages:

 | Package | Path | Responsibility |
 |---------|------|----------------|
 | `main` | `cmd/ciri/` | Entry point, dependency wiring |
 | `hardware` | `internal/hardware/` | GPU/CPU/RAM detection, GPU DB loading, GPU name matching |
 | `model` | `internal/model/` | HF model catalog loading, category assignment |
 | `predictor` | `internal/predictor/` | VRAM fit checking, speed estimation, prediction orchestration |
 | `tui` | `internal/tui/` | Bubble Tea terminal UI (7 screens) |

### Startup sequence (`cmd/ciri/main.go`)

```
main()
  ├── Resolve data directory
  ├── LoadGPUDB("gpus.json")           → []hardware.GPU
  ├── GetSpecs(gpuDB)                  → hardware.DetectionResult
  │     ├── Specs.DetectCPU()          → CPU model, cores
  │     ├── Specs.DetectRAM()          → RAM total/avail (bytes + GB)
  │     ├── Specs.DetectOllamaCpp()    → ollama/llama.cpp on PATH?
  │     └── Specs.DetectGPU(gpuDB)     → GPU matching cascade
  ├── LoadCatalog("hf_models.json")    → []model.Model
  ├── LoadBenchmarks("benchmarks.json")→ *predictor.BenchmarkDB
  ├── NewPredictor(gpu, RAM, models, db) → *predictor.Predictor
  └── NewApp(specs, gpu, models, pred, db, version) → tea.Program (TUI)
```

---

## 2. Data Files & Relationships

![Data Files Relationships](DataFiles_Relations.png)

Three JSON data files in the `data/` directory are loaded at startup:

| File | Format | Loader | Purpose |
|------|--------|--------|---------|
| `gpus.json` | `[]gpuJSON` | `hardware.LoadGPUDB()` | GPU specs: VRAM, bandwidth, TFLOPS, PCI IDs, architecture, aliases |
| `hf_models.json` | `[]Model` | `model.LoadCatalog()` | 1000+ HF models with params, quant, RAM/VRAM reqs, categories |
| `benchmark_cache.json` | `benchmarkCacheFile` | `predictor.LoadBenchmarks()` | Real-world tok/s measurements indexed by GPU + model |

### Data flow

```
GPUs.csv ──┐
           ├──► merge_gpus.py ──► gpus.json ──► LoadGPUDB() ──► []GPU
pci.ids  ──┘
                                   
hf_models.json ────────────────────► LoadCatalog() ──► []Model (with Categories)

benchmark_cache.json ──────────────► LoadBenchmarks() ──► BenchmarkDB
                                        ├── byNameHfID: "gpuName|hfId" → []Row
                                        └── byArchHfID: "arch|hfId"    → []Row
```

---

## 3. GPU Database Loading & Normalization

![GPU Loading & Normalization Flow](GPU_Loading_Normalization_Flow.png)

### `LoadGPUDB()` — `internal/hardware/detection.go:194`

**Callers:** `cmd/ciri/main.go:29`, `detection_test.go:47,97,108,141,181`

Reads `gpus.json` and converts each `gpuJSON` entry into an internal `GPU` struct:

1. **Identity:** assigns a sequential `ID`, copies `Name`
2. **Normalization:** calls `NormalizeGPUName(r.Name)` to produce `CanonicalName`
3. **PCI IDs:** collects `VendorID`, deduplicates `DeviceIDs` from `pci_device_id` and `pci_device_ids`
4. **Laptop detection:** flags `IsLaptop` if name contains "laptop"/"mobile", or PCI variant key is "mobile"/"max_q"
5. **Aliases:** calls `deriveAliases(r.Name)` to generate vendor-stripped short names
6. **Specs:** copies `VRAMGB`, `Bandwidth`, `TFLOPS`, `Architecture` when present

### `NormalizeGPUName()` — `internal/hardware/normalizer.go:32`

**Callers:** `detection.go:210`, `matcher_ghw.go:27`, `matcher_vendor.go:35,140`, tests

Transforms raw marketing names into canonical searchable form:

```
"NVIDIA GeForce RTX 4090"              → "rtx 4090"
"AMD Radeon (TM) RX 7900 XTX"         → "rx 7900 xtx"
"Intel(R) Arc(TM) A770 Graphics"      → "arc a770"
"NVIDIA GeForce RTX 3080 Laptop GPU"  → "rtx 3080 laptop"
```

Steps: lowercase → strip driver versions → strip vendor prefixes → remove `(tm)`/`(r)` → remove "graphics"/"gpu" → replace "mobile" with "laptop" → remove special chars → collapse whitespace.

### `deriveAliases()` — `internal/hardware/detection.go:263`

Strips vendor prefixes (e.g. "nvidia geforce rtx ", "amd radeon rx ") to produce alias strings for broader matching. Only strips the longest matching prefix.

---

## 4. Hardware Detection Flow

![Hardware Detection Flow](HardWareDetectionFlow.png)

### `GetSpecs()` — `internal/hardware/detection.go:75`

**Callers:** `cmd/ciri/main.go:36`

Entry point for all hardware detection. Orchestrates four steps:

```
GetSpecs(gpuDB)
  ├── Specs.DetectCPU()       → CpuModel, CpuCores
  ├── Specs.DetectRAM()       → RamTotalGB, RamAvailGB, RamTotalBytes, RamAvailBytes
  ├── Specs.DetectOllamaCpp() → HasOllama, HasLlamaCPP
  └── Specs.DetectGPU(gpuDB)  → GPU matching cascade
```

### `DetectCPU()` — `detection.go:95`

Uses `ghw.CPU()` to get model name and total core count. Falls back to "Unknown" on permission errors.

### `DetectRAM()` — `detection.go:110`

Uses `ghw.Memory()` to get total and usable physical bytes. Stores both float64 (GB for display) and uint64 (bytes for offloading math).

### `DetectOllamaCpp()` — `detection.go:125`

Checks PATH for `ollama`, `llama.cpp`, `llama-cli`, `llama-server` via `execLookPath()`.

---

## 5. GPU Matching Strategies

![GPU Detection Diagram](DetectingGPUDiagram.png)

The heart of the system: a three-strategy cascade with confidence scoring.

![Matching Flow](MatchingFlow.png)

### `DetectGPU()` — `internal/hardware/detection.go:135`

**Callers:** `detection.go:87`, `detection_test.go:407,430`

Tries three matchers in order. If any reaches **≥0.95 confidence**, returns immediately. Otherwise keeps the best guess.

```go
strategies := []GPUMatcher{
    &PCIMatcher{},       // highest confidence (0.94-0.98)
    &VendorAPIMatcher{}, // medium confidence  (0.80-0.95)
    &GHWFuzzyMatcher{},  // lowest confidence  (0.30-0.90)
}
```

### 5.1 PCIMatcher — `internal/hardware/matcher_pci.go:9`

**Callers:** `detection.go:147` (via interface)

Uses PCI vendor/device IDs for exact hardware match.

```
Detect()
  ├── detectPCI(ctx)              → platform-specific PCI scan
  │     ├── Linux:   /sys/class/drm/card*/device (vendor + device files)
  │     ├── macOS:   returns nil (no sysfs)
  │     └── Windows: PowerShell Get-PnpDevice / wmic
  ├── findGPUsByPCI(db, vendorID, deviceID)  → []*GPU (may be multiple variants)
  ├── detectVRAM(ctx, pci)                   → float64 GiB
  │     ├── Linux NVIDIA:  nvidia-smi
  │     ├── Linux AMD:     sysfs mem_info_vram_total
  │     └── Windows NVIDIA: nvidia-smi
  └── pickBestPCIMatch(matches, vram) → *GPU
        ├── VRAM detected? → pick closest VRAM match
        └── No VRAM?       → prefer desktop over laptop
```

Confidence: `0.98` (single match), `0.96` (multi match + VRAM tiebreak), `0.94` (ambiguous).

### 5.2 VendorAPIMatcher — `internal/hardware/matcher_vendor.go:12`

**Callers:** `detection.go:147` (via interface)

Queries vendor CLI tools for the GPU marketing name.

```
Detect()
  ├── detectPCI(ctx)              → PCIInfo (vendor ID)
  ├── detectVendorName(ctx, pci)  → string
  │     ├── NVIDIA: nvidiaSMIQuery() → nvidia-smi --query-gpu=name
  │     ├── AMD:    rocmSMIQuery()   → rocm-smi --showproductname --json
  │     └── macOS:  system_profiler SPDisplaysDataType → "Chipset Model"
  └── resolveByName(db, name, 0.95)
```

`resolveByName()` tries four match types with decreasing confidence:

| Step | Lookup | Confidence |
|------|--------|------------|
| 1 | `findGPUByName()` — exact marketing name | baseConf (0.95) |
| 2 | `findGPUByAlias()` — vendor-stripped alias | baseConf - 0.03 |
| 3 | `findGPUByCanonicalName()` — normalized name | baseConf - 0.05 |
| 4 | `fuzzyFindGPUs()` — substring + token overlap | baseConf - 0.08 to -0.15 |

### 5.3 GHWFuzzyMatcher — `internal/hardware/matcher_ghw.go:10`

**Callers:** `detection.go:147` (via interface)

Lowest-confidence strategy. Uses the `ghw` library directly.

```
Detect()
  ├── detectRawGPUName() → string (filters iGPUs: Intel HD/UHD/Iris, non-RX Radeon)
  ├── findGPUByName()    → 0.90
  ├── findGPUByAlias()   → 0.85
  ├── findGPUByCanonicalName() → 0.80
  ├── fuzzyFindGPUs() + tokenOverlapScore() → 0.50-0.70
  └── bare GPU with detected name → 0.30 (always returns something)
```

### Platform-specific detection

| Function | File | Line | Linux | macOS | Windows |
|----------|------|------|-------|-------|---------|
| `detectPCI` | `detection_linux.go:21` / `_darwin.go:15` / `tools_windows.go:15` | sysfs scan | nil | PnP/WMI |
| `detectVRAM` | `detection_linux.go:81` / `_darwin.go:20` / `tools_windows.go:55` | nvidia-smi/sysfs | 0 | nvidia-smi |
| `detectVendorName` | `detection_linux.go:113` / `_darwin.go:25` | nvidia-smi/rocm-smi | system_profiler | — |
| `detectRawGPUName` | `detection_linux.go:166` / `_darwin.go:50` | ghw (iGPU filter) | ghw | — |
| `execWithTimeout` | `tools_unix.go:12` / `tools_windows.go:75` | context+exec | — | context+exec |
| `execLookPath` | `tools_unix.go:19` | exec.LookPath | — | — |

---

## 6. Model Catalog & Categorization

![Model Categorization Logic](Model_Categorization_Logic.png)

### `LoadCatalog()` — `internal/model/catalog.go:35`

**Callers:** `cmd/ciri/main.go:44`, tests

Reads `hf_models.json`, unmarshals into `[]Model`, and calls `Categorize()` on each.

### `Categorize()` — `internal/model/category.go:30`

**Callers:** `catalog.go:47`, tests

Classifies models into categories based on `UseCase`, `Capabilities`, and `PipelineTag`:

```
Categorize(m)
  ├── UseCase contains "code"                   → CategoryCoding
  ├── UseCase contains "chat"/"instruction"      → CategoryChat
  ├── hasCapability("vision") / pipeline match   → CategoryVision
  ├── pipeline == "translation"/"asr"            → CategoryTranslation
  └── fallback (no match)                       → CategoryGeneral
```

A model can belong to **multiple** categories (e.g. vision-capable chat model).

### Category constants

| Category | Value | Example models |
|----------|-------|----------------|
| `CategoryCoding` | "Coding" | CodeLlama, DeepSeek-Coder |
| `CategoryChat` | "Chat" | Llama-3, Mistral |
| `CategoryGeneral` | "General" | Text-generation fallback |
| `CategoryVision` | "Vision" | LLaVA, llava-v1.6 |
| `CategoryTranslation` | "Translation" | NLLB, Whisper |

![Classification Tree](Classification_Tree.png)

---

## 7. Prediction Engine

![Prediction Engine](Prediction_Engine.png)

### `Predictor` — `internal/predictor/predictor.go:13`

The orchestrator that ties hardware, models, and benchmarks together.

### `NewPredictor()` — `predictor.go:30`

**Callers:** `cmd/ciri/main.go:57`, tests

Creates a `Predictor` bound to the detected GPU, available system RAM, model catalog, and benchmark database. Automatically detects Apple Silicon by checking GPU name/architecture/vendor ID (`106b`).

### `Predict()` — `predictor.go:47`

**Callers:** `predictor.go:54,83` (via PredictAll/Predict), tests

The main prediction loop:

```
Predict(category)
  for each model in catalog:
    if model not in category → skip
    fit := CheckFit(model, gpu, sysRAM, isApple)
    if fit == TooHeavy → skip (not shown to user)
    tok/s, confidence := EstimateSpeed(model, gpu, benchmarks)
    append to results
  sort: Recommended first (by tok/s desc), then Advanced
  return results
```

### `CountByCategory()` — `predictor.go:83`

**Callers:** internal to `predictor` package, tests

Counts fitting models per category. Used by `AllCategories` but no longer by the TUI directly — the home screen uses a fixed 4-item menu instead.

### `CheckFit()` — `internal/predictor/vram.go:36`

**Callers:** `predictor.go:54,83,120` (via PredictAll, Predict, CountByCategory), tests

Determines whether a model fits:

| Condition | Result |
|-----------|--------|
| Apple Silicon: `ModelVRAMRequirement(m) × 1.1 ≤ (sysRAM - 4GB)` | Recommended |
| Apple Silicon: otherwise | TooHeavy |
| dGPU: `ModelVRAMRequirement(m) × 1.1 ≤ GPU.VRAMGB` | Recommended |
| dGPU: `MinRAMGB ≤ sysRAMAvail` | Advanced (spills to RAM) |
| dGPU: otherwise | TooHeavy |

`ModelVRAMRequirement(m)` returns `max(MinVRAMGB, weightSize)` where `weightSize` is the model's actual on-disk weight footprint (`parameters_raw / 1e9 × BytesPerParam(quantization)`). This prevents the catalog's curated `min_vram_gb` from understating the VRAM needed.

---

## 8. Speed Estimation

![Speed Estimation Tree](Speed_Estimation_Tree.png)

### `EstimateSpeed()` — `internal/predictor/estimate.go:263`

**Callers:** `predictor.go:61`, tests

Three-tier cascade returning `(tokPerSec, confidenceLabel)`:

```
Tier A: Exact benchmark match
  ├── lookupMedian(byNameHfID, canonical|hfID)  → tok/s | "Benchmark"
  └── lookupMedian(byNameHfID, gpuName|hfID)    → tok/s | "Benchmark"

Tier B: Architecture-family scaling
  └── archScaledEstimate(arch, hfID, TFLOPS, db) → median tok/s | "Estimate"
        └── applySpillPenalty(scaled, gpu, model) ← ×0.2 if model > VRAM

Tier C: Roofline heuristic
  ├── memoryBound = (Bandwidth × 0.75) / modelSizeGB
  ├── computeBound = (TFLOPS × 1e12 × 0.20) / (2 × params)
  └── tok/s = min(memoryBound, computeBound) | "Heuristic"
        └── ×0.2 if model > VRAM
```

### Key constants

| Constant | Value | Meaning |
|----------|-------|---------|
| `modelFLOPUtilization` | 0.20 | Decode FLOP utilisation |
| `flopsPerParamPerToken` | 2.0 | One multiply-add per param per token |
| `vramBufferFactor` | 1.1 | 10% VRAM headroom |
| `appleOSOverhead` | 4.0 GB | macOS unified memory reserved by OS |

### `GetMemoryEfficiency()` — `estimate.go:195`

Resolves bandwidth utilisation dynamically from the GPU architecture string (e.g.
`"ada lovelace"` → 0.80, `"pascal"` → 0.50, `"apple m4"` → 0.75). Falls back
to 0.60 for unrecognised architectures, or 0.45 for system RAM. Replaces the
old hardcoded `memoryEfficiency` constant.

### `BytesPerParam()` — `estimate.go:331`

Maps quantization tags to bytes-per-parameter (e.g. `Q4_K_M` → 0.625, `FP16` → 2.0). Defaults to 2.0 for unknown quants.

### `lookupMedian()` — `estimate.go:362`

Looks up benchmark rows by composite key and returns median tok/s.

### `archScaledEstimate()` — `estimate.go:380`

Returns median benchmark tok/s for an architecture family (e.g. "ada lovelace", "rdna 3").

### `median()` / `sortFloat64()` — `estimate.go:409,423`

Computes median of benchmark values using an insertion sort (small N).

---

## 9. TUI Components

![TUI Components](TUI_Components.png)

The terminal UI uses [Bubble Tea](https://github.com/charmbracelet/bubbletea) (the Go Elm architecture) with [Lipgloss](https://github.com/charmbracelet/lipgloss) for styling.

### Root model: `App` — `internal/tui/app.go:31`

Manages seven screens via a `screen` enum:

| Screen | Model | File | Purpose |
|--------|-------|------|---------|
| `screenHome` | `homeModel` | `home.go` | 4-option menu (Explore, Download, Local LLMs, Settings) |
| `screenExplore` | `exploreModel` | `explore.go` | Full model catalog browser with search, sort & filters |
| `screenDetail` | `detailModel` | `detail.go` | Full model specs & fit assessment |
| `screenBenchmarks` | `benchmarksModel` | `benchmarks.go` | Benchmark rows for selected model |
| `screenDownload` | `downloadModel` | `download.go` | Coming-soon placeholder |
| `screenLocal` | `localModel` | `local.go` | Coming-soon placeholder |
| `screenSettings` | `settingsModel` | `settings.go` | Hardware info display |

### Screen flow

```
Home ──Enter──► Explore ──Enter──► Detail ──b──► Benchmarks
  ▲                │                                   │
  ├──Esc───────────┘              ◄───────Esc──────────┘
  │
  ├──► Download     (Esc → Home)
  ├──► Local        (Esc → Home)
  └──► Settings     (Esc → Home)
```

### CIRI logo + hardware header

When on the home screen, the 6-line CIRI ASCII art is rendered side-by-side with system info (GPU name, VRAM, RAM, CPU, Ollama/llama.cpp status) above all boxes. Only shown on the home screen to avoid visual clutter on other pages.

### Hardware bar — `hardware_bar.go:11`

Always-visible status line at the top (in the title box) showing: `CPU | RAM avail/total | GPU name + VRAM`

### Home screen (`home.go`)

A menu with four items:

1. **Explore Models** → `screenExplore`
2. **Download Models** → `screenDownload`
3. **Manage Local LLMs** → `screenLocal`
4. **Settings / Hardware Configs** → `screenSettings`

Navigated with ↑/↓, selected with Enter/Space.

### Explore screen (`explore.go`)

Replaces the old results screen. Displays all models in the catalog (with `parameters_raw ≥ 500M`) in a single table.

#### Search/filter bar

Compact llmfit-style bar with five boxed controls:

| Control | Key | Values |
|---------|-----|--------|
| **Search** | `/` | Type to filter by name, quant, or parameter count |
| **Sort** | `s` / `A` / `D` | Name, Params, Speed, Disk, Date, Fit — direction toggled by `A` (asc) / `D` (desc) |
| **Type** | `t` | All, Coding, Chat, Vision, Translation, General |
| **Fit** | `f` | All, Perfect, Good, Slow |
| **Shown** | — | `N/M` (filtered / total) |

Search highlights the box in bold cyan when active. Sort, Type, and Fit boxes are highlighted when the selection deviates from the default.

#### Table columns (no Provider column)

| Column | Content |
|--------|---------|
| `●` | Fit indicator (green = Perfect, yellow = Good, grey = Slow) + ▶ for selected row |
| **Model** | Shows `provider/model-name` (e.g. `meta-llama/Llama-3.2-3B-Instruct`); gets all freed width from removed Provider column |
| **Params** | Parameter count string (e.g. `7B`, `70B`) |
| **tok/s** | Estimated tokens/sec, prefixed with `~` for spill models |
| **Quant** | Quantization format (e.g. `Q4_K_M`, `FP16`) |
| **Disk** | Model weight footprint — **dynamic units**: `<1GB` → `320MB`, `>=1GB` → `8.2G` |
| **Mode** | `GPU` (Recommended) / `CPU` (spills) |
| **Mem%** | VRAM usage % with color: green `<50%`, yellow `≤80%`, red `>80%`, em-dash if no GPU |
| **Ctx** | Context length (k/M suffix) |
| **Date** | Release date (YYYY-MM) |
| **Fit** | Perfect, Good, or Slow — colored text matches fit status |

#### Dynamic disk formatting

The `formatDiskSize()` helper (`explore.go`) switches formatting at the 1 GB boundary:

```go
weightGB := ModelWeightSizeGB(m)
if weightGB < 1.0 {
    return fmt.Sprintf("%.0fMB", weightGB*1024)  // e.g. "320MB"
}
return fmt.Sprintf("%.1fG", weightGB)             // e.g. "8.2G"
```

#### Colored memory percentage

When `mem% < 50` the cell is green, between 50 and 80 it's yellow, above 80 it's red. This helps users instantly see how close a model is to filling VRAM.

#### Fit dots

- Green ● = fits in VRAM (Recommended)
- Yellow ● = spills to RAM (Advanced)
- Grey ● = too heavy

### Detail screen

Shows: model name, provider, parameters, quantization, format, context length, architecture, pipeline, resource requirements (min RAM, recommended RAM, min VRAM), fit assessment with colored status, estimated speed, VRAM usage % (uses `ModelVRAMRequirement` — the larger of `min_vram_gb` and actual weight size), and community stats (downloads, likes).

### Benchmarks screen

Shows real-world tok/s measurements from the benchmark database for the closest hardware match. Each row displays engine name, tok/s, peak VRAM, context length, and notes.

### Settings screen

Shows hardware configuration:
- GPU name and VRAM
- RAM (available / total)
- CPU model and core count
- Ollama ✓/× status
- llama.cpp ✓/× status

Used as a replacement for the info that was previously displayed alongside the CIRI logo on the home screen.

---

## 10. Function Reference

Every function with its file location, callers, and purpose is documented as a Go doc comment in the source code. Below is a summary index.

### Package `main` — `cmd/ciri/main.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `main` | 16 | Go runtime | Entry point; wires dependencies and starts TUI |

### Package `hardware` — `internal/hardware/`

#### `detection.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `GetSpecs` | 75 | `main.go:36` | Orchestrates CPU/RAM/tools/GPU detection |
| `DetectCPU` | 95 | `detection.go:78` | Detects CPU model and cores via ghw |
| `DetectRAM` | 110 | `detection.go:81` | Detects total/available RAM via ghw |
| `DetectOllamaCpp` | 125 | `detection.go:85` | Checks PATH for ollama/llama.cpp |
| `DetectGPU` | 135 | `detection.go:87` | 3-strategy GPU matching cascade |
| `LoadGPUDB` | 194 | `main.go:29` | Loads & normalises gpus.json |
| `deriveAliases` | 263 | `detection.go:257` | Strips vendor prefixes for alias matching |
| `findGPUsByPCI` | 289 | `matcher_pci.go:15` | Searches GPU DB by PCI vendor/device ID |
| `pickBestPCIMatch` | 306 | `matcher_pci.go:21` | Disambiguates multiple PCI matches |
| `abs` | 335 | `detection.go:316,318` | Float absolute value helper |

#### `detection_linux.go` (build tag: `linux`)

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `detectPCI` | 21 | `matcher_pci.go:10`, `matcher_vendor.go:13` | Scans sysfs for PCI devices |
| `readHexFile` | 68 | `detection_linux.go:31-32` | Reads hex value from sysfs file |
| `detectVRAM` | 81 | `matcher_pci.go:20` | Detects VRAM via nvidia-smi or sysfs |
| `detectVendorName` | 113 | `matcher_vendor.go:14` | Queries nvidia-smi/rocm-smi for GPU name |
| `nvidiaSMIQuery` | 133 | `detection_linux.go:119` | Runs nvidia-smi --query-gpu=name |
| `rocmSMIQuery` | 142 | `detection_linux.go:125` | Runs rocm-smi --showproductname --json |
| `detectRawGPUName` | 166 | `matcher_ghw.go:11` | Gets GPU name from ghw, filtering iGPUs |

#### `detection_darwin.go` (build tag: `darwin`)

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `detectPCI` | 15 | `matcher_pci.go:10`, `matcher_vendor.go:13` | macOS stub — returns nil |
| `detectVRAM` | 20 | `matcher_pci.go:20` | macOS stub — returns 0 |
| `detectVendorName` | 25 | `matcher_vendor.go:14` | Uses system_profiler for chipset model |
| `glxinfoQuery` | 45 | (unused) | macOS stub — returns "" |
| `detectRawGPUName` | 50 | `matcher_ghw.go:11` | Uses ghw as fallback on macOS |

#### `tools_unix.go` / `tools_windows.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `execWithTimeout` (unix) | 12 | `detection_linux.go:88,134,144`, `detection_darwin.go:29` | Runs command with deadline |
| `execLookPath` (unix) | 19 | `detection.go:126,128,129` | Checks PATH for executable |
| `detectPCI` (win) | 15 | `matcher_pci.go:10`, `matcher_vendor.go:13` | Queries Windows PnP device IDs |
| `parseWindowsPnP` (win) | 34 | `tools_windows.go:20,31` | Parses PCI IDs from PnP string |
| `detectVRAM` (win) | 55 | `matcher_pci.go:20` | Windows VRAM via nvidia-smi |
| `execWithTimeout` (win) | 75 | `tools_windows.go:17,26,62` | Windows exec with deadline |

#### `normalizer.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `NormalizeGPUName` | 32 | `detection.go:210`, `matcher_ghw.go:27`, `matcher_vendor.go:35,140` | Converts raw name to canonical form |

#### `matcher_pci.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `PCIMatcher.Detect` | 9 | `detection.go:147` | PCI vendor/device ID matching (confidence 0.94-0.98) |

#### `matcher_vendor.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `VendorAPIMatcher.Detect` | 12 | `detection.go:147` | Vendor CLI name matching |
| `resolveByName` | 23 | `matcher_vendor.go:18` | Tiered name resolution with decreasing confidence |
| `findGPUByName` | 53 | `matcher_ghw.go:17`, `matcher_vendor.go:25` | Exact marketing name match |
| `findGPUByAlias` | 64 | `matcher_ghw.go:22`, `matcher_vendor.go:30` | Alias match |
| `findGPUByCanonicalName` | 77 | `matcher_ghw.go:28`, `matcher_vendor.go:36` | Canonical name match |
| `fuzzyFindGPUs` | 88 | `matcher_ghw.go:33`, `matcher_vendor.go:41` | Substring + prefix match |
| `tokenOverlapScore` | 116 | `matcher_ghw.go:40,42` | Token-based fuzzy ranking |
| `tokenizeGPUName` | 139 | `matcher_vendor.go:117-118` | Splits name into tokens (≥2 chars) |

#### `matcher_ghw.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `GHWFuzzyMatcher.Detect` | 10 | `detection.go:147` | Fuzzy name matching via ghw (confidence 0.30-0.90) |

### Package `model` — `internal/model/`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `LoadCatalog` | 35 | `main.go:44` | Loads hf_models.json and categorises |
| `AllCategories` | 17 | `predictor.go:122`, `explore.go:385` | Returns 5 categories in display order |
| `Categorize` | 30 | `catalog.go:47` | Assigns categories from UseCase/Capabilities |
| `hasCapability` | 55 | `category.go:40` | Checks if model has a capability |

### Package `predictor` — `internal/predictor/`

#### `predictor.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `NewPredictor` | 30 | `main.go:57` | Creates Predictor, detects Apple Silicon |
| `Predict` | 47 | `explore.go:55` (via PredictAll), tests | Returns sorted model predictions for category |
| `CountByCategory` | 112 | internal | Counts fitting models per category |
| `hasCategory` | 100 | `predictor.go:52,88` | Checks model category membership |

#### `vram.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `FitStatus.String` | 17 | fmt.Stringer | Human-readable fit status |
| `CheckFit` | 44 | `predictor.go:54,83,120` | Determines Recommended/Advanced/TooHeavy |

#### `estimate.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `LoadBenchmarks` | 81 | `main.go:51` | Loads benchmark_cache.json, builds indices |
| `ByNameHfID` | 147 | `benchmarks.go:31` | Returns byNameHfID index |
| `extractGPUName` | 153 | `estimate.go:105` | Strips VRAM suffix from preset name |
| `EstimateSpeed` | 263 | `predictor.go:61` | 3-tier speed estimation |
| `BytesPerParam` | 325 | `estimate.go:307,371`, `explore.go:89,132` | Returns bytes/param for quant |
| `ModelWeightSizeGB` | 339 | `explore.go:132`, `helpers.go:54` | Computes model weight footprint from params × bytes/param |
| `ModelVRAMRequirement` | 350 | `vram.go:42`, `detail.go:77` | Returns max(MinVRAMGB, weightSize) for honest VRAM check |
| `computeBoundEstimate` | 343 | `estimate.go:302` | Arithmetic throughput cap |
| `lookupMedian` | 362 | `estimate.go:274,277` | Median benchmark lookups |
| `archScaledEstimate` | 380 | `estimate.go:285` | Architecture-family median |
| `median` | 409 | `estimate.go:377,397` | Float64 median |

### Package `tui` — `internal/tui/`

#### `app.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `NewApp` | 52 | `main.go:60` | Creates root App model |
| `Init` | 66 | Bubble Tea | Lifecycle — returns nil |
| `Update` | 69 | Bubble Tea | Routes messages to active screen |
| `View` | 119 | Bubble Tea | Renders logo (home only) + title box + screen content |
| `isTextInput` | 177 | `app.go:93` | True if capturing search text |
| `label` | 181 | `app.go:131-164` | Current screen title for box |
| `renderLogoHeader` | 228 | `app.go:122` | CIRI ASCII art + HW info (home screen only) |
| `hardwareInfoLines` | 242 | `app.go:229` | System info lines for logo side-by-side |

#### `home.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `homeUpdate` | 32 | `app.go:95` | 4-item menu navigation & selection |
| `homeView` | 56 | `app.go:141` | Renders menu (Explore, Download, Local, Settings) |

#### `explore.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `newExploreModel` | 86 | `app.go:132` | Creates explore model with all predictions (≥500M params) |
| `applyFilters` | 99 | `explore.go:93` | Filters by search/fit/type, sorts by column & direction |
| `exploreUpdate` | 179 | `app.go:97` | Keyboard: / f s A D t ↑↓ Enter b Esc |
| `exploreView` | 261 | `app.go:135` | Renders search bar, table, scroll indicators |
| `exploreSearchBar` | 301 | `explore.go:265` | 5-box search/sort/type/fit/shown bar |
| `explorePreview` | 359 | `app.go:137` | Selected model preview bar at bottom |
| `exploreFooter` | 368 | `app.go:140` | Keyboard shortcut help |
| `emVisibleRows` | 377 | `explore.go:274,291` | Visible rows count |
| `cycleTypeFilter` | 383 | `explore.go:260` | Cycles nil → Coding → Chat → Vision → Translation → General → nil |
| `hasTypeCategory` | 398 | `explore.go:119` | Checks if model belongs to a type filter category |
| `exploreColWidths` | 416 | `explore.go:262` | Dynamic column widths (no provider column) |
| `renderExploreHeader` | 431 | `explore.go:267` | Column headers (Model, Params, tok/s, Quant, Disk, Mode, Mem%, Ctx, Date, Fit) |
| `renderExploreRow` | 442 | `explore.go:283` | Single prediction row with provider/name model column |
| `formatDiskSize` | 497 | `explore.go:289` | Dynamic disk formatting (MB <1GB, G >=1GB) |

#### `helpers.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `truncate` | 21 | Various | String truncation with ellipsis |
| `formatDate` | 31 | `explore.go` | Date to YYYY-MM |
| `formatMode` | 41 | `explore.go` | "GPU" vs "CPU" label |
| `formatMemPctRaw` | 48 | `explore.go` | VRAM usage percentage via ModelVRAMRequirement |
| `fitDotStr` | 65 | `explore.go` | Colored fit status dot |
| `fitLabel` | 77 | `explore.go` | Colored fit status text |
| `fitLabelPlain` | 88 | `explore.go` | Uncolored fit status text |
| `padCell` | 99 | `explore.go` | Fixed-width cell padding |
| `renderMiniBox` | 109 | `explore.go` | Rounded control box for search bar |
| `fitFilterLabel` | 124 | `explore.go` | Fit filter display name |

#### `detail.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `newDetailModel` | 25 | `app.go:143` | Creates detail model |
| `detailUpdate` | 33 | `app.go:100` | Esc→Explore / B→Benchmarks |
| `detailView` | 43 | `app.go:146` | Full model detail rendering |
| `detailRow` | 99 | `detail.go:60-90` | Label-value row |
| `formatContext` | 106 | `detail.go:64`, `benchmarks.go:113`, `explore.go:295` | Context length formatting |
| `formatNum` | 116 | `detail.go:88-89` | Large number formatting |

#### `benchmarks.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `newBenchmarksModel` | 23 | `app.go:83,149` | Creates benchmarks model |
| `benchUpdate` | 47 | `app.go:104` | Scroll and back navigation |
| `benchView` | 67 | `app.go:152` | Benchmark table rendering |
| `bmVisibleRows` | 141 | `benchmarks.go:85` | Visible benchmark rows count |

#### `download.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `downloadUpdate` | 10 | `app.go:106` | Esc→Home |
| `downloadView` | 17 | `app.go:158` | "Coming soon" placeholder |

#### `local.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `localUpdate` | 10 | `app.go:110` | Esc→Home |
| `localView` | 17 | `app.go:162` | "Coming soon" placeholder |

#### `settings.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `settingsUpdate` | 18 | `app.go:114` | Esc→Home |
| `settingsView` | 24 | `app.go:167` | Hardware info display |

#### `hardware_bar.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `RenderHardwareBar` | 11 | `app.go:127` | CPU/RAM/GPU status bar |

#### `styles.go`

| Function | Line | Callers | Purpose |
|----------|------|---------|---------|
| `RenderLabeledLine` | 83 | (uncalled) | Section divider with label |
| `RenderDivider` | 95 | `explore.go:269`, `benchmarks.go:98` | Horizontal divider |
| `RenderBox` | 102 | `app.go:128-167` | Bordered box with title |
| `repeat` | 136 | Various | String repetition |
