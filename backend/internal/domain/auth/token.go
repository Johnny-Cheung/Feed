package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是当前项目的 JWT 载荷结构。
// 这里暂时只扩展一个 UserID，其余通用字段复用 RegisteredClaims。
type Claims struct {
	UserID uint64 `json:"uid"`
	jwt.RegisteredClaims
}

// TokenManager 负责生成和解析 access token。
type TokenManager struct {
	secret      []byte
	expireHours int
}

// NewTokenManager 创建 JWT 管理器。
func NewTokenManager(secret string, expireHours int) *TokenManager {
	return &TokenManager{
		secret:      []byte(secret),
		expireHours: expireHours,
	}
}

// GenerateAccessToken 生成 access token，并返回 token 字符串和过期秒数。
func (m *TokenManager) GenerateAccessToken(userID uint64) (string, int64, error) {
	now := time.Now().UTC()
	expireAt := now.Add(time.Duration(m.expireHours) * time.Hour)

	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expireAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(m.secret)
	if err != nil {
		return "", 0, fmt.Errorf("sign jwt: %w", err)
	}

	expiresIn := int64((time.Duration(m.expireHours) * time.Hour) / time.Second)
	return signedToken, expiresIn, nil
}

// ParseAccessToken 解析并校验 access token。
func (m *TokenManager) ParseAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// 这里显式限制只接受 HS256 这类 HMAC 签名方法，
		// 避免被其他签名算法混淆。
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse jwt: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("jwt claims invalid")
	}

	if claims.UserID == 0 {
		// 理论上这里不会发生，因为 GenerateAccessToken 时一定会带上 uid。
		// 这里额外兜底是为了防止未来手工构造或旧 token 格式异常。
		userID, convErr := strconv.ParseUint(claims.Subject, 10, 64)
		if convErr != nil {
			return nil, fmt.Errorf("parse subject as user id: %w", convErr)
		}
		claims.UserID = userID
	}

	return claims, nil
}
