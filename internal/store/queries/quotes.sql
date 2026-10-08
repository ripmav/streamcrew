-- name: GetQuote :one
SELECT * FROM quotes WHERE number = ?;

-- name: ListQuotes :many
SELECT * FROM quotes ORDER BY number;

-- name: RandomQuote :one
SELECT * FROM quotes ORDER BY random() LIMIT 1;

-- name: LatestQuote :one
SELECT * FROM quotes ORDER BY number DESC LIMIT 1;

-- name: CountQuotes :one
SELECT count(*) FROM quotes;

-- name: InsertQuote :execlastid
INSERT INTO quotes (id, text, game, quoted_at, added_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InsertQuoteWithNumber :exec
INSERT INTO quotes (number, id, text, game, quoted_at, added_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateQuote :execrows
UPDATE quotes SET text = ?, game = ?, quoted_at = ?, added_by = ?, updated_at = ? WHERE number = ?;

-- name: DeleteQuote :execrows
DELETE FROM quotes WHERE number = ?;

-- SPDX-License-Identifier: MIT
