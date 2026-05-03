package user

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"strings"

	appErrors "feed-backend/internal/common/errors"
	authDomain "feed-backend/internal/domain/auth"
	videoDomain "feed-backend/internal/domain/video"
	"feed-backend/internal/infra/feedcache"
	filestorage "feed-backend/internal/infra/storage"
	"feed-backend/internal/model"

	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	repo          *Repository
	storage       *filestorage.LocalStorage
	cache         *feedcache.Cache
	staticBaseURL string
	defaultLimit  int
	maxLimit      int
}

func NewService(repo *Repository, storage *filestorage.LocalStorage, cache *feedcache.Cache, staticBaseURL string, defaultLimit, maxLimit int) *Service {
	return &Service{
		repo:          repo,
		storage:       storage,
		cache:         cache,
		staticBaseURL: staticBaseURL,
		defaultLimit:  defaultLimit,
		maxLimit:      maxLimit,
	}
}

func (s *Service) GetMe(ctx context.Context, userID uint64) (*MeResponse, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.buildMeResponse(user), nil
}

func (s *Service) UpdateProfile(ctx context.Context, userID uint64, req UpdateProfileRequest) (*MeResponse, error) {
	nickname := strings.TrimSpace(req.Nickname)
	bio := strings.TrimSpace(req.Bio)
	if nickname == "" || len(nickname) > 32 || len(bio) > 200 {
		return nil, appErrors.ErrInvalidParams
	}

	if err := s.repo.UpdateProfile(ctx, userID, nickname, bio); err != nil {
		return nil, err
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.storeUserBriefBestEffort(ctx, user)
	return s.buildMeResponse(user), nil
}

func (s *Service) UpdateAvatar(ctx context.Context, userID uint64, header *multipart.FileHeader) (*MeResponse, error) {
	if header == nil {
		return nil, appErrors.ErrInvalidParams
	}

	result, err := s.saveAvatar(header)
	if err != nil {
		return nil, err
	}

	if err := s.repo.UpdateAvatar(ctx, userID, result.RelativePath); err != nil {
		_ = s.storage.Delete(result.RelativePath)
		return nil, err
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.storeUserBriefBestEffort(ctx, user)
	return s.buildMeResponse(user), nil
}

func (s *Service) UpdatePassword(ctx context.Context, userID uint64, req UpdatePasswordRequest) error {
	oldPassword := req.OldPassword
	newPassword := req.NewPassword
	if strings.TrimSpace(oldPassword) == "" || len(oldPassword) > 32 || len(oldPassword) < 6 ||
		strings.TrimSpace(newPassword) == "" || len(newPassword) > 32 || len(newPassword) < 6 {
		return appErrors.ErrInvalidParams
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	if err := authDomain.ComparePassword(user.PasswordHash, oldPassword); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return appErrors.ErrOldPasswordWrong
		}
		return fmt.Errorf("compare old password: %w", err)
	}

	passwordHash, err := authDomain.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	return s.repo.UpdatePasswordHash(ctx, userID, passwordHash)
}

func (s *Service) GetAuthorProfile(ctx context.Context, viewerUserID, targetUserID uint64) (*UserProfileResponse, error) {
	user, err := s.repo.GetUserByID(ctx, targetUserID)
	if err != nil {
		return nil, err
	}

	relationStatus, err := s.relationStatus(ctx, viewerUserID, targetUserID)
	if err != nil {
		return nil, err
	}

	return &UserProfileResponse{
		ID:             user.ID,
		Nickname:       user.Nickname,
		AvatarURL:      filestorage.BuildStaticURL(s.staticBaseURL, user.AvatarPath),
		Bio:            user.Bio,
		RelationStatus: relationStatus,
	}, nil
}

func (s *Service) ListMyVideos(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*VideoPageResponse, error) {
	return s.listAuthorVideos(ctx, viewerUserID, viewerUserID, rawCursor, requestedLimit)
}

func (s *Service) ListAuthorVideos(ctx context.Context, viewerUserID, authorID uint64, rawCursor string, requestedLimit int) (*VideoPageResponse, error) {
	if _, err := s.repo.GetUserByID(ctx, authorID); err != nil {
		return nil, err
	}
	return s.listAuthorVideos(ctx, viewerUserID, authorID, rawCursor, requestedLimit)
}

func (s *Service) ListLikedVideos(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*VideoPageResponse, error) {
	cursor, limit, err := s.parsePage(rawCursor, requestedLimit)
	if err != nil {
		return nil, err
	}

	refs, err := s.repo.ListLikedVideoRefs(ctx, viewerUserID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return s.buildVideoPage(ctx, refs, viewerUserID, limit)
}

func (s *Service) ListFavoritedVideos(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*VideoPageResponse, error) {
	cursor, limit, err := s.parsePage(rawCursor, requestedLimit)
	if err != nil {
		return nil, err
	}

	refs, err := s.repo.ListFavoritedVideoRefs(ctx, viewerUserID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return s.buildVideoPage(ctx, refs, viewerUserID, limit)
}

func (s *Service) ListFollowings(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*UserPageResponse, error) {
	cursor, limit, err := s.parsePage(rawCursor, requestedLimit)
	if err != nil {
		return nil, err
	}

	refs, err := s.repo.ListFollowingRefs(ctx, viewerUserID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return s.buildUserPage(ctx, refs, viewerUserID, limit)
}

func (s *Service) ListFollowers(ctx context.Context, viewerUserID uint64, rawCursor string, requestedLimit int) (*UserPageResponse, error) {
	cursor, limit, err := s.parsePage(rawCursor, requestedLimit)
	if err != nil {
		return nil, err
	}

	refs, err := s.repo.ListFollowerRefs(ctx, viewerUserID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return s.buildUserPage(ctx, refs, viewerUserID, limit)
}

func (s *Service) listAuthorVideos(ctx context.Context, viewerUserID, authorID uint64, rawCursor string, requestedLimit int) (*VideoPageResponse, error) {
	cursor, limit, err := s.parsePage(rawCursor, requestedLimit)
	if err != nil {
		return nil, err
	}

	refs, err := s.repo.ListAuthorVideoRefs(ctx, authorID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return s.buildVideoPage(ctx, refs, viewerUserID, limit)
}

func (s *Service) parsePage(rawCursor string, requestedLimit int) (*timeCursor, int, error) {
	cursor, err := decodeTimeCursor(rawCursor)
	if err != nil {
		return nil, 0, appErrors.ErrInvalidParams
	}
	return cursor, s.normalizeLimit(requestedLimit), nil
}

func (s *Service) buildVideoPage(ctx context.Context, refs []videoRef, viewerUserID uint64, limit int) (*VideoPageResponse, error) {
	items, keptRefs, err := s.buildVideoCards(ctx, refs, viewerUserID)
	if err != nil {
		return nil, err
	}

	hasMore := len(keptRefs) > limit
	if hasMore {
		items = items[:limit]
		keptRefs = keptRefs[:limit]
	}

	nextCursor := ""
	if hasMore {
		last := keptRefs[len(keptRefs)-1]
		nextCursor, err = encodeTimeCursor(timeCursor{Time: last.CursorTime, ID: last.VideoID})
		if err != nil {
			return nil, err
		}
	}

	return &VideoPageResponse{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) buildVideoCards(ctx context.Context, refs []videoRef, viewerUserID uint64) ([]*videoDomain.VideoCard, []videoRef, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}

	orderedVideoIDs := make([]uint64, 0, len(refs))
	for _, ref := range refs {
		orderedVideoIDs = append(orderedVideoIDs, ref.VideoID)
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

	likedVideoIDs := make(map[uint64]struct{})
	favoritedVideoIDs := make(map[uint64]struct{})
	followedAuthorIDs := make(map[uint64]struct{})
	if viewerUserID > 0 {
		loadedFromCache := false
		if s.cache != nil {
			viewerVideos := make([]feedcache.ViewerRelationVideo, 0, len(videos))
			for i := range videos {
				video := videos[i]
				viewerVideos = append(viewerVideos, feedcache.ViewerRelationVideo{
					VideoID:     video.ID,
					PublishedAt: video.PublishedAt,
				})
			}

			relations, cacheErr := s.cache.LoadViewerRelations(ctx, viewerUserID, viewerVideos, authorIDs)
			if cacheErr == nil {
				loadedFromCache = true
				likedVideoIDs = relations.LikedVideoIDs
				favoritedVideoIDs = relations.FavoritedVideoIDs
				followedAuthorIDs = relations.FollowedAuthorIDs
			} else {
				log.Printf("load user page viewer relations from cache failed, fallback to mysql: viewer_user_id=%d err=%v", viewerUserID, cacheErr)
			}
		}

		if !loadedFromCache {
			likedVideoIDs, err = s.repo.GetLikedVideoIDs(ctx, viewerUserID, orderedVideoIDs)
			if err != nil {
				return nil, nil, err
			}
			favoritedVideoIDs, err = s.repo.GetFavoritedVideoIDs(ctx, viewerUserID, orderedVideoIDs)
			if err != nil {
				return nil, nil, err
			}
			followedAuthorIDs, err = s.repo.GetFollowedAuthorIDs(ctx, viewerUserID, authorIDs)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	refMap := make(map[uint64]videoRef, len(refs))
	for _, ref := range refs {
		refMap[ref.VideoID] = ref
	}

	items := make([]*videoDomain.VideoCard, 0, len(refs))
	keptRefs := make([]videoRef, 0, len(refs))
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
		keptRefs = append(keptRefs, refMap[video.ID])
	}

	return items, keptRefs, nil
}

func (s *Service) buildUserPage(ctx context.Context, refs []userRef, viewerUserID uint64, limit int) (*UserPageResponse, error) {
	items, keptRefs, err := s.buildUserCards(ctx, refs, viewerUserID)
	if err != nil {
		return nil, err
	}

	hasMore := len(keptRefs) > limit
	if hasMore {
		items = items[:limit]
		keptRefs = keptRefs[:limit]
	}

	nextCursor := ""
	if hasMore {
		last := keptRefs[len(keptRefs)-1]
		nextCursor, err = encodeTimeCursor(timeCursor{Time: last.CursorTime, ID: last.UserID})
		if err != nil {
			return nil, err
		}
	}

	return &UserPageResponse{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) buildUserCards(ctx context.Context, refs []userRef, viewerUserID uint64) ([]*UserCard, []userRef, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}

	userIDs := make([]uint64, 0, len(refs))
	for _, ref := range refs {
		userIDs = append(userIDs, ref.UserID)
	}

	users, err := s.repo.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		return nil, nil, err
	}

	userMap := make(map[uint64]*model.User, len(users))
	for i := range users {
		user := users[i]
		userMap[user.ID] = &user
	}

	followingSet := make(map[uint64]struct{})
	followedBySet := make(map[uint64]struct{})
	if viewerUserID > 0 {
		followingSet, err = s.repo.GetUsersFollowedByViewer(ctx, viewerUserID, userIDs)
		if err != nil {
			return nil, nil, err
		}
		followedBySet, err = s.repo.GetUsersFollowingViewer(ctx, viewerUserID, userIDs)
		if err != nil {
			return nil, nil, err
		}
	}

	items := make([]*UserCard, 0, len(refs))
	keptRefs := make([]userRef, 0, len(refs))
	for _, ref := range refs {
		target := userMap[ref.UserID]
		if target == nil {
			continue
		}

		_, following := followingSet[target.ID]
		_, followedBy := followedBySet[target.ID]
		relation := makeRelationStatus(viewerUserID, target.ID, following, followedBy)

		items = append(items, &UserCard{
			ID:             target.ID,
			Nickname:       target.Nickname,
			AvatarURL:      filestorage.BuildStaticURL(s.staticBaseURL, target.AvatarPath),
			Bio:            target.Bio,
			RelationStatus: relation,
		})
		keptRefs = append(keptRefs, ref)
	}

	return items, keptRefs, nil
}

func (s *Service) relationStatus(ctx context.Context, viewerUserID, targetUserID uint64) (*string, error) {
	if viewerUserID == 0 {
		return nil, nil
	}

	followingSet, err := s.repo.GetUsersFollowedByViewer(ctx, viewerUserID, []uint64{targetUserID})
	if err != nil {
		return nil, err
	}
	followedBySet, err := s.repo.GetUsersFollowingViewer(ctx, viewerUserID, []uint64{targetUserID})
	if err != nil {
		return nil, err
	}

	_, following := followingSet[targetUserID]
	_, followedBy := followedBySet[targetUserID]
	return makeRelationStatus(viewerUserID, targetUserID, following, followedBy), nil
}

func makeRelationStatus(viewerUserID, targetUserID uint64, following, followedBy bool) *string {
	if viewerUserID == 0 {
		return nil
	}
	status := RelationNone
	if viewerUserID != targetUserID {
		switch {
		case following && followedBy:
			status = RelationMutual
		case following:
			status = RelationFollowingAuthor
		case followedBy:
			status = RelationFollowedByAuthor
		}
	}
	return &status
}

func (s *Service) saveAvatar(header *multipart.FileHeader) (*filestorage.SaveResult, error) {
	file, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("open avatar file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	result, err := s.storage.SaveAvatar(file, header)
	if err != nil {
		return nil, mapStorageError(err)
	}
	return result, nil
}

func (s *Service) storeUserBriefBestEffort(ctx context.Context, user *model.User) {
	if s.cache == nil || user == nil {
		return
	}

	brief := feedcache.NewUserBrief(user)
	if brief == nil {
		return
	}

	if err := s.cache.StoreUserBrief(ctx, brief); err != nil {
		log.Printf("store user brief cache failed: user_id=%d err=%v", user.ID, err)
	}
}

func (s *Service) buildMeResponse(user *model.User) *MeResponse {
	return &MeResponse{
		ID:        user.ID,
		Username:  user.Username,
		Nickname:  user.Nickname,
		AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, user.AvatarPath),
		Bio:       user.Bio,
	}
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

func mapStorageError(err error) error {
	switch {
	case errors.Is(err, filestorage.ErrEmptyFile),
		errors.Is(err, filestorage.ErrInvalidUpload),
		errors.Is(err, filestorage.ErrFileTooLarge),
		errors.Is(err, filestorage.ErrUnsupportedFileExtension),
		errors.Is(err, filestorage.ErrUnsupportedMIMEType):
		return appErrors.Wrap(err, appErrors.ErrInvalidParams.HTTPStatus, appErrors.ErrInvalidParams.Code, appErrors.ErrInvalidParams.Message)
	default:
		return err
	}
}
