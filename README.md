# Can_I_Run_IT






State Machine Architecture
Root Model (ciriModel)
├── State: HOME (category menu)
├── State: RESULTS (filtered models for selected category)
├── State: DETAIL (single model full specs)
└── State: BENCHMARKS (tok/s data from benchmark_cache.json)
Each state is its own Bubble Tea Model with Update() and View(). The root model delegates to the current state. Navigation:
HOME ──[Enter]──→ RESULTS ──[Enter]──→ DETAIL
  ↑                  │                     │
  │                  │──[B]────────────→ BENCHMARKS
  │                  │                     │
  └──[Esc]──────────┘──[Esc]──────────────┘
Screen 1: HOME (Category Selection)
Hardware: i7-7700HQ │ 7.2/15.6 GB RAM │ GTX 1070 8GB │ Ollama ✓ │ llama.cpp ✓
───────────────────────────────────────────────────────────────────────────────
 What do you want to do?

   1. Coding          (42 models fit)
   2. Chat             (38 models fit)
   3. General          (27 models fit)
   4. Vision           (15 models fit)
   5. Translation      (8 models fit)

↑↓ Select  / Search  q Quit
- Hardware bar: single line, computed from terminal width
- Menu: centered or left-aligned, no boxes, no borders
- Footer: single line keybinding hints



Screen 2: RESULTS (Per-Category)
Hardware: i7-7700HQ │ 7.2/15.6 GB RAM │ GTX 1070 8GB
Category: Coding                                           [Esc] Back
──────────────────────────────────────────────────────────────────────
 RECOMMENDED (fits in 8GB VRAM)

   ✓ Qwen3-4B-Instruct       4B   Q4_K_M   42 tok/s
   ✓ Phi-4-mini-instruct     4B   Q8_0     50 tok/s
   ✓ Llama-3.2-3B-Instruct   3B   Q8_0     58 tok/s
   ✓ Gemma-4-2B-it            2B   Q8_0     71 tok/s

 ADVANCED (spills to RAM — will be slow)

   ⚠ Qwen3-8B-Instruct       8B   Q4_K_M   ~4 tok/s
   ⚠ Llama-3.1-8B-Instruct   8B   Q4_K_M   ~6 tok/s

↑↓ Navigate  Enter Details  B Benchmarks  Esc Back
- No side panels. No charts. Just a clean vertical list with two section headers.
- Scrollable via bubbles/viewport if list exceeds terminal height.
- tok/s values: from benchmark_cache.json if available, otherwise estimated via (available_vram / model_vram_requirement) * base_tok_per_sec.


Screen 3: DETAIL
Hardware: i7-7700HQ │ 7.2/15.6 GB RAM │ GTX 1070 8GB
Qwen3-4B-Instruct                                           [Esc] Back
──────────────────────────────────────────────────────────────────────
 Provider        Alibaba
 Parameters      4B
 Quantization     Q4_K_M
 Format           GGUF
 Context Length   262k
 Architecture     qwen3
 Pipeline         chat

 Resources
   Min RAM        2.0 GB
   Recommended    4.0 GB
   Min VRAM       2.5 GB
   Disk           2.4 GB

 Fit Assessment
   Status         Perfect (fits in VRAM)
   Est. Speed     42 tok/s
   VRAM Usage     62%

 Community
   Downloads      14,320
   Likes          432

 Esc Back  B Benchmarks


 
Screen 4: BENCHMARKS (from benchmark_cache.json)
Hardware: i7-7700HQ │ 7.2/15.6 GB RAM │ GTX 1070 8GB
Benchmarks: Qwen3-4B-Instruct                               [Esc] Back
──────────────────────────────────────────────────────────────────────
 Benchmark Results (closest hardware match)

 Engine          tok/s    VRAM     Context   Notes
 llama.cpp       42.1     3.2 GB   32k       Q4_K_M, CUDA
 vLLM            —        —        —         Not benchmarked on
                                             comparable hardware

 * Estimated from nearest hardware preset: GTX 1070 class

 Esc Back