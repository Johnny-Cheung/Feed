package bootstrap

import (
	"fmt"
	"log"

	"feed-backend/internal/model"

	"gorm.io/gorm"
)

// RunAutoMigrate 执行项目的自动建表流程。
// 它是 bootstrap 层对 model.AutoMigrate 的一层封装：
// - model 包负责定义“有哪些表”
// - bootstrap 包负责决定“启动时什么时候执行迁移”
func RunAutoMigrate(db *gorm.DB) error {
	log.Println("running gorm auto migrate")

	if err := model.AutoMigrate(db); err != nil {
		return fmt.Errorf("auto migrate schema: %w", err)
	}

	log.Println("gorm auto migrate completed")
	return nil
}
