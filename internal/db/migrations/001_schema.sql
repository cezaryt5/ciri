
CREATE TABLE gpus (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    vram_gb INTEGER,
    memory_type TEXT,
    memory_bus INTEGER,
    pcie TEXT,
    shading_units INTEGER,
    tmus INTEGER,
    rops INTEGER,
    release_date TEXT,
    architecture TEXT,
    memory_bandwidth_gbps REAL,
    fp16_tflops REAL,
    int8_tops REAL,
    tdp_watts INTEGER
);

CREATE TABLE apple_silicon_gpus (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    gpu_cores INTEGER,
    memory_gb INTEGER,
    memory_type TEXT,
    memory_bandwidth_gbps REAL,
    gpu_tflops REAL,
    neural_engine_tops REAL,
    tdp_watts INTEGER,
    release_date TEXT,
    architecture TEXT
);


CREATE TABLE hf_models (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    provider TEXT,
    parameter_count TEXT,
    parameters_raw INTEGER,
    min_ram_gb REAL,
    recommended_ram_gb REAL,
    min_vram_gb REAL,
    quantization TEXT,
    format TEXT,
    context_length INTEGER,
    use_case TEXT,
    capabilities TEXT,
    pipeline_tag TEXT,
    architecture TEXT,
    hf_downloads INTEGER,
    hf_likes INTEGER,
    release_date TEXT,
    num_hidden_layers INTEGER,
    num_attention_heads INTEGER,
    num_key_value_heads INTEGER,
    head_dim INTEGER,
    hidden_size INTEGER,
    vocab_size INTEGER,
    moe_intermediate_size INTEGER,
    shared_expert_intermediate_size INTEGER,
    is_moe BOOLEAN DEFAULT FALSE,
    num_experts INTEGER,
    active_experts INTEGER,
    active_parameters INTEGER,
    trust_level TEXT NOT NULL DEFAULT 'untrusted'
);