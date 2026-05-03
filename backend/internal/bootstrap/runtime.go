package bootstrap

import (
	"errors"

	filestorage "feed-backend/internal/infra/storage"

	"github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Runtime 保存整个应用运行时要共享的基础设施对象。
// 可以把它理解成“应用运行时资源包”。
type Runtime struct {
	Config               *Config
	DB                   *gorm.DB
	Redis                *redis.Client
	RabbitMQ             *amqp091.Connection
	RabbitChannel        *amqp091.Channel
	RabbitConfirmChannel *amqp091.Channel
	// Storage 是阶段五新增的本地文件存储服务。
	// 后续上传视频、封面、头像时，业务层都会从这里取用。
	Storage *filestorage.LocalStorage
}

// Close 统一关闭 Runtime 中持有的底层资源。
// 这里不会因为一个资源关闭失败就立刻退出，
// 而是尽量把所有资源都尝试关闭，最后再把错误合并返回。
func (r *Runtime) Close() error {
	var errs []error

	// 先关 channel，再关 RabbitMQ 连接。
	// 因为 channel 依赖 connection 存在。
	if r.RabbitConfirmChannel != nil {
		if err := r.RabbitConfirmChannel.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.RabbitChannel != nil {
		if err := r.RabbitChannel.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// IsClosed 可以避免对已经关闭的连接重复调用 Close。
	if r.RabbitMQ != nil && !r.RabbitMQ.IsClosed() {
		if err := r.RabbitMQ.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// 关闭 Redis 客户端。
	if r.Redis != nil {
		if err := r.Redis.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// Gorm 的 *gorm.DB 需要先取到底层 *sql.DB，才能真正关闭连接池。
	if r.DB != nil {
		sqlDB, err := r.DB.DB()
		if err != nil {
			errs = append(errs, err)
		} else if err := sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
