-- name: ListFeedItems :many
SELECT id, user_id, counter, repeat_key, body, created_at
FROM user_feed_items
WHERE user_id = ?
  AND (? IS NULL OR counter < ?)
  AND (? IS NULL OR counter > ?)
ORDER BY counter DESC
LIMIT ?;

-- name: CreateFeedItem :exec
INSERT INTO user_feed_items (
    id, user_id, counter, repeat_key, body
) VALUES (
    ?, ?, ?, ?, ?
);

-- name: DeleteFeedItemsByRepeatKey :exec
DELETE FROM user_feed_items
WHERE user_id = ? AND repeat_key = ?;
