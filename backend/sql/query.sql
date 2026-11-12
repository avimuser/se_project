-- name: CreateLaundromat :one
INSERT INTO "laundromat" DEFAULT VALUES RETURNING *;

-- name: GetLaundromats :many
SELECT * FROM "laundromat";