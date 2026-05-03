package video

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"strings"
	"time"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/infra/feedcache"
	filestorage "feed-backend/internal/infra/storage"
	"feed-backend/internal/model"
)

type Service struct {
	repo          *Repository
	storage       *filestorage.LocalStorage
	publisher     *Publisher
	cache         *feedcache.Cache
	staticBaseURL string
}

func NewService(
	repo *Repository,
	storage *filestorage.LocalStorage,
	publisher *Publisher,
	cache *feedcache.Cache,
	staticBaseURL string,
) *Service {
	return &Service{
		repo:          repo,
		storage:       storage,
		publisher:     publisher,
		cache:         cache,
		staticBaseURL: staticBaseURL,
	}
}

func (s *Service) Publish(ctx context.Context, operatorUserID uint64, req PublishRequest) (*VideoCard, error) {
	title := strings.TrimSpace(req.Title)
	if len(title) == 0 || len(title) > 100 || req.VideoHeader == nil || req.CoverHeader == nil {
		return nil, appErrors.ErrInvalidParams
	}

	author, err := s.repo.GetUserByID(ctx, operatorUserID)
	if err != nil {
		return nil, err
	}

	videoSaveResult, err := s.saveFile(req.VideoHeader, s.storage.SaveVideo)
	if err != nil {
		return nil, err
	}

	coverSaveResult, err := s.saveFile(req.CoverHeader, s.storage.SaveCover)
	if err != nil {
		_ = s.storage.Delete(videoSaveResult.RelativePath)
		return nil, err
	}

	now := time.Now().UTC()
	video := &model.Video{
		AuthorID:       operatorUserID,
		Title:          title,
		VideoPath:      videoSaveResult.RelativePath,
		CoverPath:      coverSaveResult.RelativePath,
		VideoSizeBytes: videoSaveResult.SizeBytes,
		Status:         model.VideoStatusPublished,
		PublishedAt:    now,
	}

	if err := s.repo.CreateVideoWithStats(ctx, video); err != nil {
		_ = s.storage.Delete(videoSaveResult.RelativePath)
		_ = s.storage.Delete(coverSaveResult.RelativePath)
		return nil, err
	}

	stats := &model.VideoStats{VideoID: video.ID}
	s.storePublicFeedModelsBestEffort(ctx, video, author, stats)

	if err := s.publisher.PublishVideoPublished(ctx, video.ID, operatorUserID); err != nil {
		log.Printf("publish video.published event failed: video_id=%d err=%v", video.ID, err)
		return nil, appErrors.ServiceUnavailable("rabbitmq unavailable", err)
	}

	return s.buildVideoCard(video, author, stats, ViewerStateObject{}), nil
}

func (s *Service) GetDetail(ctx context.Context, videoID uint64, viewerUserID uint64) (*VideoCard, error) {
	video, err := s.repo.GetVisibleVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	author, err := s.repo.GetUserByID(ctx, video.AuthorID)
	if err != nil {
		return nil, err
	}

	stats, err := s.repo.GetVideoStatsByVideoID(ctx, video.ID)
	if err != nil {
		return nil, err
	}

	viewerState, err := s.buildViewerState(ctx, viewerUserID, video.ID, video.AuthorID, video.PublishedAt)
	if err != nil {
		return nil, err
	}

	return s.buildVideoCard(video, author, stats, viewerState), nil
}

func (s *Service) Update(ctx context.Context, operatorUserID, videoID uint64, req UpdateRequest) (*VideoCard, error) {
	video, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if video.AuthorID != operatorUserID {
		return nil, appErrors.ErrForbidden
	}

	updates := make(map[string]interface{})
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if len(title) == 0 || len(title) > 100 {
			return nil, appErrors.ErrInvalidParams
		}
		updates["title"] = title
	}

	var newCoverPath string
	if req.CoverHeader != nil {
		coverSaveResult, saveErr := s.saveFile(req.CoverHeader, s.storage.SaveCover)
		if saveErr != nil {
			return nil, saveErr
		}
		newCoverPath = coverSaveResult.RelativePath
		updates["cover_path"] = newCoverPath
	}

	if len(updates) == 0 {
		return nil, appErrors.ErrInvalidParams
	}

	if err := s.repo.UpdateVideoByID(ctx, videoID, updates); err != nil {
		if newCoverPath != "" {
			_ = s.storage.Delete(newCoverPath)
		}
		return nil, err
	}

	updatedVideo, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		return nil, err
	}

	author, err := s.repo.GetUserByID(ctx, updatedVideo.AuthorID)
	if err != nil {
		return nil, err
	}

	stats, err := s.repo.GetVideoStatsByVideoID(ctx, updatedVideo.ID)
	if err != nil {
		return nil, err
	}

	s.storeVideoBaseBestEffort(ctx, updatedVideo)
	return s.buildVideoCard(updatedVideo, author, stats, ViewerStateObject{}), nil
}

func (s *Service) Delete(ctx context.Context, operatorUserID, videoID uint64) error {
	video, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		return err
	}
	if video.AuthorID != operatorUserID {
		return appErrors.ErrForbidden
	}

	if err := s.repo.SoftDeleteVideoByID(ctx, videoID); err != nil {
		return err
	}

	s.deleteVideoCachesBestEffort(ctx, videoID)

	if err := s.publisher.PublishVideoDeleted(ctx, videoID, operatorUserID); err != nil {
		log.Printf("publish video.deleted event failed: video_id=%d err=%v", videoID, err)
		return appErrors.ServiceUnavailable("rabbitmq unavailable", err)
	}

	return nil
}

func (s *Service) saveFile(
	header *multipart.FileHeader,
	saveFunc func(multipart.File, *multipart.FileHeader) (*filestorage.SaveResult, error),
) (*filestorage.SaveResult, error) {
	file, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("open multipart file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	result, err := saveFunc(file, header)
	if err != nil {
		return nil, mapStorageError(err)
	}

	return result, nil
}

func (s *Service) buildViewerState(ctx context.Context, viewerUserID, videoID, authorID uint64, publishedAt time.Time) (ViewerStateObject, error) {
	if viewerUserID == 0 {
		return ViewerStateObject{}, nil
	}

	if s.cache != nil {
		relations, err := s.cache.LoadViewerRelations(ctx, viewerUserID, []feedcache.ViewerRelationVideo{{
			VideoID:     videoID,
			PublishedAt: publishedAt,
		}}, []uint64{authorID})
		if err == nil {
			_, liked := relations.LikedVideoIDs[videoID]
			_, favorited := relations.FavoritedVideoIDs[videoID]
			_, followingAuthor := relations.FollowedAuthorIDs[authorID]
			if viewerUserID == authorID {
				followingAuthor = false
			}

			return ViewerStateObject{
				Liked:           liked,
				Favorited:       favorited,
				FollowingAuthor: followingAuthor,
			}, nil
		}
		log.Printf("load viewer state from cache failed, fallback to mysql: video_id=%d viewer_user_id=%d err=%v", videoID, viewerUserID, err)
	}

	liked, err := s.repo.ExistsVideoLike(ctx, viewerUserID, videoID)
	if err != nil {
		return ViewerStateObject{}, err
	}

	favorited, err := s.repo.ExistsVideoFavorite(ctx, viewerUserID, videoID)
	if err != nil {
		return ViewerStateObject{}, err
	}

	followingAuthor := false
	if viewerUserID != authorID {
		followingAuthor, err = s.repo.ExistsUserFollow(ctx, viewerUserID, authorID)
		if err != nil {
			return ViewerStateObject{}, err
		}
	}

	return ViewerStateObject{
		Liked:           liked,
		Favorited:       favorited,
		FollowingAuthor: followingAuthor,
	}, nil
}

func (s *Service) buildVideoCard(
	video *model.Video,
	author *model.User,
	stats *model.VideoStats,
	viewerState ViewerStateObject,
) *VideoCard {
	return &VideoCard{
		ID:          video.ID,
		Title:       video.Title,
		VideoURL:    filestorage.BuildStaticURL(s.staticBaseURL, video.VideoPath),
		CoverURL:    filestorage.BuildStaticURL(s.staticBaseURL, video.CoverPath),
		PublishedAt: video.PublishedAt,
		Author: UserSummary{
			ID:        author.ID,
			Nickname:  author.Nickname,
			AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, author.AvatarPath),
		},
		Stats: VideoStatsObject{
			LikeCount:     stats.LikeCount,
			CommentCount:  stats.CommentCount,
			FavoriteCount: stats.FavoriteCount,
		},
		ViewerState: viewerState,
	}
}

func (s *Service) storePublicFeedModelsBestEffort(ctx context.Context, video *model.Video, author *model.User, stats *model.VideoStats) {
	if s.cache == nil {
		return
	}

	s.storeVideoBaseBestEffort(ctx, video)
	s.storeUserBriefBestEffort(ctx, author)
	s.storeVideoStatsBestEffort(ctx, stats)
}

func (s *Service) storeVideoBaseBestEffort(ctx context.Context, video *model.Video) {
	if s.cache == nil {
		return
	}

	base := feedcache.NewVideoBase(video)
	if base == nil {
		return
	}

	if err := s.cache.StoreVideoBase(ctx, base); err != nil {
		log.Printf("store video base cache failed: video_id=%d err=%v", video.ID, err)
	}
}

func (s *Service) storeUserBriefBestEffort(ctx context.Context, user *model.User) {
	if s.cache == nil {
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

func (s *Service) storeVideoStatsBestEffort(ctx context.Context, stats *model.VideoStats) {
	if s.cache == nil {
		return
	}

	cachedStats := feedcache.NewVideoStats(stats)
	if cachedStats == nil {
		return
	}

	if err := s.cache.StoreVideoStats(ctx, cachedStats); err != nil {
		log.Printf("store video stats cache failed: video_id=%d err=%v", stats.VideoID, err)
	}
}

func (s *Service) deleteVideoCachesBestEffort(ctx context.Context, videoID uint64) {
	if s.cache == nil || videoID == 0 {
		return
	}

	if err := s.cache.DeleteVideoBasesByVideoIDs(ctx, []uint64{videoID}); err != nil {
		log.Printf("delete video base cache failed: video_id=%d err=%v", videoID, err)
	}
	if err := s.cache.DeleteVideoStatsByVideoIDs(ctx, []uint64{videoID}); err != nil {
		log.Printf("delete video stats cache failed: video_id=%d err=%v", videoID, err)
	}
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
