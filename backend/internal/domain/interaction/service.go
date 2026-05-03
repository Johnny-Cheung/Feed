package interaction

import (
	"context"
	"log"
	"time"

	appErrors "feed-backend/internal/common/errors"
	videoDomain "feed-backend/internal/domain/video"
	"feed-backend/internal/infra/feedcache"
	filestorage "feed-backend/internal/infra/storage"
	"feed-backend/internal/model"
)

type Service struct {
	repo                      *Repository
	publisher                 *Publisher
	cache                     *feedcache.Cache
	staticBaseURL             string
	videoRelationStreamMaxLen int64
	hotCommentCacheEntries    int
	hotCommentCacheTTL        time.Duration
}

func NewService(repo *Repository, publisher *Publisher, cache *feedcache.Cache, staticBaseURL string, videoRelationStreamMaxLen int, hotCommentCacheEntries int, hotCommentCacheTTL time.Duration) *Service {
	return &Service{
		repo:                      repo,
		publisher:                 publisher,
		cache:                     cache,
		staticBaseURL:             staticBaseURL,
		videoRelationStreamMaxLen: normalizeVideoRelationStreamMaxLen(videoRelationStreamMaxLen),
		hotCommentCacheEntries:    normalizeHotCommentCacheEntries(hotCommentCacheEntries),
		hotCommentCacheTTL:        normalizeHotCommentCacheTTL(hotCommentCacheTTL),
	}
}

func normalizeVideoRelationStreamMaxLen(value int) int64 {
	if value <= 0 {
		return feedcache.VideoRelationStreamDefaultMaxLen
	}
	return int64(value)
}

func normalizeHotCommentCacheEntries(value int) int {
	if value <= 0 {
		return feedcache.DefaultHotCommentCacheEntries
	}
	return value
}

func normalizeHotCommentCacheTTL(value time.Duration) time.Duration {
	if value <= 0 {
		return feedcache.DefaultHotCommentCacheTTL
	}
	return value
}

func (s *Service) newVideoRelationStreamEvent(eventType, relationType string, userID, videoID uint64, active bool) feedcache.VideoRelationStreamEvent {
	return feedcache.VideoRelationStreamEvent{
		EventID:      newEventID(),
		EventType:    eventType,
		RelationType: relationType,
		UserID:       userID,
		VideoID:      videoID,
		Active:       active,
		OccurredAt:   time.Now().UTC(),
		MaxLen:       s.videoRelationStreamMaxLen,
	}
}

func (s *Service) LikeVideo(ctx context.Context, userID, videoID uint64) (*ToggleLikeResponse, error) {
	if s.cache == nil {
		return s.likeVideoMySQL(ctx, userID, videoID)
	}

	video, err := s.loadVisibleVideoBase(ctx, videoID)
	if err != nil {
		return nil, err
	}

	stats, err := s.loadVideoStatsSeed(ctx, videoID)
	if err != nil {
		log.Printf("load video stats seed failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.likeVideoMySQL(ctx, userID, videoID)
	}

	if err := s.cache.EnsureLikeRelationCached(ctx, userID, videoID, video.PublishedAt); err != nil {
		log.Printf("ensure like relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.likeVideoMySQL(ctx, userID, videoID)
	}

	changed, _, err := s.cache.ToggleLikeRelationAndStats(
		ctx,
		userID,
		videoID,
		video.PublishedAt,
		true,
		stats,
		s.newVideoRelationStreamEvent(EventTypeVideoLiked, feedcache.VideoRelationTypeLike, userID, videoID, true),
	)
	if err != nil {
		log.Printf("toggle like relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.likeVideoMySQL(ctx, userID, videoID)
	}
	if changed {
		s.markHomeHotDirtyBestEffort(ctx, videoID)
	}

	return &ToggleLikeResponse{Liked: true}, nil
}

func (s *Service) UnlikeVideo(ctx context.Context, userID, videoID uint64) (*ToggleLikeResponse, error) {
	if s.cache == nil {
		return s.unlikeVideoMySQL(ctx, userID, videoID)
	}

	video, err := s.loadVisibleVideoBase(ctx, videoID)
	if err != nil {
		return nil, err
	}

	stats, err := s.loadVideoStatsSeed(ctx, videoID)
	if err != nil {
		log.Printf("load video stats seed failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.unlikeVideoMySQL(ctx, userID, videoID)
	}

	if err := s.cache.EnsureLikeRelationCached(ctx, userID, videoID, video.PublishedAt); err != nil {
		log.Printf("ensure like relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.unlikeVideoMySQL(ctx, userID, videoID)
	}

	changed, _, err := s.cache.ToggleLikeRelationAndStats(
		ctx,
		userID,
		videoID,
		video.PublishedAt,
		false,
		stats,
		s.newVideoRelationStreamEvent(EventTypeVideoUnliked, feedcache.VideoRelationTypeLike, userID, videoID, false),
	)
	if err != nil {
		log.Printf("toggle like relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.unlikeVideoMySQL(ctx, userID, videoID)
	}
	if changed {
		s.markHomeHotDirtyBestEffort(ctx, videoID)
	}

	return &ToggleLikeResponse{Liked: false}, nil
}

func (s *Service) FavoriteVideo(ctx context.Context, userID, videoID uint64) (*ToggleFavoriteResponse, error) {
	if s.cache == nil {
		return s.favoriteVideoMySQL(ctx, userID, videoID)
	}

	video, err := s.loadVisibleVideoBase(ctx, videoID)
	if err != nil {
		return nil, err
	}

	stats, err := s.loadVideoStatsSeed(ctx, videoID)
	if err != nil {
		log.Printf("load video stats seed failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.favoriteVideoMySQL(ctx, userID, videoID)
	}

	if err := s.cache.EnsureFavoriteRelationCached(ctx, userID, videoID, video.PublishedAt); err != nil {
		log.Printf("ensure favorite relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.favoriteVideoMySQL(ctx, userID, videoID)
	}

	changed, _, err := s.cache.ToggleFavoriteRelationAndStats(
		ctx,
		userID,
		videoID,
		video.PublishedAt,
		true,
		stats,
		s.newVideoRelationStreamEvent(EventTypeVideoFavorited, feedcache.VideoRelationTypeFavorite, userID, videoID, true),
	)
	if err != nil {
		log.Printf("toggle favorite relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.favoriteVideoMySQL(ctx, userID, videoID)
	}
	if changed {
		s.markHomeHotDirtyBestEffort(ctx, videoID)
	}

	return &ToggleFavoriteResponse{Favorited: true}, nil
}

func (s *Service) UnfavoriteVideo(ctx context.Context, userID, videoID uint64) (*ToggleFavoriteResponse, error) {
	if s.cache == nil {
		return s.unfavoriteVideoMySQL(ctx, userID, videoID)
	}

	video, err := s.loadVisibleVideoBase(ctx, videoID)
	if err != nil {
		return nil, err
	}

	stats, err := s.loadVideoStatsSeed(ctx, videoID)
	if err != nil {
		log.Printf("load video stats seed failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.unfavoriteVideoMySQL(ctx, userID, videoID)
	}

	if err := s.cache.EnsureFavoriteRelationCached(ctx, userID, videoID, video.PublishedAt); err != nil {
		log.Printf("ensure favorite relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.unfavoriteVideoMySQL(ctx, userID, videoID)
	}

	changed, _, err := s.cache.ToggleFavoriteRelationAndStats(
		ctx,
		userID,
		videoID,
		video.PublishedAt,
		false,
		stats,
		s.newVideoRelationStreamEvent(EventTypeVideoUnfavorited, feedcache.VideoRelationTypeFavorite, userID, videoID, false),
	)
	if err != nil {
		log.Printf("toggle favorite relation cache failed, fallback to mysql: user_id=%d video_id=%d err=%v", userID, videoID, err)
		return s.unfavoriteVideoMySQL(ctx, userID, videoID)
	}
	if changed {
		s.markHomeHotDirtyBestEffort(ctx, videoID)
	}

	return &ToggleFavoriteResponse{Favorited: false}, nil
}

func (s *Service) FollowUser(ctx context.Context, userID, targetUserID uint64) (*ToggleFollowResponse, error) {
	if targetUserID == 0 {
		return nil, appErrors.ErrInvalidParams
	}
	if userID == targetUserID {
		return nil, appErrors.ErrCannotFollowSelf
	}
	if _, err := s.repo.GetUserByID(ctx, targetUserID); err != nil {
		return nil, err
	}

	changed, err := s.repo.CreateFollowIfAbsent(ctx, userID, targetUserID)
	if err != nil {
		return nil, err
	}

	s.markUserActiveBestEffort(ctx, userID)
	s.syncFollowRelationBestEffort(ctx, userID, targetUserID, true)
	if changed {
		s.publishBestEffort(ctx, func() error {
			return s.publisher.PublishUserFollowed(ctx, userID, targetUserID)
		}, "user.followed", targetUserID)
	}

	return &ToggleFollowResponse{Following: true}, nil
}

func (s *Service) UnfollowUser(ctx context.Context, userID, targetUserID uint64) (*ToggleFollowResponse, error) {
	if targetUserID == 0 {
		return nil, appErrors.ErrInvalidParams
	}
	if userID == targetUserID {
		return nil, appErrors.ErrCannotFollowSelf
	}
	if _, err := s.repo.GetUserByID(ctx, targetUserID); err != nil {
		return nil, err
	}

	changed, err := s.repo.DeleteFollowIfExists(ctx, userID, targetUserID)
	if err != nil {
		return nil, err
	}

	s.markUserActiveBestEffort(ctx, userID)
	s.syncFollowRelationBestEffort(ctx, userID, targetUserID, false)
	if changed {
		s.publishBestEffort(ctx, func() error {
			return s.publisher.PublishUserUnfollowed(ctx, userID, targetUserID)
		}, "user.unfollowed", targetUserID)
	}

	return &ToggleFollowResponse{Following: false}, nil
}

func (s *Service) CreateComment(ctx context.Context, userID, videoID uint64, req CreateCommentRequest) (*CommentItem, error) {
	content := normalizeCommentContent(req.Content)
	if content == "" || len(content) > 500 {
		return nil, appErrors.ErrInvalidParams
	}

	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	comment := &model.Comment{
		VideoID: videoID,
		UserID:  userID,
		Content: content,
		Status:  model.CommentStatusNormal,
	}
	if err := s.repo.CreateComment(ctx, comment); err != nil {
		return nil, err
	}
	s.syncCommentCountBestEffort(ctx, video.ID, 1)
	s.deleteTopCommentsCacheBestEffort(ctx, video.ID)

	s.publishBestEffort(ctx, func() error {
		return s.publisher.PublishVideoCommented(ctx, videoID, userID)
	}, "video.commented", videoID)

	return s.buildCommentItem(comment, user), nil
}

func (s *Service) DeleteComment(ctx context.Context, userID, commentID uint64) error {
	comment, err := s.repo.GetActiveCommentByID(ctx, commentID)
	if err != nil {
		return err
	}
	if comment.UserID != userID {
		return appErrors.ErrCannotDeleteComment
	}

	changed, err := s.repo.SoftDeleteCommentByID(ctx, commentID, userID)
	if err != nil {
		return err
	}
	if !changed {
		return appErrors.ErrCommentNotFound
	}
	s.syncCommentCountBestEffort(ctx, comment.VideoID, -1)
	s.deleteTopCommentsCacheBestEffort(ctx, comment.VideoID)

	s.publishBestEffort(ctx, func() error {
		return s.publisher.PublishVideoCommentDeleted(ctx, comment.VideoID, userID)
	}, "video.comment_deleted", comment.VideoID)

	return nil
}

func (s *Service) ListComments(ctx context.Context, videoID uint64, rawCursor string, requestedLimit int) (*CommentPageResponse, error) {
	cursor, err := decodeCommentCursor(rawCursor)
	if err != nil {
		return nil, appErrors.ErrInvalidParams
	}

	limit := normalizeLimit(requestedLimit, 10, 20)

	if hot, cacheErr := s.isHomeHotVideo(ctx, videoID); cacheErr != nil {
		log.Printf("check home hot video for comments failed, fallback to mysql: video_id=%d err=%v", videoID, cacheErr)
	} else if hot {
		if page, ok, err := s.listHotCachedComments(ctx, videoID, cursor, limit); err != nil {
			log.Printf("list hot comments cache failed, fallback to mysql: video_id=%d err=%v", videoID, err)
		} else if ok {
			return page, nil
		}
		return s.listCommentsFromMySQL(ctx, videoID, cursor, limit)
	}

	if _, err := s.repo.GetVisibleVideoByID(ctx, videoID); err != nil {
		return nil, err
	}

	return s.listCommentsFromMySQL(ctx, videoID, cursor, limit)
}

func (s *Service) isHomeHotVideo(ctx context.Context, videoID uint64) (bool, error) {
	if s.cache == nil || s.hotCommentCacheEntries <= 0 || s.hotCommentCacheTTL <= 0 {
		return false, nil
	}
	return s.cache.IsHomeHotVideo(ctx, videoID)
}

func (s *Service) listCommentsFromMySQL(ctx context.Context, videoID uint64, cursor *commentCursor, limit int) (*CommentPageResponse, error) {
	comments, err := s.repo.ListActiveCommentsByVideoID(ctx, videoID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return s.buildCommentPageFromModels(ctx, comments, limit)
}

func (s *Service) listHotCachedComments(ctx context.Context, videoID uint64, cursor *commentCursor, limit int) (*CommentPageResponse, bool, error) {
	if s.cache == nil || s.hotCommentCacheEntries <= 0 || s.hotCommentCacheTTL <= 0 {
		return nil, false, nil
	}

	comments, hit, err := s.cache.LoadTopComments(ctx, videoID)
	if err != nil {
		return nil, false, err
	}
	if !hit {
		models, err := s.repo.ListActiveCommentsByVideoID(ctx, videoID, nil, s.hotCommentCacheEntries)
		if err != nil {
			return nil, false, err
		}
		comments = commentBriefsFromModels(models)
		if err := s.attachUserBriefsToComments(ctx, comments); err != nil {
			return nil, false, err
		}
		if err := s.cache.StoreTopComments(ctx, videoID, comments, s.hotCommentCacheEntries, s.hotCommentCacheTTL); err != nil {
			log.Printf("store top comments cache failed: video_id=%d err=%v", videoID, err)
		}
	}

	pageComments, ok := sliceCachedCommentBriefs(comments, cursor, limit, s.hotCommentCacheEntries)
	if !ok {
		return nil, false, nil
	}

	page, err := s.buildCommentPageFromBriefs(ctx, pageComments, limit)
	if err != nil {
		return nil, false, err
	}
	return page, true, nil
}

func (s *Service) buildCommentPageFromModels(ctx context.Context, comments []model.Comment, limit int) (*CommentPageResponse, error) {
	userIDs := uniqueCommentUserIDs(comments)
	users, err := s.repo.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	userMap := make(map[uint64]*model.User, len(users))
	for i := range users {
		user := users[i]
		userMap[user.ID] = &user
	}

	items := make([]*CommentItem, 0, len(comments))
	keptComments := make([]model.Comment, 0, len(comments))
	for _, comment := range comments {
		user := userMap[comment.UserID]
		if user == nil {
			continue
		}
		commentCopy := comment
		items = append(items, s.buildCommentItem(&commentCopy, user))
		keptComments = append(keptComments, comment)
	}

	return buildCommentPage(items, keptComments, limit, func(comment model.Comment) commentCursor {
		return commentCursor{
			Time: comment.CreatedAt,
			ID:   comment.ID,
		}
	})
}

func (s *Service) buildCommentPageFromBriefs(ctx context.Context, comments []feedcache.CommentBrief, limit int) (*CommentPageResponse, error) {
	userMap := map[uint64]*feedcache.UserBrief{}
	missingUserIDs := uniqueMissingBriefUserIDs(comments)
	if len(missingUserIDs) > 0 {
		var err error
		userMap, err = s.loadCommentUserBriefs(ctx, missingUserIDs)
		if err != nil {
			return nil, err
		}
	}

	items := make([]*CommentItem, 0, len(comments))
	keptComments := make([]feedcache.CommentBrief, 0, len(comments))
	for _, comment := range comments {
		user := comment.User
		if user == nil {
			user = userMap[comment.UserID]
		}
		if user == nil {
			continue
		}
		commentCopy := comment
		items = append(items, s.buildCommentItemFromBrief(&commentCopy, user))
		keptComments = append(keptComments, comment)
	}

	return buildCommentPage(items, keptComments, limit, func(comment feedcache.CommentBrief) commentCursor {
		return commentCursor{
			Time: comment.CreatedAt,
			ID:   comment.ID,
		}
	})
}

func (s *Service) loadCommentUserBriefs(ctx context.Context, userIDs []uint64) (map[uint64]*feedcache.UserBrief, error) {
	if len(userIDs) == 0 {
		return map[uint64]*feedcache.UserBrief{}, nil
	}

	if s.cache != nil {
		loaded, missing, err := s.cache.LoadUserBriefsByUserIDs(ctx, userIDs)
		if err == nil {
			if len(missing) > 0 {
				built, buildErr := s.cache.BuildUserBriefsByUserIDs(ctx, missing)
				if buildErr != nil {
					log.Printf("build comment user brief cache failed, fallback to mysql: err=%v", buildErr)
					return s.loadCommentUserBriefsFromMySQL(ctx, userIDs)
				}
				for userID, brief := range built {
					loaded[userID] = brief
				}
				if err := s.cache.StoreUserBriefs(ctx, built); err != nil {
					log.Printf("store comment user brief cache failed: err=%v", err)
				}
			}
			return loaded, nil
		}
		log.Printf("load comment user brief cache failed, fallback to mysql: err=%v", err)
	}

	return s.loadCommentUserBriefsFromMySQL(ctx, userIDs)
}

func (s *Service) loadCommentUserBriefsFromMySQL(ctx context.Context, userIDs []uint64) (map[uint64]*feedcache.UserBrief, error) {
	users, err := s.repo.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	result := make(map[uint64]*feedcache.UserBrief, len(users))
	for i := range users {
		user := users[i]
		brief := feedcache.NewUserBrief(&user)
		if brief == nil {
			continue
		}
		result[brief.UserID] = brief
	}
	return result, nil
}

func (s *Service) attachUserBriefsToComments(ctx context.Context, comments []feedcache.CommentBrief) error {
	if len(comments) == 0 {
		return nil
	}

	userIDs := uniqueBriefUserIDs(comments)
	userMap, err := s.loadCommentUserBriefs(ctx, userIDs)
	if err != nil {
		return err
	}

	for i := range comments {
		comments[i].User = userMap[comments[i].UserID]
	}
	return nil
}

func buildCommentPage[T any](items []*CommentItem, keptComments []T, limit int, cursorFor func(T) commentCursor) (*CommentPageResponse, error) {
	hasMore := len(keptComments) > limit
	if hasMore {
		items = items[:limit]
		keptComments = keptComments[:limit]
	}

	nextCursor := ""
	if hasMore {
		cursor := cursorFor(keptComments[len(keptComments)-1])
		encoded, err := encodeCommentCursor(cursor)
		if err != nil {
			return nil, err
		}
		nextCursor = encoded
	}

	return &CommentPageResponse{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func commentBriefsFromModels(comments []model.Comment) []feedcache.CommentBrief {
	briefs := make([]feedcache.CommentBrief, 0, len(comments))
	for i := range comments {
		brief := feedcache.NewCommentBrief(&comments[i])
		if brief == nil {
			continue
		}
		briefs = append(briefs, *brief)
	}
	return briefs
}

func uniqueCommentUserIDs(comments []model.Comment) []uint64 {
	userIDs := make([]uint64, 0, len(comments))
	seen := make(map[uint64]struct{}, len(comments))
	for _, comment := range comments {
		if comment.UserID == 0 {
			continue
		}
		if _, exists := seen[comment.UserID]; exists {
			continue
		}
		seen[comment.UserID] = struct{}{}
		userIDs = append(userIDs, comment.UserID)
	}
	return userIDs
}

func uniqueBriefUserIDs(comments []feedcache.CommentBrief) []uint64 {
	userIDs := make([]uint64, 0, len(comments))
	seen := make(map[uint64]struct{}, len(comments))
	for _, comment := range comments {
		if comment.UserID == 0 {
			continue
		}
		if _, exists := seen[comment.UserID]; exists {
			continue
		}
		seen[comment.UserID] = struct{}{}
		userIDs = append(userIDs, comment.UserID)
	}
	return userIDs
}

func uniqueMissingBriefUserIDs(comments []feedcache.CommentBrief) []uint64 {
	userIDs := make([]uint64, 0, len(comments))
	seen := make(map[uint64]struct{}, len(comments))
	for _, comment := range comments {
		if comment.UserID == 0 || comment.User != nil {
			continue
		}
		if _, exists := seen[comment.UserID]; exists {
			continue
		}
		seen[comment.UserID] = struct{}{}
		userIDs = append(userIDs, comment.UserID)
	}
	return userIDs
}

func sliceCachedCommentBriefs(comments []feedcache.CommentBrief, cursor *commentCursor, limit, maxEntries int) ([]feedcache.CommentBrief, bool) {
	if limit <= 0 {
		return nil, true
	}

	start := 0
	if cursor != nil {
		start = -1
		for i, comment := range comments {
			if commentBriefAfterCursor(comment, cursor) {
				start = i
				break
			}
		}
		if start < 0 {
			return nil, len(comments) < maxEntries
		}
	}

	needCount := limit + 1
	end := start + needCount
	if end <= len(comments) {
		return comments[start:end], true
	}
	if len(comments) < maxEntries {
		return comments[start:], true
	}
	return nil, false
}

func commentBriefAfterCursor(comment feedcache.CommentBrief, cursor *commentCursor) bool {
	if cursor == nil {
		return true
	}

	createdAt := comment.CreatedAt.UTC().Round(0)
	cursorTime := cursor.Time.UTC().Round(0)
	if createdAt.Before(cursorTime) {
		return true
	}
	return createdAt.Equal(cursorTime) && comment.ID < cursor.ID
}

func (s *Service) likeVideoMySQL(ctx context.Context, userID, videoID uint64) (*ToggleLikeResponse, error) {
	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	_, err = s.repo.CreateLikeIfAbsent(ctx, userID, videoID)
	if err != nil {
		return nil, err
	}
	s.syncLikeRelationBestEffort(ctx, userID, videoID, video.PublishedAt, true)

	return &ToggleLikeResponse{Liked: true}, nil
}

func (s *Service) unlikeVideoMySQL(ctx context.Context, userID, videoID uint64) (*ToggleLikeResponse, error) {
	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	_, err = s.repo.DeleteLikeIfExists(ctx, userID, videoID)
	if err != nil {
		return nil, err
	}
	s.syncLikeRelationBestEffort(ctx, userID, videoID, video.PublishedAt, false)

	return &ToggleLikeResponse{Liked: false}, nil
}

func (s *Service) favoriteVideoMySQL(ctx context.Context, userID, videoID uint64) (*ToggleFavoriteResponse, error) {
	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	_, err = s.repo.CreateFavoriteIfAbsent(ctx, userID, videoID)
	if err != nil {
		return nil, err
	}
	s.syncFavoriteRelationBestEffort(ctx, userID, videoID, video.PublishedAt, true)

	return &ToggleFavoriteResponse{Favorited: true}, nil
}

func (s *Service) unfavoriteVideoMySQL(ctx context.Context, userID, videoID uint64) (*ToggleFavoriteResponse, error) {
	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	_, err = s.repo.DeleteFavoriteIfExists(ctx, userID, videoID)
	if err != nil {
		return nil, err
	}
	s.syncFavoriteRelationBestEffort(ctx, userID, videoID, video.PublishedAt, false)

	return &ToggleFavoriteResponse{Favorited: false}, nil
}

func (s *Service) loadVisibleVideoBase(ctx context.Context, videoID uint64) (*feedcache.VideoBase, error) {
	if s.cache != nil {
		loaded, missing, err := s.cache.LoadVideoBasesByVideoIDs(ctx, []uint64{videoID})
		if err == nil {
			if base := loaded[videoID]; base != nil {
				return base, nil
			}
			if len(missing) > 0 {
				built, buildErr := s.cache.BuildVideoBasesByVideoIDs(ctx, []uint64{videoID})
				if buildErr != nil {
					return nil, buildErr
				}
				if base := built[videoID]; base != nil {
					if storeErr := s.cache.StoreVideoBase(ctx, base); storeErr != nil {
						log.Printf("store video base cache failed: video_id=%d err=%v", videoID, storeErr)
					}
					return base, nil
				}
			}
			return nil, appErrors.ErrVideoNotFound
		}
		log.Printf("load video base cache failed, fallback to mysql: video_id=%d err=%v", videoID, err)
	}

	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}
	return feedcache.NewVideoBase(video), nil
}

func (s *Service) loadVideoStatsSeed(ctx context.Context, videoID uint64) (*feedcache.VideoStats, error) {
	if s.cache == nil {
		return &feedcache.VideoStats{VideoID: videoID}, nil
	}

	loaded, missing, err := s.cache.LoadVideoStatsByVideoIDs(ctx, []uint64{videoID})
	if err != nil {
		return nil, err
	}
	if stats := loaded[videoID]; stats != nil {
		return stats, nil
	}
	if len(missing) == 0 {
		return &feedcache.VideoStats{VideoID: videoID}, nil
	}

	built, err := s.cache.BuildVideoStatsByVideoIDs(ctx, []uint64{videoID})
	if err != nil {
		return nil, err
	}
	if stats := built[videoID]; stats != nil {
		return stats, nil
	}
	return &feedcache.VideoStats{VideoID: videoID}, nil
}

func (s *Service) syncCommentCountBestEffort(ctx context.Context, videoID uint64, delta int64) {
	if s.cache == nil || videoID == 0 || delta == 0 {
		return
	}

	stats, err := s.loadVideoStatsSeed(ctx, videoID)
	if err != nil {
		log.Printf("load video stats seed for comment count failed: video_id=%d delta=%d err=%v", videoID, delta, err)
		s.deleteVideoStatsCacheBestEffort(ctx, videoID)
		return
	}

	if _, err := s.cache.IncrementVideoCommentCount(ctx, videoID, delta, stats); err != nil {
		log.Printf("sync comment count cache failed: video_id=%d delta=%d err=%v", videoID, delta, err)
		s.deleteVideoStatsCacheBestEffort(ctx, videoID)
		return
	}
	s.markHomeHotDirtyBestEffort(ctx, videoID)
}

func (s *Service) deleteVideoStatsCacheBestEffort(ctx context.Context, videoID uint64) {
	if s.cache == nil || videoID == 0 {
		return
	}
	if err := s.cache.DeleteVideoStatsByVideoIDs(ctx, []uint64{videoID}); err != nil {
		log.Printf("delete video stats cache failed: video_id=%d err=%v", videoID, err)
	}
}

func (s *Service) markHomeHotDirtyBestEffort(ctx context.Context, videoID uint64) {
	if s.cache == nil || videoID == 0 {
		return
	}
	if err := s.cache.MarkHomeHotDirty(ctx, videoID); err != nil {
		log.Printf("mark home hot dirty failed: video_id=%d err=%v", videoID, err)
	}
}

func (s *Service) deleteTopCommentsCacheBestEffort(ctx context.Context, videoID uint64) {
	if s.cache == nil || videoID == 0 {
		return
	}
	if err := s.cache.DeleteTopComments(ctx, videoID); err != nil {
		log.Printf("delete top comments cache failed: video_id=%d err=%v", videoID, err)
	}
}

func (s *Service) buildCommentItem(comment *model.Comment, user *model.User) *CommentItem {
	return &CommentItem{
		ID:        comment.ID,
		Content:   comment.Content,
		CreatedAt: comment.CreatedAt,
		User: videoDomain.UserSummary{
			ID:        user.ID,
			Nickname:  user.Nickname,
			AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, user.AvatarPath),
		},
	}
}

func (s *Service) buildCommentItemFromBrief(comment *feedcache.CommentBrief, user *feedcache.UserBrief) *CommentItem {
	return &CommentItem{
		ID:        comment.ID,
		Content:   comment.Content,
		CreatedAt: comment.CreatedAt,
		User: videoDomain.UserSummary{
			ID:        user.UserID,
			Nickname:  user.Nickname,
			AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, user.AvatarPath),
		},
	}
}

func (s *Service) publishBestEffort(ctx context.Context, publish func() error, eventType string, videoID uint64) {
	if publish == nil {
		return
	}
	if err := publish(); err != nil {
		log.Printf("publish %s failed: entity_id=%d err=%v", eventType, videoID, err)
	}
}

func (s *Service) syncLikeRelationBestEffort(ctx context.Context, userID, videoID uint64, publishedAt time.Time, liked bool) {
	if s.cache == nil {
		return
	}
	if err := s.cache.SyncLikeRelation(ctx, userID, videoID, publishedAt, liked); err != nil {
		log.Printf("sync like relation cache failed: user_id=%d video_id=%d err=%v", userID, videoID, err)
	}
}

func (s *Service) syncFavoriteRelationBestEffort(ctx context.Context, userID, videoID uint64, publishedAt time.Time, favorited bool) {
	if s.cache == nil {
		return
	}
	if err := s.cache.SyncFavoriteRelation(ctx, userID, videoID, publishedAt, favorited); err != nil {
		log.Printf("sync favorite relation cache failed: user_id=%d video_id=%d err=%v", userID, videoID, err)
	}
}

func (s *Service) syncFollowRelationBestEffort(ctx context.Context, userID, authorID uint64, following bool) {
	if s.cache == nil {
		return
	}
	if err := s.cache.SyncFollowRelation(ctx, userID, authorID, following); err != nil {
		log.Printf("sync follow relation cache failed: user_id=%d author_id=%d err=%v", userID, authorID, err)
	}
}

func (s *Service) markUserActiveBestEffort(ctx context.Context, userID uint64) {
	if s.cache == nil || userID == 0 {
		return
	}
	if err := s.cache.MarkUserActive(ctx, userID); err != nil {
		log.Printf("mark user active failed: user_id=%d err=%v", userID, err)
	}
}

func normalizeLimit(requestedLimit, defaultLimit, maxLimit int) int {
	if requestedLimit <= 0 {
		return defaultLimit
	}
	if requestedLimit > maxLimit {
		return maxLimit
	}
	return requestedLimit
}
