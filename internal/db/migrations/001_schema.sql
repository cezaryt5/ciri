CREATE TABLE gpus (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vendor TEXT NOT NULL, -- 'NVIDIA', 'AMD', 'Intel', 'Apple'
    name TEXT NOT NULL,
    canonical_name TEXT UNIQUE, -- Needed for your alias/normalized search
    vram_gb REAL,
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
CREATE UNIQUE INDEX idx_gpus_name ON gpus(name);

CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    gpu_id INTEGER, -- REMOVED UNIQUE constraint
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (gpu_id) REFERENCES gpus(id)
);

CREATE TABLE gpu_pci_ids (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vendor_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    subsystem_vendor_id TEXT,
    subsystem_device_id TEXT,
    gpu_id INTEGER NOT NULL,
    source TEXT NOT NULL DEFAULT 'manual',
    confidence REAL NOT NULL DEFAULT 1.0,
    FOREIGN KEY (gpu_id) REFERENCES gpus(id) ON DELETE CASCADE,
    UNIQUE (vendor_id, device_id, subsystem_vendor_id, subsystem_device_id)
);
