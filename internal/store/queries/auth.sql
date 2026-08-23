-- name: InsertUser :exec
INSERT INTO users (id, username, password_hash, oidc_subject, oidc_email, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: GetUserByOIDCSubject :one
SELECT * FROM users WHERE oidc_subject = ? AND oidc_subject != '';

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserOIDC :exec
UPDATE users SET oidc_subject = ?, oidc_email = ?, updated_at = ? WHERE id = ?;

-- name: InsertSession :exec
INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen, user_agent)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen = ?, expires_at = ? WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteSessionsForUser :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;
