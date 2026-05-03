package interaction

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/infra/feedcache"

	"github.com/redis/go-redis/v9"
)

const (
	videoRelationStreamGroup       = "feed-video-relation-writers"
	videoRelationStreamBatchSize   = 20
	videoRelationStreamBlock       = 5 * time.Second
	videoRelationStreamPendingIdle = 30 * time.Second
)

type videoRelationStreamEvent struct {
	EventID      string
	EventType    string
	RelationType string
	UserID       uint64
	VideoID      uint64
	Active       bool
	OccurredAt   string
}

func startVideoRelationStreamConsumer(ctx context.Context, repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, homeHotMaxEntries int) error {
	if redisClient == nil {
		return nil
	}

	if err := redisClient.XGroupCreateMkStream(ctx, feedcache.VideoRelationStreamKey(), videoRelationStreamGroup, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create video relation stream group: %w", err)
	}

	go consumeVideoRelationStream(ctx, repo, cache, redisClient, videoRelationStreamConsumerName(), homeHotMaxEntries)
	return nil
}

func consumeVideoRelationStream(ctx context.Context, repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, consumerName string, homeHotMaxEntries int) {
	claimStart := "0-0"
	for {
		if ctx.Err() != nil {
			return
		}

		nextStart, err := consumeClaimedVideoRelationMessages(ctx, repo, cache, redisClient, consumerName, homeHotMaxEntries, claimStart)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("claim video relation stream messages failed: err=%v", err)
			time.Sleep(time.Second)
		} else {
			claimStart = nextStart
		}

		streams, err := redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    videoRelationStreamGroup,
			Consumer: consumerName,
			Streams:  []string{feedcache.VideoRelationStreamKey(), ">"},
			Count:    videoRelationStreamBatchSize,
			Block:    videoRelationStreamBlock,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, redis.Nil) {
				continue
			}
			log.Printf("read video relation stream failed: err=%v", err)
			time.Sleep(time.Second)
			continue
		}

		consumeVideoRelationMessages(ctx, repo, cache, redisClient, streams, homeHotMaxEntries)
	}
}

func consumeClaimedVideoRelationMessages(ctx context.Context, repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, consumerName string, homeHotMaxEntries int, start string) (string, error) {
	if start == "" {
		start = "0-0"
	}

	messages, nextStart, err := redisClient.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   feedcache.VideoRelationStreamKey(),
		Group:    videoRelationStreamGroup,
		Consumer: consumerName,
		MinIdle:  videoRelationStreamPendingIdle,
		Start:    start,
		Count:    videoRelationStreamBatchSize,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "0-0", nil
		}
		return start, err
	}

	if len(messages) > 0 {
		consumeVideoRelationMessages(ctx, repo, cache, redisClient, []redis.XStream{{
			Stream:   feedcache.VideoRelationStreamKey(),
			Messages: messages,
		}}, homeHotMaxEntries)
	}
	if nextStart == "" {
		return "0-0", nil
	}
	return nextStart, nil
}

func consumeVideoRelationMessages(ctx context.Context, repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, streams []redis.XStream, homeHotMaxEntries int) {
	for _, stream := range streams {
		for _, message := range stream.Messages {
			event, err := parseVideoRelationStreamMessage(message)
			if err != nil {
				log.Printf("parse video relation stream message failed: stream_id=%s err=%v", message.ID, err)
				_ = redisClient.XAck(ctx, feedcache.VideoRelationStreamKey(), videoRelationStreamGroup, message.ID).Err()
				continue
			}

			if err := handleVideoRelationStreamEvent(ctx, repo, cache, redisClient, event, homeHotMaxEntries); err != nil {
				log.Printf("handle video relation stream message failed: stream_id=%s event_id=%s type=%s user_id=%d video_id=%d err=%v", message.ID, event.EventID, event.EventType, event.UserID, event.VideoID, err)
				continue
			}

			if err := redisClient.XAck(ctx, feedcache.VideoRelationStreamKey(), videoRelationStreamGroup, message.ID).Err(); err != nil {
				log.Printf("ack video relation stream message failed: stream_id=%s err=%v", message.ID, err)
			}
		}
	}
}

func handleVideoRelationStreamEvent(ctx context.Context, repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, event *videoRelationStreamEvent, homeHotMaxEntries int) error {
	video, err := repo.GetVideoByIDIncludingDeleted(ctx, event.VideoID)
	if err != nil {
		if errors.Is(err, appErrors.ErrVideoNotFound) {
			return nil
		}
		return err
	}

	active := event.Active
	if cache != nil {
		loadedActive, found, err := cache.LoadVideoRelationState(ctx, event.RelationType, event.UserID, event.VideoID, video.PublishedAt)
		if err != nil {
			return err
		}
		if found {
			active = loadedActive
		}
	}

	switch event.RelationType {
	case feedcache.VideoRelationTypeLike:
		if active {
			if _, err := repo.CreateLikeIfAbsent(ctx, event.UserID, event.VideoID); err != nil {
				return err
			}
		} else if _, err := repo.DeleteLikeIfExists(ctx, event.UserID, event.VideoID); err != nil {
			return err
		}
	case feedcache.VideoRelationTypeFavorite:
		if active {
			if _, err := repo.CreateFavoriteIfAbsent(ctx, event.UserID, event.VideoID); err != nil {
				return err
			}
		} else if _, err := repo.DeleteFavoriteIfExists(ctx, event.UserID, event.VideoID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown video relation type %q", event.RelationType)
	}

	if err := upsertVideoStatsFromCacheOrMySQL(ctx, repo, cache, event.VideoID); err != nil {
		return err
	}
	return markVideoHotFeedDirty(ctx, redisClient, event.VideoID)
}

func parseVideoRelationStreamMessage(message redis.XMessage) (*videoRelationStreamEvent, error) {
	eventID, ok := streamStringValue(message.Values, "event_id")
	if !ok || eventID == "" {
		return nil, fmt.Errorf("missing event_id")
	}
	eventType, ok := streamStringValue(message.Values, "event_type")
	if !ok || eventType == "" {
		return nil, fmt.Errorf("missing event_type")
	}
	relationType, ok := streamStringValue(message.Values, "relation_type")
	if !ok || relationType == "" {
		return nil, fmt.Errorf("missing relation_type")
	}
	userID, ok := streamUint64Value(message.Values, "user_id")
	if !ok || userID == 0 {
		return nil, fmt.Errorf("invalid user_id")
	}
	videoID, ok := streamUint64Value(message.Values, "video_id")
	if !ok || videoID == 0 {
		return nil, fmt.Errorf("invalid video_id")
	}
	activeRaw, ok := streamStringValue(message.Values, "active")
	if !ok || (activeRaw != "1" && activeRaw != "0") {
		return nil, fmt.Errorf("invalid active")
	}
	occurredAt, _ := streamStringValue(message.Values, "occurred_at")

	return &videoRelationStreamEvent{
		EventID:      eventID,
		EventType:    eventType,
		RelationType: relationType,
		UserID:       userID,
		VideoID:      videoID,
		Active:       activeRaw == "1",
		OccurredAt:   occurredAt,
	}, nil
}

func streamStringValue(values map[string]interface{}, field string) (string, bool) {
	value, exists := values[field]
	if !exists || value == nil {
		return "", false
	}

	switch current := value.(type) {
	case string:
		return current, true
	case []byte:
		return string(current), true
	case int:
		return strconv.Itoa(current), true
	case int64:
		return strconv.FormatInt(current, 10), true
	case uint64:
		return strconv.FormatUint(current, 10), true
	default:
		return "", false
	}
}

func streamUint64Value(values map[string]interface{}, field string) (uint64, bool) {
	raw, ok := streamStringValue(values, field)
	if !ok || raw == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

func videoRelationStreamConsumerName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("interaction-video-relation-%s-%d", hostname, os.Getpid())
}
