package feedcache

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	authorFollowingModePull          = "pull"
	authorOutboxBatchSize            = 100
	followedPullAuthorsReadySentinel = "0"
)

func (c *Cache) AddAuthorOutboxRefs(ctx context.Context, authorID uint64, refs []FollowingInboxRef) error {
	if c == nil || c.redis == nil || authorID == 0 || len(refs) == 0 {
		return nil
	}

	pipe := c.redis.Pipeline()
	addAuthorOutboxRefsToPipeline(ctx, pipe, authorID, refs)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("add author outbox refs: %w", err)
	}
	return nil
}

func (c *Cache) ReplaceAuthorOutboxRefs(ctx context.Context, authorID uint64, refs []FollowingInboxRef) error {
	if c == nil || c.redis == nil || authorID == 0 {
		return nil
	}

	pipe := c.redis.TxPipeline()
	pipe.Del(ctx, authorOutboxKey(authorID))
	addAuthorOutboxRefsToPipeline(ctx, pipe, authorID, refs)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("replace author outbox refs: %w", err)
	}
	return nil
}

func (c *Cache) DeleteAuthorOutbox(ctx context.Context, authorID uint64) error {
	if c == nil || c.redis == nil || authorID == 0 {
		return nil
	}

	if err := c.redis.Del(ctx, authorOutboxKey(authorID)).Err(); err != nil {
		return fmt.Errorf("delete author outbox: %w", err)
	}
	return nil
}

func (c *Cache) RemoveAuthorOutboxVideo(ctx context.Context, authorID, videoID uint64) error {
	if c == nil || c.redis == nil || authorID == 0 || videoID == 0 {
		return nil
	}

	if err := c.redis.ZRem(ctx, authorOutboxKey(authorID), strconv.FormatUint(videoID, 10)).Err(); err != nil {
		return fmt.Errorf("remove author outbox video: %w", err)
	}
	return nil
}

func (c *Cache) LoadMergedAuthorOutboxRefs(ctx context.Context, authorIDs []uint64, maxPublishedAt *time.Time, count int) ([]FollowingInboxRef, error) {
	if c == nil || c.redis == nil || len(authorIDs) == 0 || count <= 0 {
		return nil, nil
	}

	queryMax := "+inf"
	if maxPublishedAt != nil && !maxPublishedAt.IsZero() {
		queryMax = strconv.FormatInt(maxPublishedAt.UTC().UnixMilli(), 10)
	}

	pipe := c.redis.Pipeline()
	cmds := make([]*redis.ZSliceCmd, 0, len(authorIDs))
	filteredAuthorIDs := make([]uint64, 0, len(authorIDs))
	for _, authorID := range authorIDs {
		if authorID == 0 {
			continue
		}
		filteredAuthorIDs = append(filteredAuthorIDs, authorID)
		cmds = append(cmds, pipe.ZRevRangeByScoreWithScores(ctx, authorOutboxKey(authorID), &redis.ZRangeBy{
			Max:   queryMax,
			Min:   "-inf",
			Count: int64(count),
		}))
	}

	if len(cmds) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("load merged author outbox refs: %w", err)
	}

	refs := make([]FollowingInboxRef, 0, len(cmds)*count)
	seen := make(map[uint64]struct{}, len(cmds)*count)
	for i, cmd := range cmds {
		zs, err := cmd.Result()
		if err != nil {
			return nil, fmt.Errorf("load author outbox refs: author_id=%d err=%w", filteredAuthorIDs[i], err)
		}
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

func (c *Cache) LoadFollowedPullAuthorIDs(ctx context.Context, userID uint64) ([]uint64, bool, error) {
	if userID == 0 {
		return nil, true, nil
	}
	if c == nil || c.redis == nil {
		return nil, false, nil
	}

	meta, err := c.loadViewerRelationMeta(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	if !meta.FollowsFullReady {
		return nil, false, nil
	}

	members, err := c.redis.SMembers(ctx, followedPullAuthorsKey(userID)).Result()
	if err != nil {
		return nil, false, fmt.Errorf("load followed pull author ids: %w", err)
	}
	if len(members) == 0 {
		return nil, false, nil
	}

	authorIDs := make([]uint64, 0, len(members))
	for _, member := range members {
		if member == followedPullAuthorsReadySentinel {
			continue
		}
		authorID, parseErr := strconv.ParseUint(member, 10, 64)
		if parseErr != nil || authorID == 0 {
			continue
		}
		authorIDs = append(authorIDs, authorID)
	}
	return authorIDs, true, nil
}

func (c *Cache) ReplaceFollowedPullAuthorIDs(ctx context.Context, userID uint64, authorIDs []uint64) error {
	if c == nil || c.redis == nil || userID == 0 {
		return nil
	}

	pipe := c.redis.TxPipeline()
	pipe.Del(ctx, followedPullAuthorsKey(userID))
	addFollowedPullAuthorsReplaceToPipeline(ctx, pipe, userID, authorIDs)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("replace followed pull author ids: %w", err)
	}
	return nil
}

func (c *Cache) SyncFollowedPullAuthor(ctx context.Context, userID, authorID uint64, followedPull bool) error {
	if c == nil || c.redis == nil || userID == 0 || authorID == 0 {
		return nil
	}

	pipe := c.redis.Pipeline()
	addFollowedPullAuthorSyncToPipeline(ctx, pipe, userID, authorID, followedPull)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("sync followed pull author: %w", err)
	}
	return nil
}

func (c *Cache) AddFollowedPullAuthorForUsers(ctx context.Context, userIDs []uint64, authorID uint64) error {
	return c.syncFollowedPullAuthorForUsers(ctx, userIDs, authorID, true)
}

func (c *Cache) RemoveFollowedPullAuthorForUsers(ctx context.Context, userIDs []uint64, authorID uint64) error {
	return c.syncFollowedPullAuthorForUsers(ctx, userIDs, authorID, false)
}

func (c *Cache) LoadAuthorPullModeIDs(ctx context.Context, authorIDs []uint64) (map[uint64]struct{}, error) {
	result := make(map[uint64]struct{})
	if c == nil || c.redis == nil || len(authorIDs) == 0 {
		return result, nil
	}

	keys := make([]string, 0, len(authorIDs))
	filteredAuthorIDs := make([]uint64, 0, len(authorIDs))
	for _, authorID := range authorIDs {
		if authorID == 0 {
			continue
		}
		filteredAuthorIDs = append(filteredAuthorIDs, authorID)
		keys = append(keys, authorFollowingModeKey(authorID))
	}
	if len(keys) == 0 {
		return result, nil
	}

	values, err := c.redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("load author pull modes: %w", err)
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		raw, ok := redisValueToString(value)
		if !ok || raw != authorFollowingModePull {
			continue
		}
		result[filteredAuthorIDs[i]] = struct{}{}
	}
	return result, nil
}

func (c *Cache) IsAuthorPullMode(ctx context.Context, authorID uint64) (bool, error) {
	if c == nil || c.redis == nil || authorID == 0 {
		return false, nil
	}

	value, err := c.redis.Get(ctx, authorFollowingModeKey(authorID)).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, fmt.Errorf("get author pull mode: %w", err)
	}
	return value == authorFollowingModePull, nil
}

func (c *Cache) SetAuthorPullMode(ctx context.Context, authorID uint64, pull bool) error {
	if c == nil || c.redis == nil || authorID == 0 {
		return nil
	}

	key := authorFollowingModeKey(authorID)
	if pull {
		if err := c.redis.Set(ctx, key, authorFollowingModePull, 0).Err(); err != nil {
			return fmt.Errorf("set author pull mode: %w", err)
		}
		return nil
	}
	if err := c.redis.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("clear author pull mode: %w", err)
	}
	return nil
}

func (c *Cache) syncFollowedPullAuthorForUsers(ctx context.Context, userIDs []uint64, authorID uint64, followedPull bool) error {
	if c == nil || c.redis == nil || len(userIDs) == 0 || authorID == 0 {
		return nil
	}

	for start := 0; start < len(userIDs); start += followingInboxFanoutBatchSize {
		end := start + followingInboxFanoutBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}

		pipe := c.redis.Pipeline()
		for _, userID := range userIDs[start:end] {
			addFollowedPullAuthorSyncToPipeline(ctx, pipe, userID, authorID, followedPull)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("sync followed pull author for users: %w", err)
		}
	}
	return nil
}

func addFollowedPullAuthorsReplaceToPipeline(ctx context.Context, pipe redis.Pipeliner, userID uint64, authorIDs []uint64) {
	if userID == 0 {
		return
	}

	members := make([]interface{}, 0, len(authorIDs)+1)
	members = append(members, followedPullAuthorsReadySentinel)
	seen := make(map[uint64]struct{}, len(authorIDs))
	for _, authorID := range authorIDs {
		if authorID == 0 {
			continue
		}
		if _, exists := seen[authorID]; exists {
			continue
		}
		seen[authorID] = struct{}{}
		members = append(members, strconv.FormatUint(authorID, 10))
	}

	key := followedPullAuthorsKey(userID)
	pipe.SAdd(ctx, key, members...)
	pipe.Expire(ctx, key, FollowingActiveTTL)
}

func addFollowedPullAuthorSyncToPipeline(ctx context.Context, pipe redis.Pipeliner, userID, authorID uint64, followedPull bool) {
	if userID == 0 || authorID == 0 {
		return
	}

	key := followedPullAuthorsKey(userID)
	pipe.SAdd(ctx, key, followedPullAuthorsReadySentinel)
	if followedPull {
		pipe.SAdd(ctx, key, strconv.FormatUint(authorID, 10))
	} else {
		pipe.SRem(ctx, key, strconv.FormatUint(authorID, 10))
	}
	pipe.Expire(ctx, key, FollowingActiveTTL)
}

func addAuthorOutboxRefsToPipeline(ctx context.Context, pipe redis.Pipeliner, authorID uint64, refs []FollowingInboxRef) {
	if authorID == 0 || len(refs) == 0 {
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

	key := authorOutboxKey(authorID)
	pipe.ZAdd(ctx, key, zs...)
	pipe.ZRemRangeByRank(ctx, key, 0, -FollowingInboxMaxEntries-1)
}
