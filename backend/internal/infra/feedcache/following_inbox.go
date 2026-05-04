package feedcache

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const followingInboxFanoutBatchSize = 200

const markUserActiveScriptSource = `
local active_key = KEYS[1]
local ttl_seconds = tonumber(ARGV[1])
local refresh_threshold_seconds = tonumber(ARGV[2])

local active_ttl = redis.call("TTL", active_key)
if active_ttl > refresh_threshold_seconds then
	return 0
end

redis.call("SET", active_key, "1", "EX", ttl_seconds)
for i = 2, #KEYS do
	redis.call("EXPIRE", KEYS[i], ttl_seconds)
end

return 1
`

var markUserActiveScript = redis.NewScript(markUserActiveScriptSource)

type FollowingInboxRef struct {
	VideoID     uint64
	PublishedAt time.Time
}

func (c *Cache) MarkUserActive(ctx context.Context, userID uint64) error {
	if c == nil || c.redis == nil || userID == 0 {
		return nil
	}

	keys := []string{
		userActiveKey(userID),
		followingInboxKey(userID),
	}
	keys = append(keys, userRelationTTLKeys(userID)...)

	if err := markUserActiveScript.Run(
		ctx,
		c.redis,
		keys,
		strconv.FormatInt(int64(FollowingActiveTTL/time.Second), 10),
		strconv.FormatInt(int64(FollowingActiveRefreshThreshold/time.Second), 10),
	).Err(); err != nil {
		return fmt.Errorf("mark user active: %w", err)
	}
	return nil
}

func (c *Cache) FilterActiveUserIDs(ctx context.Context, userIDs []uint64) ([]uint64, error) {
	if c == nil || c.redis == nil || len(userIDs) == 0 {
		return nil, nil
	}

	activeUserIDs := make([]uint64, 0, len(userIDs))
	seen := make(map[uint64]struct{}, len(userIDs))
	for start := 0; start < len(userIDs); start += followingInboxFanoutBatchSize {
		end := start + followingInboxFanoutBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}

		keys := make([]string, 0, end-start)
		filteredUserIDs := make([]uint64, 0, end-start)
		for _, userID := range userIDs[start:end] {
			if userID == 0 {
				continue
			}
			if _, exists := seen[userID]; exists {
				continue
			}
			seen[userID] = struct{}{}
			filteredUserIDs = append(filteredUserIDs, userID)
			keys = append(keys, userActiveKey(userID))
		}
		if len(keys) == 0 {
			continue
		}

		values, err := c.redis.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, fmt.Errorf("filter active user ids: %w", err)
		}
		for i, value := range values {
			if value == nil {
				continue
			}
			activeUserIDs = append(activeUserIDs, filteredUserIDs[i])
		}
	}

	return activeUserIDs, nil
}

func (c *Cache) LoadFollowingInboxRefs(ctx context.Context, userID uint64, maxPublishedAt *time.Time, count int) ([]FollowingInboxRef, error) {
	if c == nil || c.redis == nil || userID == 0 || count <= 0 {
		return nil, nil
	}

	query := &redis.ZRangeBy{
		Max:   "+inf",
		Min:   "-inf",
		Count: int64(count),
	}
	if maxPublishedAt != nil && !maxPublishedAt.IsZero() {
		query.Max = strconv.FormatInt(maxPublishedAt.UTC().UnixMilli(), 10)
	}

	zs, err := c.redis.ZRevRangeByScoreWithScores(ctx, followingInboxKey(userID), query).Result()
	if err != nil {
		return nil, fmt.Errorf("load following inbox refs: %w", err)
	}

	refs := make([]FollowingInboxRef, 0, len(zs))
	seen := make(map[uint64]struct{}, len(zs))
	for _, z := range zs {
		videoID, ok := parseFollowingInboxMemberToUint64(z.Member)
		if !ok || videoID == 0 {
			continue
		}
		if _, exists := seen[videoID]; exists {
			continue
		}
		seen[videoID] = struct{}{}
		refs = append(refs, FollowingInboxRef{
			VideoID:     videoID,
			PublishedAt: time.UnixMilli(int64(z.Score)).UTC(),
		})
	}

	sort.Slice(refs, func(i, j int) bool {
		left := refs[i].PublishedAt.UTC()
		right := refs[j].PublishedAt.UTC()
		if left.Equal(right) {
			return refs[i].VideoID > refs[j].VideoID
		}
		return left.After(right)
	})

	if len(refs) > count {
		refs = refs[:count]
	}
	return refs, nil
}

func (c *Cache) AddFollowingInboxRefs(ctx context.Context, userID uint64, refs []FollowingInboxRef) error {
	if c == nil || c.redis == nil || userID == 0 || len(refs) == 0 {
		return nil
	}

	return c.addFollowingInboxRefsForUsers(ctx, []uint64{userID}, refs)
}

func (c *Cache) FanoutFollowingInboxRefs(ctx context.Context, userIDs []uint64, refs []FollowingInboxRef) error {
	if c == nil || c.redis == nil || len(userIDs) == 0 || len(refs) == 0 {
		return nil
	}

	return c.addFollowingInboxRefsForUsers(ctx, userIDs, refs)
}

func (c *Cache) RemoveFollowingInboxVideo(ctx context.Context, userIDs []uint64, videoID uint64) error {
	if c == nil || c.redis == nil || len(userIDs) == 0 || videoID == 0 {
		return nil
	}

	member := strconv.FormatUint(videoID, 10)
	for start := 0; start < len(userIDs); start += followingInboxFanoutBatchSize {
		end := start + followingInboxFanoutBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}

		pipe := c.redis.Pipeline()
		for _, userID := range userIDs[start:end] {
			if userID == 0 {
				continue
			}
			pipe.ZRem(ctx, followingInboxKey(userID), member)
		}

		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("remove following inbox video: %w", err)
		}
	}
	return nil
}

func (c *Cache) RemoveFollowingInboxVideos(ctx context.Context, userID uint64, videoIDs []uint64) error {
	if c == nil || c.redis == nil || userID == 0 || len(videoIDs) == 0 {
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

	if err := c.redis.ZRem(ctx, followingInboxKey(userID), members...).Err(); err != nil {
		return fmt.Errorf("remove following inbox videos: %w", err)
	}
	return nil
}

func addFollowingInboxRefsToPipeline(ctx context.Context, pipe redis.Pipeliner, userID uint64, refs []FollowingInboxRef) {
	if userID == 0 || len(refs) == 0 {
		return
	}

	zs := make([]redis.Z, 0, len(refs))
	for _, ref := range refs {
		if ref.VideoID == 0 || ref.PublishedAt.IsZero() {
			continue
		}
		zs = append(zs, redis.Z{
			Score:  float64(ref.PublishedAt.UTC().UnixMilli()),
			Member: strconv.FormatUint(ref.VideoID, 10),
		})
	}
	if len(zs) == 0 {
		return
	}

	key := followingInboxKey(userID)
	pipe.ZAdd(ctx, key, zs...)
	pipe.ZRemRangeByRank(ctx, key, 0, -FollowingInboxMaxEntries-1)
	pipe.Expire(ctx, key, FollowingActiveTTL)
}

func (c *Cache) addFollowingInboxRefsForUsers(ctx context.Context, userIDs []uint64, refs []FollowingInboxRef) error {
	if len(userIDs) == 0 || len(refs) == 0 {
		return nil
	}

	for start := 0; start < len(userIDs); start += followingInboxFanoutBatchSize {
		end := start + followingInboxFanoutBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}

		pipe := c.redis.Pipeline()
		for _, userID := range userIDs[start:end] {
			addFollowingInboxRefsToPipeline(ctx, pipe, userID, refs)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("add following inbox refs: %w", err)
		}
	}
	return nil
}

func parseFollowingInboxMemberToUint64(member interface{}) (uint64, bool) {
	switch value := member.(type) {
	case string:
		parsed, err := strconv.ParseUint(value, 10, 64)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.ParseUint(string(value), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
