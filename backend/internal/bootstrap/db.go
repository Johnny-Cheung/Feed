package bootstrap

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewMySQL 创建并验证 MySQL 连接。
// 如果这里返回成功，说明：
// 1. 连接字符串可用
// 2. 数据库连接已建立
// 3. 基础 ping 检查通过
func NewMySQL(cfg MySQLConfig) (*gorm.DB, error) {
	// 使用 Gorm 打开数据库连接。
	// 当前把日志级别设成 Silent，避免初始化阶段 SQL 日志过多。
	db, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}

	// 这里设置的是数据库连接池参数，
	// 不是“只建立多少条连接”。
	// Go 会按需要复用和创建连接。
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)

	connMaxLifetime, err := cfg.ConnMaxLifetimeDuration()
	if err != nil {
		return nil, err
	}
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	// 给 ping 一个超时时间，避免数据库异常时卡死启动流程。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 主动 ping 一次，尽早发现数据库不可达问题。
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return db, nil
}
