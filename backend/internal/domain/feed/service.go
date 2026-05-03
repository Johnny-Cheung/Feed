package feed

import (
	"context"
	"log"
	"sort"
	"time"

	appErrors "feed-backend/internal/common/errors"
	videoDomain "feed-backend/internal/domain/video"
	"feed-backend/internal/infra/feedcache"
	filestorage "feed-backend/internal/infra/storage"
	"feed-backend/internal/model"
)

type Service struct {
	repo          *Repository
	cache         *feedcache.Cache
	defaultLimit  int
	maxLimit      int
	staticBaseURL string
}

func NewService(repo *Repository, cache *feedcache.Cache, defaultLimit, maxLimit int, staticBaseURL string) *Service {
	return &Service{
		repo:          repo,
		cache:         cache,
		defaultLimit:  defaultLimit,
		maxLimit:      maxLimit,
		staticBaseURL: staticBaseURL,
	}
}

func (s *Service) GetHome(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*HomeFeedResponse, error) {
	cursor, err := decodeHotCursor(rawCursor)
	if err != nil {
		return nil, appErrors.ErrInvalidParams
	}
	s.markUserActiveBestEffort(ctx, viewerUserID)

	limit := s.normalizeLimit(requestedLimit)
	needCount := limit + 1
	candidateFetchCount := maxInt(needCount*3, 30)

	refs, usedCache, err := s.loadCandidateRefs(ctx, cursor, needCount, candidateFetchCount)
	if err != nil {
		return nil, err
	}

	items, orderedRefs, err := s.buildHomeVideoCards(ctx, refs, viewerUserID, needCount)
	if err != nil {
		return nil, err
	}

	if usedCache && len(orderedRefs) < needCount && len(refs) == candidateFetchCount {
		mysqlRefs, loadErr := s.repo.LoadHomeHotRefsFromMySQL(ctx, cursor, needCount)
		if loadErr != nil {
			return nil, loadErr
		}

		items, orderedRefs, err = s.buildHomeVideoCards(ctx, mysqlRefs, viewerUserID, needCount)
		if err != nil {
			return nil, err
		}
	}

	hasMore := len(orderedRefs) > limit
	if hasMore {
		items = items[:limit]
		orderedRefs = orderedRefs[:limit]
	}

	nextCursor := ""
	if hasMore {
		nextCursor, err = encodeHotCursor(hotCursor{
			Score: orderedRefs[len(orderedRefs)-1].HotScore,
			ID:    orderedRefs[len(orderedRefs)-1].VideoID,
		})
		if err != nil {
			return nil, err
		}
	}

	return &HomeFeedResponse{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) GetFollowing(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*FollowingFeedResponse, error) {
	cursor, err := decodeTimeCursor(rawCursor)
	if err != nil {
		return nil, appErrors.ErrInvalidParams
	}
	s.markUserActiveBestEffort(ctx, viewerUserID)

	limit := s.normalizeLimit(requestedLimit)
	needCount := limit + 1
	candidateFetchCount := maxInt(needCount*3, 30)

	refs, usedCache, err := s.loadFollowingCandidateRefs(ctx, viewerUserID, cursor, needCount, candidateFetchCount)
	if err != nil {
		return nil, err
	}

	items, orderedRefs, err := s.buildFollowingVideoCards(ctx, refs, viewerUserID)
	if err != nil {
		return nil, err
	}

	if usedCache && len(orderedRefs) < needCount {
		mysqlRefs, loadErr := s.repo.LoadFollowingRefsFromMySQL(ctx, viewerUserID, cursor, needCount)
		if loadErr != nil {
			return nil, loadErr
		}
		s.backfillFollowingInboxBestEffort(ctx, viewerUserID, mysqlRefs)

		items, orderedRefs, err = s.buildFollowingVideoCards(ctx, mysqlRefs, viewerUserID)
		if err != nil {
			return nil, err
		}
	}

	hasMore := len(orderedRefs) > limit
	if hasMore {
		items = items[:limit]
		orderedRefs = orderedRefs[:limit]
	}

	nextCursor := ""
	if hasMore {
		nextCursor, err = encodeTimeCursor(timeCursor{
			Time: orderedRefs[len(orderedRefs)-1].PublishedAt,
			ID:   orderedRefs[len(orderedRefs)-1].VideoID,
		})
		if err != nil {
			return nil, err
		}
	}

	return &FollowingFeedResponse{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) loadFollowingCandidateRefs(ctx context.Context, viewerUserID uint64, cursor *timeCursor, needCount, candidateFetchCount int) ([]FollowingFeedRef, bool, error) {
	if s.cache != nil {
		var maxPublishedAt *time.Time
		if cursor != nil {
			cursorTime := cursor.Time.UTC()
			maxPublishedAt = &cursorTime
		}

		cacheReady := true
		inboxRefs, inboxErr := s.cache.LoadFollowingInboxRefs(ctx, viewerUserID, maxPublishedAt, candidateFetchCount)
		if inboxErr != nil {
			log.Printf("load following refs from redis inbox failed, fallback to mysql: %v", inboxErr)
			cacheReady = false
		}

		var outboxRefs []feedcache.FollowingInboxRef
		pullModeAuthorIDs, pullAuthorReady, authorErr := s.cache.LoadFollowedPullAuthorIDs(ctx, viewerUserID)
		if authorErr != nil {
			log.Printf("load followed pull author ids failed, fallback to mysql: %v", authorErr)
			cacheReady = false
		} else if !pullAuthorReady {
			s.cache.WarmViewerRelationsAsync(viewerUserID)
			cacheReady = false
		} else if len(pullModeAuthorIDs) > 0 {
			var modeErr error
			outboxRefs, modeErr = s.cache.LoadMergedAuthorOutboxRefs(ctx, pullModeAuthorIDs, maxPublishedAt, candidateFetchCount)
			if modeErr != nil {
				log.Printf("load author outbox refs failed, fallback to mysql: %v", modeErr)
				outboxRefs = nil
				cacheReady = false
			}
		}

		if cacheReady {
			refs := mergeFollowingCandidateRefs(inboxRefs, outboxRefs, cursor, candidateFetchCount)
			if len(refs) > 0 {
				return refs, true, nil
			}
		}
	}

	mysqlRefs, err := s.repo.LoadFollowingRefsFromMySQL(ctx, viewerUserID, cursor, needCount)
	if err != nil {
		return nil, false, err
	}
	s.backfillFollowingInboxBestEffort(ctx, viewerUserID, mysqlRefs)
	return mysqlRefs, false, nil
}

func mergeFollowingCandidateRefs(inboxRefs, outboxRefs []feedcache.FollowingInboxRef, cursor *timeCursor, count int) []FollowingFeedRef {
	merged := make([]FollowingFeedRef, 0, len(inboxRefs)+len(outboxRefs))
	seen := make(map[uint64]struct{}, len(inboxRefs)+len(outboxRefs))
	appendRefs := func(refs []feedcache.FollowingInboxRef) {
		for _, ref := range refs {
			if ref.VideoID == 0 || ref.PublishedAt.IsZero() {
				continue
			}
			if _, exists := seen[ref.VideoID]; exists {
				continue
			}
			current := FollowingFeedRef{
				VideoID:     ref.VideoID,
				PublishedAt: ref.PublishedAt.UTC(),
			}
			if !isAfterTimeCursor(current, cursor) {
				continue
			}
			seen[ref.VideoID] = struct{}{}
			merged = append(merged, current)
		}
	}

	appendRefs(inboxRefs)
	appendRefs(outboxRefs)

	sort.Slice(merged, func(i, j int) bool {
		left := merged[i].PublishedAt.UTC()
		right := merged[j].PublishedAt.UTC()
		if left.Equal(right) {
			return merged[i].VideoID > merged[j].VideoID
		}
		return left.After(right)
	})
	if len(merged) > count {
		merged = merged[:count]
	}
	return merged
}

func (s *Service) backfillFollowingInboxBestEffort(ctx context.Context, viewerUserID uint64, refs []FollowingFeedRef) {
	if s.cache == nil || viewerUserID == 0 || len(refs) == 0 {
		return
	}

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
	if len(inboxRefs) == 0 {
		return
	}

	if err := s.cache.AddFollowingInboxRefs(ctx, viewerUserID, inboxRefs); err != nil {
		log.Printf("backfill following inbox failed: viewer_user_id=%d err=%v", viewerUserID, err)
	}
}

func (s *Service) loadCandidateRefs(ctx context.Context, cursor *hotCursor, needCount, candidateFetchCount int) ([]HotFeedRef, bool, error) {
	cacheRefs, err := s.repo.LoadHomeHotRefsFromRedis(ctx, cursor, candidateFetchCount)
	if err != nil {
		log.Printf("load home hot refs from redis failed, fallback to mysql: %v", err)
		cacheRefs = nil
	}

	if len(cacheRefs) > 0 {
		return cacheRefs, true, nil
	}

	mysqlRefs, err := s.repo.LoadHomeHotRefsFromMySQL(ctx, cursor, needCount)
	if err != nil {
		return nil, false, err
	}

	return mysqlRefs, false, nil
}

func (s *Service) buildHomeVideoCards(ctx context.Context, refs []HotFeedRef, viewerUserID uint64, needCount int) ([]*videoDomain.VideoCard, []HotFeedRef, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}

	videoIDs := make([]uint64, 0, len(refs))
	for _, ref := range refs {
		videoIDs = append(videoIDs, ref.VideoID)
	}

	if s.cache == nil {
		items, keptVideoIDs, err := s.buildVideoCardsByVideoIDs(ctx, videoIDs, viewerUserID)
		if err != nil {
			return nil, nil, err
		}
		return items, hotRefsByKeptVideoIDs(refs, keptVideoIDs), nil
	}

	bases, err := s.loadVideoBases(ctx, videoIDs)
	if err != nil {
		return nil, nil, err
	}

	selectedRefs := selectHomeRefsByAuthorDiversity(refs, bases, needCount)
	selectedVideoIDs := make([]uint64, 0, len(selectedRefs))
	for _, ref := range selectedRefs {
		selectedVideoIDs = append(selectedVideoIDs, ref.VideoID)
	}

	items, keptVideoIDs, err := s.buildVideoCardsByVideoIDsWithBases(ctx, selectedVideoIDs, viewerUserID, bases)
	if err != nil {
		return nil, nil, err
	}

	return items, hotRefsByKeptVideoIDs(selectedRefs, keptVideoIDs), nil
}

func selectHomeRefsByAuthorDiversity(refs []HotFeedRef, bases map[uint64]*feedcache.VideoBase, needCount int) []HotFeedRef {
	if needCount <= 0 || len(refs) == 0 {
		return nil
	}

	selected := make([]HotFeedRef, 0, minInt(needCount, len(refs)))
	selectedVideoIDs := make(map[uint64]struct{}, minInt(needCount, len(refs)))
	seenAuthorIDs := make(map[uint64]struct{}, minInt(needCount, len(refs)))
	orderByVideoID := make(map[uint64]int, len(refs))
	for i, ref := range refs {
		if _, exists := orderByVideoID[ref.VideoID]; !exists {
			orderByVideoID[ref.VideoID] = i
		}
	}

	for _, ref := range refs {
		base := bases[ref.VideoID]
		if base == nil || base.AuthorID == 0 {
			continue
		}
		if _, exists := seenAuthorIDs[base.AuthorID]; exists {
			continue
		}
		selected = append(selected, ref)
		selectedVideoIDs[ref.VideoID] = struct{}{}
		seenAuthorIDs[base.AuthorID] = struct{}{}
		if len(selected) >= needCount {
			return selected
		}
	}

	for _, ref := range refs {
		if _, exists := selectedVideoIDs[ref.VideoID]; exists {
			continue
		}
		if bases[ref.VideoID] == nil {
			continue
		}
		selected = append(selected, ref)
		selectedVideoIDs[ref.VideoID] = struct{}{}
		if len(selected) >= needCount {
			break
		}
	}

	sort.Slice(selected, func(i, j int) bool {
		return orderByVideoID[selected[i].VideoID] < orderByVideoID[selected[j].VideoID]
	})
	return selected
}

func hotRefsByKeptVideoIDs(refs []HotFeedRef, keptVideoIDs []uint64) []HotFeedRef {
	refMap := make(map[uint64]HotFeedRef, len(refs))
	for _, ref := range refs {
		refMap[ref.VideoID] = ref
	}

	orderedRefs := make([]HotFeedRef, 0, len(keptVideoIDs))
	for _, videoID := range keptVideoIDs {
		ref, ok := refMap[videoID]
		if !ok {
			continue
		}
		orderedRefs = append(orderedRefs, ref)
	}
	return orderedRefs
}

func (s *Service) buildFollowingVideoCards(ctx context.Context, refs []FollowingFeedRef, viewerUserID uint64) ([]*videoDomain.VideoCard, []FollowingFeedRef, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}

	videoIDs := make([]uint64, 0, len(refs))
	for _, ref := range refs {
		videoIDs = append(videoIDs, ref.VideoID)
	}

	if s.cache == nil {
		items, keptVideoIDs, err := s.buildVideoCardsByVideoIDs(ctx, videoIDs, viewerUserID)
		if err != nil {
			return nil, nil, err
		}
		return items, followingRefsByKeptVideoIDs(refs, keptVideoIDs), nil
	}

	bases, err := s.loadVideoBases(ctx, videoIDs)
	if err != nil {
		return nil, nil, err
	}

	filteredVideoIDs, err := s.filterFollowingVideoIDsByCurrentRelations(ctx, videoIDs, viewerUserID, bases)
	if err != nil {
		return nil, nil, err
	}
	if len(filteredVideoIDs) == 0 {
		return nil, nil, nil
	}

	items, keptVideoIDs, err := s.buildVideoCardsByVideoIDsWithBases(ctx, filteredVideoIDs, viewerUserID, bases)
	if err != nil {
		return nil, nil, err
	}

	return items, followingRefsByKeptVideoIDs(refs, keptVideoIDs), nil
}

func (s *Service) filterFollowingVideoIDsByCurrentRelations(ctx context.Context, orderedVideoIDs []uint64, viewerUserID uint64, bases map[uint64]*feedcache.VideoBase) ([]uint64, error) {
	if viewerUserID == 0 || len(orderedVideoIDs) == 0 {
		return nil, nil
	}

	authorIDs := make([]uint64, 0, len(orderedVideoIDs))
	seenAuthorIDs := make(map[uint64]struct{}, len(orderedVideoIDs))
	for _, videoID := range orderedVideoIDs {
		base := bases[videoID]
		if base == nil || base.AuthorID == 0 {
			continue
		}
		if _, exists := seenAuthorIDs[base.AuthorID]; exists {
			continue
		}
		seenAuthorIDs[base.AuthorID] = struct{}{}
		authorIDs = append(authorIDs, base.AuthorID)
	}
	if len(authorIDs) == 0 {
		return nil, nil
	}

	_, _, followedAuthorIDs, err := s.loadViewerState(ctx, viewerUserID, nil, authorIDs)
	if err != nil {
		return nil, err
	}

	filteredVideoIDs := make([]uint64, 0, len(orderedVideoIDs))
	for _, videoID := range orderedVideoIDs {
		base := bases[videoID]
		if base == nil {
			continue
		}
		if _, followed := followedAuthorIDs[base.AuthorID]; !followed {
			continue
		}
		filteredVideoIDs = append(filteredVideoIDs, videoID)
	}
	return filteredVideoIDs, nil
}

func followingRefsByKeptVideoIDs(refs []FollowingFeedRef, keptVideoIDs []uint64) []FollowingFeedRef {
	refMap := make(map[uint64]FollowingFeedRef, len(refs))
	for _, ref := range refs {
		refMap[ref.VideoID] = ref
	}

	orderedRefs := make([]FollowingFeedRef, 0, len(keptVideoIDs))
	for _, videoID := range keptVideoIDs {
		ref, ok := refMap[videoID]
		if !ok {
			continue
		}
		orderedRefs = append(orderedRefs, ref)
	}
	return orderedRefs
}

func (s *Service) buildVideoCardsByVideoIDs(ctx context.Context, orderedVideoIDs []uint64, viewerUserID uint64) ([]*videoDomain.VideoCard, []uint64, error) {
	if len(orderedVideoIDs) == 0 {
		return nil, nil, nil
	}

	if s.cache == nil {
		return s.buildOrderedVideoCardsFallback(ctx, orderedVideoIDs, viewerUserID)
	}

	bases, err := s.loadVideoBases(ctx, orderedVideoIDs)
	if err != nil {
		return nil, nil, err
	}

	return s.buildVideoCardsByVideoIDsWithBases(ctx, orderedVideoIDs, viewerUserID, bases)
}

func (s *Service) buildVideoCardsByVideoIDsWithBases(ctx context.Context, orderedVideoIDs []uint64, viewerUserID uint64, bases map[uint64]*feedcache.VideoBase) ([]*videoDomain.VideoCard, []uint64, error) {
	if len(orderedVideoIDs) == 0 {
		return nil, nil, nil
	}

	keptVideoIDs := make([]uint64, 0, len(orderedVideoIDs))
	authorIDs := make([]uint64, 0, len(orderedVideoIDs))
	seenAuthorIDs := make(map[uint64]struct{}, len(orderedVideoIDs))
	for _, videoID := range orderedVideoIDs {
		base := bases[videoID]
		if base == nil {
			continue
		}

		keptVideoIDs = append(keptVideoIDs, videoID)
		if _, ok := seenAuthorIDs[base.AuthorID]; ok {
			continue
		}
		seenAuthorIDs[base.AuthorID] = struct{}{}
		authorIDs = append(authorIDs, base.AuthorID)
	}

	if len(keptVideoIDs) == 0 {
		return nil, nil, nil
	}

	briefs, err := s.loadUserBriefs(ctx, authorIDs)
	if err != nil {
		return nil, nil, err
	}

	statsMap, err := s.loadVideoStats(ctx, keptVideoIDs)
	if err != nil {
		return nil, nil, err
	}

	viewerVideos := make([]feedcache.ViewerRelationVideo, 0, len(keptVideoIDs))
	for _, videoID := range keptVideoIDs {
		base := bases[videoID]
		if base == nil {
			continue
		}
		viewerVideos = append(viewerVideos, feedcache.ViewerRelationVideo{
			VideoID:     videoID,
			PublishedAt: base.PublishedAt,
		})
	}

	likedVideoIDs, favoritedVideoIDs, followedAuthorIDs, err := s.loadViewerState(ctx, viewerUserID, viewerVideos, authorIDs)
	if err != nil {
		return nil, nil, err
	}

	items := make([]*videoDomain.VideoCard, 0, len(keptVideoIDs))
	finalVideoIDs := make([]uint64, 0, len(keptVideoIDs))
	for _, videoID := range orderedVideoIDs {
		base := bases[videoID]
		if base == nil {
			continue
		}

		brief := briefs[base.AuthorID]
		if brief == nil {
			continue
		}

		stats := statsMap[videoID]
		if stats == nil {
			stats = &feedcache.VideoStats{VideoID: videoID}
		}

		_, liked := likedVideoIDs[videoID]
		_, favorited := favoritedVideoIDs[videoID]
		_, followingAuthor := followedAuthorIDs[base.AuthorID]
		if viewerUserID == 0 || viewerUserID == base.AuthorID {
			followingAuthor = false
		}

		items = append(items, &videoDomain.VideoCard{
			ID:          base.VideoID,
			Title:       base.Title,
			VideoURL:    filestorage.BuildStaticURL(s.staticBaseURL, base.VideoPath),
			CoverURL:    filestorage.BuildStaticURL(s.staticBaseURL, base.CoverPath),
			PublishedAt: base.PublishedAt,
			Author: videoDomain.UserSummary{
				ID:        brief.UserID,
				Nickname:  brief.Nickname,
				AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, brief.AvatarPath),
			},
			Stats: videoDomain.VideoStatsObject{
				LikeCount:     stats.LikeCount,
				CommentCount:  stats.CommentCount,
				FavoriteCount: stats.FavoriteCount,
			},
			ViewerState: videoDomain.ViewerStateObject{
				Liked:           liked,
				Favorited:       favorited,
				FollowingAuthor: followingAuthor,
			},
		})
		finalVideoIDs = append(finalVideoIDs, videoID)
	}

	return items, finalVideoIDs, nil
}

func (s *Service) loadVideoBases(ctx context.Context, videoIDs []uint64) (map[uint64]*feedcache.VideoBase, error) {
	result := make(map[uint64]*feedcache.VideoBase, len(videoIDs))
	loaded, missing, err := s.cache.LoadVideoBasesByVideoIDs(ctx, videoIDs)
	if err != nil {
		log.Printf("load video bases from redis failed, fallback to mysql build: %v", err)
		loaded = make(map[uint64]*feedcache.VideoBase, len(videoIDs))
		missing = append([]uint64(nil), videoIDs...)
	}

	for videoID, base := range loaded {
		result[videoID] = base
	}

	if len(missing) == 0 {
		return result, nil
	}

	built, err := s.cache.BuildVideoBasesByVideoIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	for videoID, base := range built {
		result[videoID] = base
	}

	if err := s.cache.StoreVideoBases(ctx, built); err != nil {
		log.Printf("store rebuilt video bases failed: %v", err)
	}

	stale := idsMissingFromVideoBaseMap(missing, built)
	if len(stale) > 0 {
		if err := s.cache.DeleteVideoBasesByVideoIDs(ctx, stale); err != nil {
			log.Printf("delete stale video bases failed: %v", err)
		}
	}

	return result, nil
}

func (s *Service) loadUserBriefs(ctx context.Context, userIDs []uint64) (map[uint64]*feedcache.UserBrief, error) {
	result := make(map[uint64]*feedcache.UserBrief, len(userIDs))
	loaded, missing, err := s.cache.LoadUserBriefsByUserIDs(ctx, userIDs)
	if err != nil {
		log.Printf("load user briefs from redis failed, fallback to mysql build: %v", err)
		loaded = make(map[uint64]*feedcache.UserBrief, len(userIDs))
		missing = append([]uint64(nil), userIDs...)
	}

	for userID, brief := range loaded {
		result[userID] = brief
	}

	if len(missing) == 0 {
		return result, nil
	}

	built, err := s.cache.BuildUserBriefsByUserIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	for userID, brief := range built {
		result[userID] = brief
	}

	if err := s.cache.StoreUserBriefs(ctx, built); err != nil {
		log.Printf("store rebuilt user briefs failed: %v", err)
	}

	return result, nil
}

func (s *Service) loadVideoStats(ctx context.Context, videoIDs []uint64) (map[uint64]*feedcache.VideoStats, error) {
	result := make(map[uint64]*feedcache.VideoStats, len(videoIDs))
	loaded, missing, err := s.cache.LoadVideoStatsByVideoIDs(ctx, videoIDs)
	if err != nil {
		log.Printf("load video stats cache from redis failed, fallback to mysql build: %v", err)
		loaded = make(map[uint64]*feedcache.VideoStats, len(videoIDs))
		missing = append([]uint64(nil), videoIDs...)
	}

	for videoID, stats := range loaded {
		result[videoID] = stats
	}

	if len(missing) == 0 {
		return result, nil
	}

	built, err := s.cache.BuildVideoStatsByVideoIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	for videoID, stats := range built {
		result[videoID] = stats
	}

	if err := s.cache.StoreVideoStatsBatch(ctx, built); err != nil {
		log.Printf("store rebuilt video stats cache failed: %v", err)
	}

	return result, nil
}

func (s *Service) loadViewerState(ctx context.Context, viewerUserID uint64, videos []feedcache.ViewerRelationVideo, authorIDs []uint64) (map[uint64]struct{}, map[uint64]struct{}, map[uint64]struct{}, error) {
	likedVideoIDs := make(map[uint64]struct{})
	favoritedVideoIDs := make(map[uint64]struct{})
	followedAuthorIDs := make(map[uint64]struct{})
	if viewerUserID == 0 {
		return likedVideoIDs, favoritedVideoIDs, followedAuthorIDs, nil
	}

	if s.cache != nil {
		relations, err := s.cache.LoadViewerRelations(ctx, viewerUserID, videos, authorIDs)
		if err == nil {
			return relations.LikedVideoIDs, relations.FavoritedVideoIDs, relations.FollowedAuthorIDs, nil
		}
		log.Printf("load viewer relations from cache failed, fallback to mysql: %v", err)
	}

	videoIDs := make([]uint64, 0, len(videos))
	for _, video := range videos {
		if video.VideoID == 0 {
			continue
		}
		videoIDs = append(videoIDs, video.VideoID)
	}

	var err error
	likedVideoIDs, err = s.repo.GetLikedVideoIDs(ctx, viewerUserID, videoIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	favoritedVideoIDs, err = s.repo.GetFavoritedVideoIDs(ctx, viewerUserID, videoIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	followedAuthorIDs, err = s.repo.GetFollowedAuthorIDs(ctx, viewerUserID, authorIDs)
	if err != nil {
		return nil, nil, nil, err
	}

	return likedVideoIDs, favoritedVideoIDs, followedAuthorIDs, nil
}

func (s *Service) markUserActiveBestEffort(ctx context.Context, userID uint64) {
	if s.cache == nil || userID == 0 {
		return
	}
	if err := s.cache.MarkUserActive(ctx, userID); err != nil {
		log.Printf("mark user active failed: user_id=%d err=%v", userID, err)
	}
}

func (s *Service) buildOrderedVideoCardsFallback(ctx context.Context, orderedVideoIDs []uint64, viewerUserID uint64) ([]*videoDomain.VideoCard, []uint64, error) {
	if len(orderedVideoIDs) == 0 {
		return nil, nil, nil
	}

	videos, err := s.repo.GetVisibleVideosByIDs(ctx, orderedVideoIDs)
	if err != nil {
		return nil, nil, err
	}

	videoMap := make(map[uint64]*model.Video, len(videos))
	authorIDs := make([]uint64, 0, len(videos))
	seenAuthorIDs := make(map[uint64]struct{}, len(videos))
	for i := range videos {
		video := videos[i]
		videoMap[video.ID] = &video
		if _, exists := seenAuthorIDs[video.AuthorID]; !exists {
			seenAuthorIDs[video.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, video.AuthorID)
		}
	}

	users, err := s.repo.GetUsersByIDs(ctx, authorIDs)
	if err != nil {
		return nil, nil, err
	}
	userMap := make(map[uint64]*model.User, len(users))
	for i := range users {
		user := users[i]
		userMap[user.ID] = &user
	}

	statsRows, err := s.repo.GetVideoStatsByIDs(ctx, orderedVideoIDs)
	if err != nil {
		return nil, nil, err
	}
	statsMap := make(map[uint64]*model.VideoStats, len(statsRows))
	for i := range statsRows {
		stats := statsRows[i]
		statsMap[stats.VideoID] = &stats
	}

	viewerVideos := make([]feedcache.ViewerRelationVideo, 0, len(videos))
	for i := range videos {
		video := videos[i]
		viewerVideos = append(viewerVideos, feedcache.ViewerRelationVideo{
			VideoID:     video.ID,
			PublishedAt: video.PublishedAt,
		})
	}

	likedVideoIDs, favoritedVideoIDs, followedAuthorIDs, err := s.loadViewerState(ctx, viewerUserID, viewerVideos, authorIDs)
	if err != nil {
		return nil, nil, err
	}

	items := make([]*videoDomain.VideoCard, 0, len(orderedVideoIDs))
	keptVideoIDs := make([]uint64, 0, len(orderedVideoIDs))
	for _, videoID := range orderedVideoIDs {
		video := videoMap[videoID]
		if video == nil {
			continue
		}

		author := userMap[video.AuthorID]
		if author == nil {
			continue
		}

		stats := statsMap[video.ID]
		if stats == nil {
			stats = &model.VideoStats{VideoID: video.ID}
		}

		_, liked := likedVideoIDs[video.ID]
		_, favorited := favoritedVideoIDs[video.ID]
		_, followingAuthor := followedAuthorIDs[video.AuthorID]
		if viewerUserID == 0 || viewerUserID == video.AuthorID {
			followingAuthor = false
		}

		items = append(items, &videoDomain.VideoCard{
			ID:          video.ID,
			Title:       video.Title,
			VideoURL:    filestorage.BuildStaticURL(s.staticBaseURL, video.VideoPath),
			CoverURL:    filestorage.BuildStaticURL(s.staticBaseURL, video.CoverPath),
			PublishedAt: video.PublishedAt,
			Author: videoDomain.UserSummary{
				ID:        author.ID,
				Nickname:  author.Nickname,
				AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, author.AvatarPath),
			},
			Stats: videoDomain.VideoStatsObject{
				LikeCount:     stats.LikeCount,
				CommentCount:  stats.CommentCount,
				FavoriteCount: stats.FavoriteCount,
			},
			ViewerState: videoDomain.ViewerStateObject{
				Liked:           liked,
				Favorited:       favorited,
				FollowingAuthor: followingAuthor,
			},
		})
		keptVideoIDs = append(keptVideoIDs, video.ID)
	}

	return items, keptVideoIDs, nil
}

func (s *Service) normalizeLimit(requestedLimit int) int {
	if requestedLimit <= 0 {
		return s.defaultLimit
	}
	if requestedLimit > s.maxLimit {
		return s.maxLimit
	}
	return requestedLimit
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func idsMissingFromVideoBaseMap(videoIDs []uint64, loaded map[uint64]*feedcache.VideoBase) []uint64 {
	missing := make([]uint64, 0)
	for _, videoID := range videoIDs {
		if _, ok := loaded[videoID]; ok {
			continue
		}
		missing = append(missing, videoID)
	}
	return missing
}
