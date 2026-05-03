package interaction

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func decodeCommentCursor(raw string) (*commentCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode comment cursor: %w", err)
	}

	var cursor commentCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, fmt.Errorf("unmarshal comment cursor: %w", err)
	}

	if cursor.ID == 0 {
		return nil, fmt.Errorf("comment cursor id is invalid")
	}
	if cursor.Time.IsZero() {
		return nil, fmt.Errorf("comment cursor time is invalid")
	}

	cursor.Time = cursor.Time.UTC()
	return &cursor, nil
}

func encodeCommentCursor(cursor commentCursor) (string, error) {
	cursor.Time = cursor.Time.UTC().Round(0)

	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal comment cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}
