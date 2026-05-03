package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/infra/feedcache"
	filestorage "feed-backend/internal/infra/storage"
	"feed-backend/internal/model"

	"golang.org/x/crypto/bcrypt"
)

// Service 负责认证领域的业务逻辑。
// 这一层会把“用户表读写”“密码处理”“JWT 生成”串起来。
type Service struct {
	repo          *Repository
	tokenManager  *TokenManager
	cache         *feedcache.Cache
	staticBaseURL string
}

// NewService 创建认证业务对象。
func NewService(repo *Repository, tokenManager *TokenManager, cache *feedcache.Cache, staticBaseURL string) *Service {
	return &Service{
		repo:          repo,
		tokenManager:  tokenManager,
		cache:         cache,
		staticBaseURL: staticBaseURL,
	}
}

// Register 处理注册逻辑。
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*TokenResponse, error) {
	username := normalizeUsername(req.Username)
	password := req.Password
	if username == "" || strings.TrimSpace(password) == "" {
		return nil, appErrors.ErrInvalidParams
	}

	// 注册前先查一次用户名是否存在，这样能给前端更明确的反馈。
	_, err := s.repo.GetByUsername(ctx, username)
	if err == nil {
		return nil, appErrors.ErrUsernameExists
	}
	if !errors.Is(err, appErrors.ErrUserNotFound) {
		return nil, err
	}

	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &model.User{
		Username:     username,
		PasswordHash: passwordHash,
		Nickname:     username,
		Status:       model.UserStatusActive,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}

	accessToken, expiresIn, err := s.tokenManager.GenerateAccessToken(user.ID)
	if err != nil {
		return nil, err
	}
	s.markUserActiveBestEffort(ctx, user.ID)
	s.warmViewerRelationsBestEffort(user.ID)

	return &TokenResponse{
		AccessToken: accessToken,
		ExpiresIn:   expiresIn,
	}, nil
}

// Login 处理登录逻辑。
func (s *Service) Login(ctx context.Context, req LoginRequest) (*TokenResponse, error) {
	username := normalizeUsername(req.Username)
	password := req.Password
	if username == "" || strings.TrimSpace(password) == "" {
		return nil, appErrors.ErrInvalidParams
	}

	user, err := s.repo.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, appErrors.ErrUserNotFound) {
			return nil, appErrors.ErrInvalidCredentials
		}
		return nil, err
	}

	if err := ComparePassword(user.PasswordHash, password); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return nil, appErrors.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("compare password: %w", err)
	}

	accessToken, expiresIn, err := s.tokenManager.GenerateAccessToken(user.ID)
	if err != nil {
		return nil, err
	}
	s.markUserActiveBestEffort(ctx, user.ID)
	s.warmViewerRelationsBestEffort(user.ID)

	return &TokenResponse{
		AccessToken: accessToken,
		ExpiresIn:   expiresIn,
	}, nil
}

// GetCurrentUser 返回当前登录用户信息。
func (s *Service) GetCurrentUser(ctx context.Context, userID uint64) (*CurrentUserResponse, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &CurrentUserResponse{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Nickname,
		// 数据库存的是相对路径，这里再统一拼成对前端可直接使用的完整 URL。
		AvatarURL: filestorage.BuildStaticURL(s.staticBaseURL, user.AvatarPath),
		Bio:       user.Bio,
	}, nil
}

// normalizeUsername 对用户名做最基本的标准化处理。
func normalizeUsername(username string) string {
	return strings.TrimSpace(username)
}

func (s *Service) warmViewerRelationsBestEffort(userID uint64) {
	if s.cache == nil || userID == 0 {
		return
	}

	s.cache.WarmViewerRelationsAsync(userID)
}

func (s *Service) markUserActiveBestEffort(ctx context.Context, userID uint64) {
	if s.cache == nil || userID == 0 {
		return
	}
	if err := s.cache.MarkUserActive(ctx, userID); err != nil {
		log.Printf("mark user active failed: user_id=%d err=%v", userID, err)
	}
}
