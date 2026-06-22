package ciridata

import "embed"

//go:embed gpus.json hf_models.json benchmark_cache.json
var FS embed.FS
