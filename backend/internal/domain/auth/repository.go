package auth

import (
	"context"
	"errors"
	"fmt"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/model"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Repository 负责认证领域和 users 表之间的数据交互。
// 这里先保持职责简单：
// - 按用户名查用户
// - 按 ID 查用户
// - 创建用户
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建认证领域仓储对象。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// GetByUsername 按用户名查询用户。
func (r *Repository) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErrors.ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by username: %w", err)
	}

	return &user, nil
}

// GetByID 按用户 ID 查询用户。
func (r *Repository) GetByID(ctx context.Context, userID uint64) (*model.User, error) {
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

// Create 创建用户。
func (r *Repository) Create(ctx context.Context, user *model.User) error {
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		if isDuplicateEntry(err) {
			return appErrors.ErrUsernameExists
		}
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// isDuplicateEntry 判断是否为 MySQL 唯一约束冲突。
func isDuplicateEntry(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}
