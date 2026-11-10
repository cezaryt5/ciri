package ciridata

import "embed"

//go:embed gpus.json hf_models.json benchmark_cache.json catalog_v2.json.gz
var FS embed.FS
