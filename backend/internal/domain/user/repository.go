package user

import (
	"context"
	"errors"
	"fmt"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/model"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetUserByID(ctx context.Context, userID uint64) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).First(&user, userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &user, nil
}

func (r *Repository) GetUsersByIDs(ctx context.Context, userIDs []uint64) ([]model.User, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	var users []model.User
	if err := r.db.WithContext(ctx).Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, fmt.Errorf("get users by ids: %w", err)
	}
	return users, nil
}

func (r *Repository) UpdateProfile(ctx context.Context, userID uint64, nickname, bio string) error {
	result := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"nickname": nickname,
			"bio":      bio,
		})
	if result.Error != nil {
		return fmt.Errorf("update user profile: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return appErrors.ErrUserNotFound
	}
	return nil
}

func (r *Repository) UpdateAvatar(ctx context.Context, userID uint64, avatarPath string) error {
	result := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Update("avatar_path", avatarPath)
	if result.Error != nil {
		return fmt.Errorf("update user avatar: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return appErrors.ErrUserNotFound
	}
	return nil
}

func (r *Repository) UpdatePasswordHash(ctx context.Context, userID uint64, passwordHash string) error {
	result := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Update("password_hash", passwordHash)
	if result.Error != nil {
		return fmt.Errorf("update password hash: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return appErrors.ErrUserNotFound
	}
	return nil
}

func (r *Repository) ListAuthorVideoRefs(ctx context.Context, authorID uint64, cursor *timeCursor, count int) ([]videoRef, error) {
	if count <= 0 {
		return nil, nil
	}

	var refs []videoRef
	query := r.db.WithContext(ctx).
		Table("videos").
		Select("id AS video_id, published_at AS cursor_time").
		Where("author_id = ? AND status = ?", authorID, model.VideoStatusPublished).
		Where("deleted_at IS NULL")

	if cursor != nil {
		query = query.Where(
			"(published_at < ?) OR (published_at = ? AND id < ?)",
			cursor.Time.UTC(),
			cursor.Time.UTC(),
			cursor.ID,
		)
	}

	if err := query.
		Order("published_at DESC").
		Order("id DESC").
		Limit(count).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("list author video refs: %w", err)
	}
	return refs, nil
}

func (r *Repository) ListLikedVideoRefs(ctx context.Context, userID uint64, cursor *timeCursor, count int) ([]videoRef, error) {
	return r.listInteractionVideoRefs(ctx, "video_likes", userID, cursor, count)
}

func (r *Repository) ListFavoritedVideoRefs(ctx context.Context, userID uint64, cursor *timeCursor, count int) ([]videoRef, error) {
	return r.listInteractionVideoRefs(ctx, "video_favorites", userID, cursor, count)
}

func (r *Repository) listInteractionVideoRefs(ctx context.Context, tableName string, userID uint64, cursor *timeCursor, count int) ([]videoRef, error) {
	if count <= 0 {
		return nil, nil
	}

	var refs []videoRef
	query := r.db.WithContext(ctx).
		Table(tableName+" AS rel").
		Select("rel.video_id AS video_id, rel.created_at AS cursor_time").
		Joins("JOIN videos AS v ON v.id = rel.video_id").
		Where("rel.user_id = ?", userID).
		Where("v.status = ?", model.VideoStatusPublished).
		Where("v.deleted_at IS NULL")

	if cursor != nil {
		query = query.Where(
			"(rel.created_at < ?) OR (rel.created_at = ? AND rel.video_id < ?)",
			cursor.Time.UTC(),
			cursor.Time.UTC(),
			cursor.ID,
		)
	}

	if err := query.
		Order("rel.created_at DESC").
		Order("rel.video_id DESC").
		Limit(count).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("list %s video refs: %w", tableName, err)
	}
	return refs, nil
}

func (r *Repository) ListFollowingRefs(ctx context.Context, userID uint64, cursor *timeCursor, count int) ([]userRef, error) {
	if count <= 0 {
		return nil, nil
	}

	var refs []userRef
	query := r.db.WithContext(ctx).
		Table("user_follows").
		Select("follow_user_id AS user_id, created_at AS cursor_time").
		Where("user_id = ?", userID)

	if cursor != nil {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND follow_user_id < ?)",
			cursor.Time.UTC(),
			cursor.Time.UTC(),
			cursor.ID,
		)
	}

	if err := query.
		Order("created_at DESC").
		Order("follow_user_id DESC").
		Limit(count).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("list following refs: %w", err)
	}
	return refs, nil
}

func (r *Repository) ListFollowerRefs(ctx context.Context, userID uint64, cursor *timeCursor, count int) ([]userRef, error) {
	if count <= 0 {
		return nil, nil
	}

	var refs []userRef
	query := r.db.WithContext(ctx).
		Table("user_follows").
		Select("user_id AS user_id, created_at AS cursor_time").
		Where("follow_user_id = ?", userID)

	if cursor != nil {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND user_id < ?)",
			cursor.Time.UTC(),
			cursor.Time.UTC(),
			cursor.ID,
		)
	}

	if err := query.
		Order("created_at DESC").
		Order("user_id DESC").
		Limit(count).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("list follower refs: %w", err)
	}
	return refs, nil
}

func (r *Repository) GetVisibleVideosByIDs(ctx context.Context, videoIDs []uint64) ([]model.Video, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}

	var videos []model.Video
	if err := r.db.WithContext(ctx).
		Where("id IN ? AND status = ?", videoIDs, model.VideoStatusPublished).
		Find(&videos).Error; err != nil {
		return nil, fmt.Errorf("get visible videos by ids: %w", err)
	}
	return videos, nil
}

func (r *Repository) GetVideoStatsByIDs(ctx context.Context, videoIDs []uint64) ([]model.VideoStats, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}

	var stats []model.VideoStats
	if err := r.db.WithContext(ctx).Where("video_id IN ?", videoIDs).Find(&stats).Error; err != nil {
		return nil, fmt.Errorf("get video stats by ids: %w", err)
	}
	return stats, nil
}

func (r *Repository) GetLikedVideoIDs(ctx context.Context, viewerUserID uint64, videoIDs []uint64) (map[uint64]struct{}, error) {
	return r.getUint64Set(ctx, "video_likes", "video_id", "user_id = ? AND video_id IN ?", viewerUserID, videoIDs)
}

func (r *Repository) GetFavoritedVideoIDs(ctx context.Context, viewerUserID uint64, videoIDs []uint64) (map[uint64]struct{}, error) {
	return r.getUint64Set(ctx, "video_favorites", "video_id", "user_id = ? AND video_id IN ?", viewerUserID, videoIDs)
}

func (r *Repository) GetFollowedAuthorIDs(ctx context.Context, viewerUserID uint64, authorIDs []uint64) (map[uint64]struct{}, error) {
	return r.getUint64Set(ctx, "user_follows", "follow_user_id", "user_id = ? AND follow_user_id IN ?", viewerUserID, authorIDs)
}

func (r *Repository) GetUsersFollowedByViewer(ctx context.Context, viewerUserID uint64, targetUserIDs []uint64) (map[uint64]struct{}, error) {
	return r.getUint64Set(ctx, "user_follows", "follow_user_id", "user_id = ? AND follow_user_id IN ?", viewerUserID, targetUserIDs)
}

func (r *Repository) GetUsersFollowingViewer(ctx context.Context, viewerUserID uint64, targetUserIDs []uint64) (map[uint64]struct{}, error) {
	return r.getUint64Set(ctx, "user_follows", "user_id", "follow_user_id = ? AND user_id IN ?", viewerUserID, targetUserIDs)
}

func (r *Repository) getUint64Set(ctx context.Context, tableName, column, query string, args ...interface{}) (map[uint64]struct{}, error) {
	result := make(map[uint64]struct{})
	if len(args) == 0 {
		return result, nil
	}

	var values []uint64
	if err := r.db.WithContext(ctx).
		Table(tableName).
		Select(column).
		Where(query, args...).
		Scan(&values).Error; err != nil {
		return nil, fmt.Errorf("get uint64 set from %s: %w", tableName, err)
	}

	for _, value := range values {
		result[value] = struct{}{}
	}
	return result, nil
}
