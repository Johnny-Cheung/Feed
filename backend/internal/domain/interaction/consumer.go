package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/hotscore"
	"feed-backend/internal/infra/feedcache"
	"feed-backend/internal/model"

	"github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	homeHotFeedKey            = "feed:home:hot"
	statsConsumerTag          = "interaction-video-stats"
	hotFeedConsumerTag        = "interaction-video-hotfeed"
	followingInboxConsumerTag = "interaction-video-following-inbox"
	userFollowConsumerTag     = "interaction-user-follow"
)

type Consumers struct {
	channels    []*amqp091.Channel
	cancelFuncs []context.CancelFunc
}

func StartConsumers(db *gorm.DB, redisClient *redis.Client, rabbitConn *amqp091.Connection, queueStats, queueHotFeed, queueFollowingInbox, queueUserFollow string, followingPullModeThreshold, homeHotMaxEntries int) (*Consumers, error) {
	if rabbitConn == nil {
		return nil, fmt.Errorf("rabbitmq connection is nil")
	}
	if followingPullModeThreshold <= 0 {
		return nil, fmt.Errorf("following pull mode threshold must be positive")
	}
	if homeHotMaxEntries <= 0 {
		return nil, fmt.Errorf("home hot max entries must be positive")
	}

	repo := NewRepository(db)
	cache := feedcache.NewCache(db, redisClient)
	manager := &Consumers{}

	if redisClient != nil {
		streamCtx, cancel := context.WithCancel(context.Background())
		manager.cancelFuncs = append(manager.cancelFuncs, cancel)
		if err := startVideoRelationStreamConsumer(streamCtx, repo, cache, redisClient, homeHotMaxEntries); err != nil {
			manager.Close()
			return nil, fmt.Errorf("start video relation stream consumer: %w", err)
		}
	}

	if queueStats != "" {
		statsCh, err := rabbitConn.Channel()
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("open video stats consumer channel: %w", err)
		}
		manager.channels = append(manager.channels, statsCh)

		if err := statsCh.Qos(10, 0, false); err != nil {
			manager.Close()
			return nil, fmt.Errorf("set video stats consumer qos: %w", err)
		}

		deliveries, err := statsCh.Consume(queueStats, statsConsumerTag, false, false, false, false, nil)
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("consume video stats queue: %w", err)
		}

		go consumeVideoStats(repo, cache, deliveries)
	}

	if queueHotFeed != "" {
		hotFeedCh, err := rabbitConn.Channel()
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("open video hotfeed consumer channel: %w", err)
		}
		manager.channels = append(manager.channels, hotFeedCh)

		if err := hotFeedCh.Qos(10, 0, false); err != nil {
			manager.Close()
			return nil, fmt.Errorf("set video hotfeed consumer qos: %w", err)
		}

		deliveries, err := hotFeedCh.Consume(queueHotFeed, hotFeedConsumerTag, false, false, false, false, nil)
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("consume video hotfeed queue: %w", err)
		}

		go consumeVideoHotFeed(repo, cache, redisClient, deliveries, homeHotMaxEntries)
	}

	if queueFollowingInbox != "" {
		followingInboxCh, err := rabbitConn.Channel()
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("open following inbox consumer channel: %w", err)
		}
		manager.channels = append(manager.channels, followingInboxCh)

		if err := followingInboxCh.Qos(5, 0, false); err != nil {
			manager.Close()
			return nil, fmt.Errorf("set following inbox consumer qos: %w", err)
		}

		deliveries, err := followingInboxCh.Consume(queueFollowingInbox, followingInboxConsumerTag, false, false, false, false, nil)
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("consume following inbox queue: %w", err)
		}

		go consumeFollowingInbox(repo, cache, deliveries, followingPullModeThreshold)
	}

	if queueUserFollow != "" {
		userFollowCh, err := rabbitConn.Channel()
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("open user follow consumer channel: %w", err)
		}
		manager.channels = append(manager.channels, userFollowCh)

		if err := userFollowCh.Qos(10, 0, false); err != nil {
			manager.Close()
			return nil, fmt.Errorf("set user follow consumer qos: %w", err)
		}

		deliveries, err := userFollowCh.Consume(queueUserFollow, userFollowConsumerTag, false, false, false, false, nil)
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("consume user follow queue: %w", err)
		}

		go consumeUserFollow(repo, cache, deliveries)
	}

	return manager, nil
}

func (c *Consumers) Close() error {
	var errs []error
	for _, cancel := range c.cancelFuncs {
		if cancel != nil {
			cancel()
		}
	}
	for _, ch := range c.channels {
		if ch == nil || ch.IsClosed() {
			continue
		}
		if err := ch.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func consumeVideoStats(repo *Repository, cache *feedcache.Cache, deliveries <-chan amqp091.Delivery) {
	for delivery := range deliveries {
		event, err := parseVideoEvent(delivery.Body)
		if err != nil {
			log.Printf("parse video stats event failed: %v", err)
			_ = delivery.Nack(false, false)
			continue
		}

		if err := handleVideoStatsEvent(context.Background(), repo, cache, event); err != nil {
			log.Printf("handle video stats event failed: type=%s video_id=%d err=%v", event.EventType, event.VideoID, err)
			_ = delivery.Nack(false, true)
			continue
		}

		_ = delivery.Ack(false)
	}
}

func consumeVideoHotFeed(repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, deliveries <-chan amqp091.Delivery, homeHotMaxEntries int) {
	for delivery := range deliveries {
		event, err := parseVideoEvent(delivery.Body)
		if err != nil {
			log.Printf("parse video hotfeed event failed: %v", err)
			_ = delivery.Nack(false, false)
			continue
		}

		if err := handleVideoHotFeedEvent(context.Background(), repo, cache, redisClient, event, homeHotMaxEntries); err != nil {
			log.Printf("handle video hotfeed event failed: type=%s video_id=%d err=%v", event.EventType, event.VideoID, err)
			_ = delivery.Nack(false, true)
			continue
		}

		_ = delivery.Ack(false)
	}
}

func consumeFollowingInbox(repo *Repository, cache *feedcache.Cache, deliveries <-chan amqp091.Delivery, followingPullModeThreshold int) {
	for delivery := range deliveries {
		event, err := parseVideoEvent(delivery.Body)
		if err != nil {
			log.Printf("parse following inbox event failed: %v", err)
			_ = delivery.Nack(false, false)
			continue
		}

		if err := handleFollowingInboxEvent(context.Background(), repo, cache, event, followingPullModeThreshold); err != nil {
			log.Printf("handle following inbox event failed: type=%s video_id=%d err=%v", event.EventType, event.VideoID, err)
			_ = delivery.Nack(false, true)
			continue
		}

		_ = delivery.Ack(false)
	}
}

func consumeUserFollow(repo *Repository, cache *feedcache.Cache, deliveries <-chan amqp091.Delivery) {
	for delivery := range deliveries {
		event, err := parseFollowEvent(delivery.Body)
		if err != nil {
			log.Printf("parse user follow event failed: %v", err)
			_ = delivery.Nack(false, false)
			continue
		}

		if err := handleUserFollowEvent(context.Background(), repo, cache, event); err != nil {
			log.Printf("handle user follow event failed: type=%s user_id=%d target_user_id=%d err=%v", event.EventType, event.UserID, event.TargetUserID, err)
			_ = delivery.Nack(false, true)
			continue
		}

		_ = delivery.Ack(false)
	}
}

func parseVideoEvent(body []byte) (*VideoEvent, error) {
	var event VideoEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("unmarshal video event: %w", err)
	}
	if event.VideoID == 0 || event.EventType == "" {
		return nil, fmt.Errorf("video event is invalid")
	}
	return &event, nil
}

func parseFollowEvent(body []byte) (*FollowEvent, error) {
	var event FollowEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("unmarshal follow event: %w", err)
	}
	if event.UserID == 0 || event.TargetUserID == 0 || event.EventType == "" {
		return nil, fmt.Errorf("follow event is invalid")
	}
	return &event, nil
}

func handleVideoStatsEvent(ctx context.Context, repo *Repository, cache *feedcache.Cache, event *VideoEvent) error {
	switch event.EventType {
	case EventTypeVideoLiked,
		EventTypeVideoUnliked,
		EventTypeVideoFavorited,
		EventTypeVideoUnfavorited:
		if err := persistVideoRelationEvent(ctx, repo, event); err != nil {
			return err
		}
		return upsertVideoStatsFromCacheOrMySQL(ctx, repo, cache, event.VideoID)
	case EventTypeVideoCommented,
		EventTypeVideoCommentDeleted:
		return upsertVideoStatsFromCacheOrMySQL(ctx, repo, cache, event.VideoID)
	case EventTypeVideoPublished:
		stats, err := recomputeVideoStats(ctx, repo, event.VideoID)
		if err != nil {
			return err
		}
		if cache != nil && stats != nil {
			return cache.StoreVideoStats(ctx, feedcache.NewVideoStats(stats))
		}
		return nil
	case EventTypeVideoDeleted:
		return nil
	default:
		return nil
	}
}

func recomputeVideoStats(ctx context.Context, repo *Repository, videoID uint64) (*model.VideoStats, error) {
	likeCount, err := repo.CountLikesByVideoID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	favoriteCount, err := repo.CountFavoritesByVideoID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	commentCount, err := repo.CountCommentsByVideoID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	video, err := repo.GetVideoByIDIncludingDeleted(ctx, videoID)
	if err != nil {
		if errors.Is(err, appErrors.ErrVideoNotFound) {
			return nil, nil
		}
		return nil, err
	}

	stats := &model.VideoStats{
		VideoID:       videoID,
		LikeCount:     likeCount,
		CommentCount:  commentCount,
		FavoriteCount: favoriteCount,
		HotScore:      hotscore.Calculate(video.PublishedAt, likeCount, commentCount, favoriteCount),
	}

	if err := repo.UpsertVideoStats(ctx, stats); err != nil {
		return nil, err
	}
	return stats, nil
}

func persistVideoRelationEvent(ctx context.Context, repo *Repository, event *VideoEvent) error {
	switch event.EventType {
	case EventTypeVideoLiked:
		_, err := repo.CreateLikeIfAbsent(ctx, event.OperatorUserID, event.VideoID)
		return err
	case EventTypeVideoUnliked:
		_, err := repo.DeleteLikeIfExists(ctx, event.OperatorUserID, event.VideoID)
		return err
	case EventTypeVideoFavorited:
		_, err := repo.CreateFavoriteIfAbsent(ctx, event.OperatorUserID, event.VideoID)
		return err
	case EventTypeVideoUnfavorited:
		_, err := repo.DeleteFavoriteIfExists(ctx, event.OperatorUserID, event.VideoID)
		return err
	default:
		return nil
	}
}

func upsertVideoStatsFromCacheOrMySQL(ctx context.Context, repo *Repository, cache *feedcache.Cache, videoID uint64) error {
	stats, err := loadVideoStatsFromCacheOrMySQL(ctx, repo, cache, videoID)
	if err != nil {
		return err
	}
	if stats == nil {
		return nil
	}

	video, err := repo.GetVideoByIDIncludingDeleted(ctx, videoID)
	if err != nil {
		if errors.Is(err, appErrors.ErrVideoNotFound) {
			return nil
		}
		return err
	}
	stats.HotScore = hotscore.Calculate(video.PublishedAt, stats.LikeCount, stats.CommentCount, stats.FavoriteCount)
	if cache != nil {
		if err := cache.StoreVideoStats(ctx, feedcache.NewVideoStats(stats)); err != nil {
			return err
		}
	}
	return repo.UpsertVideoStats(ctx, stats)
}

func loadVideoStatsFromCacheOrMySQL(ctx context.Context, repo *Repository, cache *feedcache.Cache, videoID uint64) (*model.VideoStats, error) {
	if cache != nil {
		loaded, missing, err := cache.LoadVideoStatsByVideoIDs(ctx, []uint64{videoID})
		if err != nil {
			log.Printf("load video stats from cache failed, fallback to mysql: video_id=%d err=%v", videoID, err)
			return recomputeVideoStats(ctx, repo, videoID)
		}
		if stats := loaded[videoID]; stats != nil {
			return feedCacheStatsToModel(stats), nil
		}
		if len(missing) == 0 {
			return &model.VideoStats{VideoID: videoID}, nil
		}
	}

	return recomputeVideoStats(ctx, repo, videoID)
}

func feedCacheStatsToModel(stats *feedcache.VideoStats) *model.VideoStats {
	if stats == nil {
		return nil
	}
	return &model.VideoStats{
		VideoID:       stats.VideoID,
		LikeCount:     stats.LikeCount,
		CommentCount:  stats.CommentCount,
		FavoriteCount: stats.FavoriteCount,
		HotScore:      stats.HotScore,
	}
}

func handleVideoHotFeedEvent(ctx context.Context, repo *Repository, cache *feedcache.Cache, redisClient *redis.Client, event *VideoEvent, homeHotMaxEntries int) error {
	if redisClient == nil {
		return nil
	}

	if event.EventType == EventTypeVideoDeleted {
		if err := removeVideoFromHotFeed(ctx, redisClient, event.VideoID); err != nil {
			return err
		}
		if cache != nil {
			if err := cache.DeleteVideoBasesByVideoIDs(ctx, []uint64{event.VideoID}); err != nil {
				return err
			}
			if err := cache.DeleteVideoStatsByVideoIDs(ctx, []uint64{event.VideoID}); err != nil {
				return err
			}
		}
		return nil
	}

	if isCachedVideoStatsEvent(event.EventType) {
		return markVideoHotFeedDirty(ctx, redisClient, event.VideoID)
	}

	video, err := repo.GetVisibleVideoByID(ctx, event.VideoID)
	if err != nil {
		if errors.Is(err, appErrors.ErrVideoNotFound) {
			return removeVideoFromHotFeed(ctx, redisClient, event.VideoID)
		}
		return err
	}

	stats, err := recomputeVideoStats(ctx, repo, event.VideoID)
	if err != nil {
		return err
	}
	if stats == nil {
		return removeVideoFromHotFeed(ctx, redisClient, event.VideoID)
	}

	pipe := redisClient.Pipeline()
	pipe.ZAdd(ctx, homeHotFeedKey, redis.Z{
		Score:  stats.HotScore,
		Member: strconv.FormatUint(video.ID, 10),
	})
	pipe.ZRemRangeByRank(ctx, homeHotFeedKey, 0, -int64(homeHotMaxEntries)-1)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("sync video hotfeed cache: %w", err)
	}

	if cache != nil {
		if err := cache.StoreVideoStats(ctx, feedcache.NewVideoStats(stats)); err != nil {
			return fmt.Errorf("refresh video stats cache: %w", err)
		}
	}

	return nil
}

func isCachedVideoStatsEvent(eventType string) bool {
	switch eventType {
	case EventTypeVideoLiked,
		EventTypeVideoUnliked,
		EventTypeVideoFavorited,
		EventTypeVideoUnfavorited,
		EventTypeVideoCommented,
		EventTypeVideoCommentDeleted:
		return true
	default:
		return false
	}
}

func markVideoHotFeedDirty(ctx context.Context, redisClient *redis.Client, videoID uint64) error {
	if redisClient == nil || videoID == 0 {
		return nil
	}
	if err := redisClient.SAdd(ctx, feedcache.HomeHotDirtyKey(), strconv.FormatUint(videoID, 10)).Err(); err != nil {
		return fmt.Errorf("mark video hotfeed dirty: %w", err)
	}
	return nil
}

func removeVideoFromHotFeed(ctx context.Context, redisClient *redis.Client, videoID uint64) error {
	if err := redisClient.ZRem(ctx, homeHotFeedKey, strconv.FormatUint(videoID, 10)).Err(); err != nil {
		return fmt.Errorf("remove video from hotfeed: %w", err)
	}
	return nil
}

func handleUserFollowEvent(ctx context.Context, repo *Repository, cache *feedcache.Cache, event *FollowEvent) error {
	switch event.EventType {
	case EventTypeUserFollowed:
		currentFollowing, err := repo.IsFollowing(ctx, event.UserID, event.TargetUserID)
		if err != nil {
			return err
		}
		if cache != nil {
			if err := cache.MarkUserActive(ctx, event.UserID); err != nil {
				return err
			}
			if err := cache.SyncFollowRelation(ctx, event.UserID, event.TargetUserID, currentFollowing); err != nil {
				return err
			}
			if !currentFollowing {
				return nil
			}
			if err := syncFollowedPullAuthorForEvent(ctx, cache, event.UserID, event.TargetUserID, true); err != nil {
				return err
			}
			return seedFollowingInboxForUser(ctx, repo, cache, event.UserID, event.TargetUserID)
		}
		return nil
	case EventTypeUserUnfollowed:
		currentFollowing, err := repo.IsFollowing(ctx, event.UserID, event.TargetUserID)
		if err != nil {
			return err
		}
		if cache != nil {
			if err := cache.MarkUserActive(ctx, event.UserID); err != nil {
				return err
			}
			if err := cache.SyncFollowRelation(ctx, event.UserID, event.TargetUserID, currentFollowing); err != nil {
				return err
			}
			if currentFollowing {
				return nil
			}
			if err := syncFollowedPullAuthorForEvent(ctx, cache, event.UserID, event.TargetUserID, false); err != nil {
				return err
			}
			return removeAuthorVideosFromFollowingInbox(ctx, repo, cache, event.UserID, event.TargetUserID)
		}
		return nil
	default:
		return nil
	}
}

func syncFollowedPullAuthorForEvent(ctx context.Context, cache *feedcache.Cache, userID, authorID uint64, following bool) error {
	if cache == nil || userID == 0 || authorID == 0 {
		return nil
	}

	followedPull := false
	if following {
		pullMode, err := cache.IsAuthorPullMode(ctx, authorID)
		if err != nil {
			return err
		}
		followedPull = pullMode
	}
	return cache.SyncFollowedPullAuthor(ctx, userID, authorID, followedPull)
}

func seedFollowingInboxForUser(ctx context.Context, repo *Repository, cache *feedcache.Cache, userID, authorID uint64) error {
	if cache == nil || userID == 0 || authorID == 0 {
		return nil
	}

	pullMode, err := cache.IsAuthorPullMode(ctx, authorID)
	if err != nil {
		return err
	}
	if pullMode {
		return nil
	}

	refs, err := repo.ListRecentVisibleVideoRefsByAuthorID(ctx, authorID, feedcache.FollowingInboxMaxEntries)
	if err != nil {
		return err
	}
	return cache.AddFollowingInboxRefs(ctx, userID, publishedVideoRefsToInboxRefs(refs))
}

func removeAuthorVideosFromFollowingInbox(ctx context.Context, repo *Repository, cache *feedcache.Cache, userID, authorID uint64) error {
	if cache == nil || userID == 0 || authorID == 0 {
		return nil
	}

	refs, err := repo.ListRecentVisibleVideoRefsByAuthorID(ctx, authorID, feedcache.FollowingInboxMaxEntries)
	if err != nil {
		return err
	}

	videoIDs := make([]uint64, 0, len(refs))
	for _, ref := range refs {
		if ref.VideoID == 0 {
			continue
		}
		videoIDs = append(videoIDs, ref.VideoID)
	}
	if len(videoIDs) == 0 {
		return nil
	}
	return cache.RemoveFollowingInboxVideos(ctx, userID, videoIDs)
}

func handleFollowingInboxEvent(ctx context.Context, repo *Repository, cache *feedcache.Cache, event *VideoEvent, followingPullModeThreshold int) error {
	if cache == nil {
		return nil
	}

	switch event.EventType {
	case EventTypeVideoPublished:
		video, err := repo.GetVisibleVideoByID(ctx, event.VideoID)
		if err != nil {
			if errors.Is(err, appErrors.ErrVideoNotFound) {
				return nil
			}
			return err
		}
		currentRef := feedcache.FollowingInboxRef{
			VideoID:     video.ID,
			PublishedAt: video.PublishedAt,
		}

		followerCount, err := repo.CountFollowersByAuthorID(ctx, video.AuthorID)
		if err != nil {
			return err
		}
		wasPullMode, err := cache.IsAuthorPullMode(ctx, video.AuthorID)
		if err != nil {
			return err
		}
		pullMode := followerCount >= int64(followingPullModeThreshold)
		if pullMode {
			if !wasPullMode {
				if err := syncAuthorOutbox(ctx, repo, cache, video.AuthorID); err != nil {
					return err
				}
				activeFollowerIDs, activeErr := loadActiveFollowerIDs(ctx, repo, cache, video.AuthorID)
				if activeErr != nil {
					return activeErr
				}
				if err := cache.AddFollowedPullAuthorForUsers(ctx, activeFollowerIDs, video.AuthorID); err != nil {
					return err
				}
			} else if err := cache.AddAuthorOutboxRefs(ctx, video.AuthorID, []feedcache.FollowingInboxRef{currentRef}); err != nil {
				return err
			}
			return cache.SetAuthorPullMode(ctx, video.AuthorID, true)
		}

		if wasPullMode {
			refs, listErr := repo.ListRecentVisibleVideoRefsByAuthorID(ctx, video.AuthorID, feedcache.FollowingInboxMaxEntries)
			if listErr != nil {
				return listErr
			}
			activeFollowerIDs, activeErr := loadActiveFollowerIDs(ctx, repo, cache, video.AuthorID)
			if activeErr != nil {
				return activeErr
			}
			if len(activeFollowerIDs) > 0 {
				if err := cache.FanoutFollowingInboxRefs(ctx, activeFollowerIDs, publishedVideoRefsToInboxRefs(refs)); err != nil {
					return err
				}
				if err := cache.RemoveFollowedPullAuthorForUsers(ctx, activeFollowerIDs, video.AuthorID); err != nil {
					return err
				}
			}
			if err := cache.SetAuthorPullMode(ctx, video.AuthorID, false); err != nil {
				return err
			}
			return cache.DeleteAuthorOutbox(ctx, video.AuthorID)
		}

		if err := cache.DeleteAuthorOutbox(ctx, video.AuthorID); err != nil {
			return err
		}
		return fanoutFollowingInboxRefsToActiveFollowers(ctx, repo, cache, video.AuthorID, []feedcache.FollowingInboxRef{currentRef})

	case EventTypeVideoDeleted:
		authorID := event.OperatorUserID
		if authorID == 0 {
			video, err := repo.GetVideoByIDIncludingDeleted(ctx, event.VideoID)
			if err != nil {
				if errors.Is(err, appErrors.ErrVideoNotFound) {
					return nil
				}
				return err
			}
			authorID = video.AuthorID
		}
		pullMode, err := cache.IsAuthorPullMode(ctx, authorID)
		if err != nil {
			return err
		}
		if pullMode {
			return cache.RemoveAuthorOutboxVideo(ctx, authorID, event.VideoID)
		}
		return removeFollowingInboxVideoFromActiveFollowers(ctx, repo, cache, authorID, event.VideoID)

	default:
		return nil
	}
}

func fanoutFollowingInboxRefsToActiveFollowers(ctx context.Context, repo *Repository, cache *feedcache.Cache, authorID uint64, refs []feedcache.FollowingInboxRef) error {
	if len(refs) == 0 {
		return nil
	}

	activeFollowerIDs, err := loadActiveFollowerIDs(ctx, repo, cache, authorID)
	if err != nil {
		return err
	}
	if len(activeFollowerIDs) == 0 {
		return nil
	}

	return cache.FanoutFollowingInboxRefs(ctx, activeFollowerIDs, refs)
}

func removeFollowingInboxVideoFromActiveFollowers(ctx context.Context, repo *Repository, cache *feedcache.Cache, authorID, videoID uint64) error {
	activeFollowerIDs, err := loadActiveFollowerIDs(ctx, repo, cache, authorID)
	if err != nil {
		return err
	}
	if len(activeFollowerIDs) == 0 {
		return nil
	}

	return cache.RemoveFollowingInboxVideo(ctx, activeFollowerIDs, videoID)
}

func loadActiveFollowerIDs(ctx context.Context, repo *Repository, cache *feedcache.Cache, authorID uint64) ([]uint64, error) {
	followerIDs, err := repo.ListFollowerUserIDsByAuthorID(ctx, authorID)
	if err != nil {
		return nil, err
	}
	if len(followerIDs) == 0 {
		return nil, nil
	}

	activeFollowerIDs, err := cache.FilterActiveUserIDs(ctx, followerIDs)
	if err != nil {
		return nil, err
	}
	return activeFollowerIDs, nil
}

func syncAuthorOutbox(ctx context.Context, repo *Repository, cache *feedcache.Cache, authorID uint64) error {
	refs, err := repo.ListRecentVisibleVideoRefsByAuthorID(ctx, authorID, feedcache.FollowingInboxMaxEntries)
	if err != nil {
		return err
	}
	return cache.ReplaceAuthorOutboxRefs(ctx, authorID, publishedVideoRefsToInboxRefs(refs))
}

func publishedVideoRefsToInboxRefs(refs []publishedVideoRef) []feedcache.FollowingInboxRef {
	inboxRefs := make([]feedcache.FollowingInboxRef, 0, len(refs))
	for _, ref := range refs {
		if ref.VideoID == 0 || ref.PublishedAt.IsZero() {
			continue
		}
		inboxRefs = append(inboxRefs, feedcache.FollowingInboxRef{
			VideoID:     ref.VideoID,
			PublishedAt: ref.PublishedAt,
		})
	}
	return inboxRefs
}
