package interaction

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/model"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

type publishedVideoRef struct {
	VideoID     uint64    `gorm:"column:video_id"`
	PublishedAt time.Time `gorm:"column:published_at"`
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetVisibleVideoByID(ctx context.Context, videoID uint64) (*model.Video, error) {
	var video model.Video
	err := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", videoID, model.VideoStatusPublished).
		First(&video).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrVideoNotFound
		}
		return nil, fmt.Errorf("get visible video by id: %w", err)
	}

	return &video, nil
}

func (r *Repository) GetVideoByIDIncludingDeleted(ctx context.Context, videoID uint64) (*model.Video, error) {
	var video model.Video
	err := r.db.WithContext(ctx).Unscoped().First(&video, videoID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrVideoNotFound
		}
		return nil, fmt.Errorf("get video by id including deleted: %w", err)
	}

	return &video, nil
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

func (r *Repository) ListFollowerUserIDsByAuthorID(ctx context.Context, authorID uint64) ([]uint64, error) {
	if authorID == 0 {
		return nil, nil
	}

	var userIDs []uint64
	if err := r.db.WithContext(ctx).
		Table("user_follows").
		Select("user_id").
		Where("follow_user_id = ?", authorID).
		Scan(&userIDs).Error; err != nil {
		return nil, fmt.Errorf("list follower user ids by author id: %w", err)
	}
	return userIDs, nil
}

func (r *Repository) CountFollowersByAuthorID(ctx context.Context, authorID uint64) (int64, error) {
	if authorID == 0 {
		return 0, nil
	}

	var count int64
	if err := r.db.WithContext(ctx).
		Table("user_follows").
		Where("follow_user_id = ?", authorID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count followers by author id: %w", err)
	}
	return count, nil
}

func (r *Repository) ListRecentVisibleVideoRefsByAuthorID(ctx context.Context, authorID uint64, limit int) ([]publishedVideoRef, error) {
	if authorID == 0 || limit <= 0 {
		return nil, nil
	}

	var refs []publishedVideoRef
	if err := r.db.WithContext(ctx).
		Table("videos").
		Select("id AS video_id, published_at AS published_at").
		Where("author_id = ? AND status = ?", authorID, model.VideoStatusPublished).
		Where("deleted_at IS NULL").
		Order("published_at DESC").
		Order("id DESC").
		Limit(limit).
		Scan(&refs).Error; err != nil {
		return nil, fmt.Errorf("list recent visible video refs by author id: %w", err)
	}
	return refs, nil
}

func (r *Repository) CreateLikeIfAbsent(ctx context.Context, userID, videoID uint64) (bool, error) {
	like := &model.VideoLike{
		UserID:  userID,
		VideoID: videoID,
	}
	if err := r.db.WithContext(ctx).Create(like).Error; err != nil {
		if isDuplicateEntry(err) {
			return false, nil
		}
		return false, fmt.Errorf("create like: %w", err)
	}
	return true, nil
}

func (r *Repository) DeleteLikeIfExists(ctx context.Context, userID, videoID uint64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		Delete(&model.VideoLike{})
	if result.Error != nil {
		return false, fmt.Errorf("delete like: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *Repository) CreateFavoriteIfAbsent(ctx context.Context, userID, videoID uint64) (bool, error) {
	favorite := &model.VideoFavorite{
		UserID:  userID,
		VideoID: videoID,
	}
	if err := r.db.WithContext(ctx).Create(favorite).Error; err != nil {
		if isDuplicateEntry(err) {
			return false, nil
		}
		return false, fmt.Errorf("create favorite: %w", err)
	}
	return true, nil
}

func (r *Repository) DeleteFavoriteIfExists(ctx context.Context, userID, videoID uint64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		Delete(&model.VideoFavorite{})
	if result.Error != nil {
		return false, fmt.Errorf("delete favorite: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *Repository) CreateFollowIfAbsent(ctx context.Context, userID, followUserID uint64) (bool, error) {
	follow := &model.UserFollow{
		UserID:       userID,
		FollowUserID: followUserID,
	}
	if err := r.db.WithContext(ctx).Create(follow).Error; err != nil {
		if isDuplicateEntry(err) {
			return false, nil
		}
		return false, fmt.Errorf("create follow: %w", err)
	}
	return true, nil
}

func (r *Repository) DeleteFollowIfExists(ctx context.Context, userID, followUserID uint64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND follow_user_id = ?", userID, followUserID).
		Delete(&model.UserFollow{})
	if result.Error != nil {
		return false, fmt.Errorf("delete follow: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *Repository) IsFollowing(ctx context.Context, userID, followUserID uint64) (bool, error) {
	if userID == 0 || followUserID == 0 {
		return false, nil
	}

	var count int64
	if err := r.db.WithContext(ctx).
		Model(&model.UserFollow{}).
		Where("user_id = ? AND follow_user_id = ?", userID, followUserID).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check follow relation: %w", err)
	}
	return count > 0, nil
}

func (r *Repository) CreateComment(ctx context.Context, comment *model.Comment) error {
	if err := r.db.WithContext(ctx).Create(comment).Error; err != nil {
		return fmt.Errorf("create comment: %w", err)
	}
	return nil
}

func (r *Repository) GetActiveCommentByID(ctx context.Context, commentID uint64) (*model.Comment, error) {
	var comment model.Comment
	err := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", commentID, model.CommentStatusNormal).
		First(&comment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrCommentNotFound
		}
		return nil, fmt.Errorf("get active comment by id: %w", err)
	}

	return &comment, nil
}

func (r *Repository) SoftDeleteCommentByID(ctx context.Context, commentID, userID uint64) (bool, error) {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&model.Comment{}).
		Where("id = ? AND user_id = ? AND status = ?", commentID, userID, model.CommentStatusNormal).
		Updates(map[string]interface{}{
			"status":     model.CommentStatusDeleted,
			"deleted_at": now,
			"updated_at": now,
		})
	if result.Error != nil {
		return false, fmt.Errorf("soft delete comment by id: %w", result.Error)
	}

	return result.RowsAffected > 0, nil
}

func (r *Repository) ListActiveCommentsByVideoID(ctx context.Context, videoID uint64, cursor *commentCursor, count int) ([]model.Comment, error) {
	if count <= 0 {
		return nil, nil
	}

	var comments []model.Comment
	query := r.db.WithContext(ctx).
		Where("video_id = ? AND status = ?", videoID, model.CommentStatusNormal)

	if cursor != nil {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.Time.UTC(),
			cursor.Time.UTC(),
			cursor.ID,
		)
	}

	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Limit(count).
		Find(&comments).Error; err != nil {
		return nil, fmt.Errorf("list active comments by video id: %w", err)
	}

	return comments, nil
}

func (r *Repository) CountLikesByVideoID(ctx context.Context, videoID uint64) (uint32, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.VideoLike{}).Where("video_id = ?", videoID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count likes by video id: %w", err)
	}
	return clampToUint32(count), nil
}

func (r *Repository) CountFavoritesByVideoID(ctx context.Context, videoID uint64) (uint32, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.VideoFavorite{}).Where("video_id = ?", videoID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count favorites by video id: %w", err)
	}
	return clampToUint32(count), nil
}

func (r *Repository) CountCommentsByVideoID(ctx context.Context, videoID uint64) (uint32, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&model.Comment{}).
		Where("video_id = ? AND status = ?", videoID, model.CommentStatusNormal).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count comments by video id: %w", err)
	}
	return clampToUint32(count), nil
}

func (r *Repository) GetVideoStatsByVideoID(ctx context.Context, videoID uint64) (*model.VideoStats, error) {
	var stats model.VideoStats
	err := r.db.WithContext(ctx).First(&stats, "video_id = ?", videoID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &model.VideoStats{VideoID: videoID}, nil
		}
		return nil, fmt.Errorf("get video stats by video id: %w", err)
	}
	return &stats, nil
}

func (r *Repository) UpsertVideoStats(ctx context.Context, stats *model.VideoStats) error {
	now := time.Now().UTC()
	stats.UpdatedAt = now
	if stats.CreatedAt.IsZero() {
		stats.CreatedAt = now
	}

	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "video_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"like_count":     stats.LikeCount,
				"comment_count":  stats.CommentCount,
				"favorite_count": stats.FavoriteCount,
				"hot_score":      stats.HotScore,
				"updated_at":     stats.UpdatedAt,
			}),
		}).
		Create(stats).Error; err != nil {
		return fmt.Errorf("upsert video stats: %w", err)
	}

	return nil
}

func isDuplicateEntry(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}

func clampToUint32(count int64) uint32 {
	if count <= 0 {
		return 0
	}
	if count >= math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(count)
}

func normalizeCommentContent(content string) string {
	return strings.TrimSpace(content)
}
