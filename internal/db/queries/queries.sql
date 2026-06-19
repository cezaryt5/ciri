-- name: GetGPUByName :one
SELECT * FROM gpus WHERE name = ?;

