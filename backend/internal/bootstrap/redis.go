package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedis 创建并验证 Redis 客户端。
func NewRedis(cfg RedisConfig) (*redis.Client, error) {
	// go-redis 返回的是一个客户端对象，
	// 真正的连接会在后续请求时按需建立。
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// 启动阶段主动 Ping 一次，用来确认地址、密码、数据库编号是否正确。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		// 如果 ping 失败，主动关闭客户端，避免留下无效资源。
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}
