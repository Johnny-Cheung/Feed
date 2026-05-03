package model

import "gorm.io/gorm"

// AutoMigrate 统一执行当前项目的表结构迁移。
// 当前阶段我们采用 Gorm AutoMigrate，而不是手写 SQL migration。
//
// 这样做的优点是：
// 1. 对初学者更容易理解
// 2. 模型和表结构放在一起，修改时不容易漏
// 3. 先把第一版业务表快速落下来
//
// 注意：
// AutoMigrate 很适合当前个人项目的早期阶段，
// 但后期如果表结构演进更复杂，仍然建议再补正式 migration 体系。
func AutoMigrate(db *gorm.DB) error {
	// 这里通过 table_options 指定 MySQL 建表选项。
	// 这样新建出来的表会统一使用 InnoDB 和 utf8mb4。
	return db.
		Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci").
		AutoMigrate(
			&User{},
			&Video{},
			&VideoStats{},
			&VideoLike{},
			&VideoFavorite{},
			&Comment{},
			&UserFollow{},
		)
}
