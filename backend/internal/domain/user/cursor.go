package user

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func decodeTimeCursor(raw string) (*timeCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode time cursor: %w", err)
	}

	var cursor timeCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, fmt.Errorf("unmarshal time cursor: %w", err)
	}
	if cursor.ID == 0 {
		return nil, fmt.Errorf("time cursor id is invalid")
	}
	if cursor.Time.IsZero() {
		return nil, fmt.Errorf("time cursor time is invalid")
	}

	cursor.Time = cursor.Time.UTC()
	return &cursor, nil
}

func encodeTimeCursor(cursor timeCursor) (string, error) {
	cursor.Time = cursor.Time.UTC().Round(0)

	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal time cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}
