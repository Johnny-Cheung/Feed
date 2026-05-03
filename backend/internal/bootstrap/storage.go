package bootstrap

import filestorage "feed-backend/internal/infra/storage"

// NewStorage 把 bootstrap 层的配置结构转换成 storage 层自己的配置结构。
// 这样 storage 包就不需要直接依赖 bootstrap 包，职责会更清晰。
func NewStorage(cfg StorageConfig) (*filestorage.LocalStorage, error) {
	return filestorage.NewLocalStorage(filestorage.Config{
		RootDir:    cfg.RootDir,
		VideosDir:  cfg.VideosDir,
		CoversDir:  cfg.CoversDir,
		AvatarsDir: cfg.AvatarsDir,
		MaxVideoMB: cfg.MaxVideoMB,
		MaxImageMB: cfg.MaxImageMB,
	})
}
