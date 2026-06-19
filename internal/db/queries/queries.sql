-- name: GetGPUByName :one
SELECT * FROM gpus WHERE name = ?;

-- name: GetGPUsByArchitecture :many
SELECT * FROM gpus WHERE architecture = ?;

-- name: GetGPUsByVRAM :many
SELECT * FROM gpus WHERE vram_gb >= ? ORDER BY vram_gb;

-- name: GetAllGPUs :many
SELECT * FROM gpus;

-- name: GetAppleSiliconGPUByName :one
SELECT * FROM apple_silicon_gpus WHERE name = ?;

-- name: GetAllAppleSiliconGPUs :many
SELECT * FROM apple_silicon_gpus;