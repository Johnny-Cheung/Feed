package feedcache

import (
	"context"
	"fmt"
	"strconv"
)

func (c *Cache) MarkHomeHotDirty(ctx context.Context, videoIDs ...uint64) error {
	if c == nil || c.redis == nil || len(videoIDs) == 0 {
		return nil
	}

	members := make([]interface{}, 0, len(videoIDs))
	for _, videoID := range videoIDs {
		if videoID == 0 {
			continue
		}
		members = append(members, strconv.FormatUint(videoID, 10))
	}
	if len(members) == 0 {
		return nil
	}

	if err := c.redis.SAdd(ctx, HomeHotDirtyKey(), members...).Err(); err != nil {
		return fmt.Errorf("mark home hot dirty: %w", err)
	}
	return nil
}
