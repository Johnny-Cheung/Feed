package bootstrap

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 是整个项目的总配置结构体。
// 配置文件中的每一段都映射到这里的一个字段。
type Config struct {
	App        AppConfig        `mapstructure:"app"`
	MySQL      MySQLConfig      `mapstructure:"mysql"`
	Redis      RedisConfig      `mapstructure:"redis"`
	RabbitMQ   RabbitMQConfig   `mapstructure:"rabbitmq"`
	JWT        JWTConfig        `mapstructure:"jwt"`
	Storage    StorageConfig    `mapstructure:"storage"`
	Pagination PaginationConfig `mapstructure:"pagination"`
	Feed       FeedConfig       `mapstructure:"feed"`
	CORS       CORSConfig       `mapstructure:"cors"`
	Jobs       JobsConfig       `mapstructure:"jobs"`
}

// AppConfig 存放服务自身的运行配置。
type AppConfig struct {
	Name          string `mapstructure:"name"`
	Env           string `mapstructure:"env"`
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	ReadTimeout   string `mapstructure:"read_timeout"`
	WriteTimeout  string `mapstructure:"write_timeout"`
	StaticBaseURL string `mapstructure:"static_base_url"`
}

// MySQLConfig 存放数据库连接配置。
type MySQLConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	User            string `mapstructure:"user"`
	Password        string `mapstructure:"password"`
	DBName          string `mapstructure:"dbname"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	ConnMaxLifetime string `mapstructure:"conn_max_lifetime"`
}

// RedisConfig 存放 Redis 连接配置。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// RabbitMQConfig 存放 RabbitMQ 配置。
type RabbitMQConfig struct {
	URL                 string `mapstructure:"url"`
	Exchange            string `mapstructure:"exchange"`
	QueueVideoStats     string `mapstructure:"queue_video_stats"`
	QueueHotFeed        string `mapstructure:"queue_hotfeed"`
	QueueFollowingInbox string `mapstructure:"queue_following_inbox"`
	QueueUserFollow     string `mapstructure:"queue_user_follow"`
}

// JWTConfig 存放鉴权相关配置。
type JWTConfig struct {
	Secret      string `mapstructure:"secret"`
	ExpireHours int    `mapstructure:"expire_hours"`
}

// StorageConfig 存放本地文件存储配置。
type StorageConfig struct {
	RootDir    string `mapstructure:"root_dir"`
	VideosDir  string `mapstructure:"videos_dir"`
	CoversDir  string `mapstructure:"covers_dir"`
	AvatarsDir string `mapstructure:"avatars_dir"`
	MaxVideoMB int    `mapstructure:"max_video_mb"`
	MaxImageMB int    `mapstructure:"max_image_mb"`
}

// PaginationConfig 存放分页默认值配置。
type PaginationConfig struct {
	DefaultLimit int `mapstructure:"default_limit"`
	MaxLimit     int `mapstructure:"max_limit"`
}

type FeedConfig struct {
	FollowingPullModeThreshold    int    `mapstructure:"following_pull_mode_threshold"`
	HomeHotMaxEntries             int    `mapstructure:"home_hot_max_entries"`
	VideoRelationStreamMaxEntries int    `mapstructure:"video_relation_stream_max_entries"`
	HotCommentCacheEntries        int    `mapstructure:"hot_comment_cache_entries"`
	HotCommentCacheTTL            string `mapstructure:"hot_comment_cache_ttl"`
}

// CORSConfig 存放跨域配置。
type CORSConfig struct {
	AllowOrigins []string `mapstructure:"allow_origins"`
}

// JobsConfig 存放后台修复任务配置。
type JobsConfig struct {
	Enabled                  bool   `mapstructure:"enabled"`
	RunOnStart               bool   `mapstructure:"run_on_start"`
	VideoStatsReconcileEvery string `mapstructure:"video_stats_reconcile_every"`
	HotFeedDirtyRefreshEvery string `mapstructure:"hot_feed_dirty_refresh_every"`
	HotFeedRebuildEvery      string `mapstructure:"hot_feed_rebuild_every"`
	LockTTL                  string `mapstructure:"lock_ttl"`
	BatchSize                int    `mapstructure:"batch_size"`
}

// LoadConfig 从配置文件和环境变量中读取配置。
// 当前读取顺序是：
// 1. 先加载 configs/config.yaml
// 2. 再允许环境变量覆盖同名字段
func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigFile("configs/config.yaml")
	setConfigDefaults(v)

	// 环境变量统一使用 FEED_ 前缀，例如：
	// FEED_APP_PORT=18080
	// FEED_MYSQL_HOST=127.0.0.1
	v.SetEnvPrefix("FEED")

	// 因为配置层级使用了点号，例如 app.port，
	// 所以这里把点号替换成下划线，方便环境变量映射。
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setConfigDefaults(v *viper.Viper) {
	v.SetDefault("jobs.enabled", true)
	v.SetDefault("jobs.run_on_start", false)
	v.SetDefault("jobs.video_stats_reconcile_every", "24h")
	v.SetDefault("jobs.hot_feed_dirty_refresh_every", "1m")
	v.SetDefault("jobs.hot_feed_rebuild_every", "1h")
	v.SetDefault("jobs.lock_ttl", "30m")
	v.SetDefault("jobs.batch_size", 500)
	v.SetDefault("feed.following_pull_mode_threshold", 10000)
	v.SetDefault("feed.home_hot_max_entries", 10000)
	v.SetDefault("feed.video_relation_stream_max_entries", 1000000)
	v.SetDefault("feed.hot_comment_cache_entries", 50)
	v.SetDefault("feed.hot_comment_cache_ttl", "1h")
}

// Validate 对关键配置做最基本的合法性检查。
// 这样可以尽量在服务启动阶段暴露错误，而不是运行到一半才发现配置不对。
func (c *Config) Validate() error {
	if c.App.Name == "" {
		return fmt.Errorf("app.name is required")
	}
	// static_base_url 用来把相对路径拼成完整静态资源地址。
	// 例如头像路径 avatars/2026/04/24/a.png 会被组装成：
	// http://localhost:18080/static/avatars/2026/04/24/a.png
	if c.App.StaticBaseURL == "" {
		return fmt.Errorf("app.static_base_url is required")
	}
	if c.App.Host == "" {
		return fmt.Errorf("app.host is required")
	}
	if c.App.Port <= 0 {
		return fmt.Errorf("app.port must be positive")
	}
	if _, err := c.App.ReadTimeoutDuration(); err != nil {
		return fmt.Errorf("app.read_timeout invalid: %w", err)
	}
	if _, err := c.App.WriteTimeoutDuration(); err != nil {
		return fmt.Errorf("app.write_timeout invalid: %w", err)
	}
	if c.MySQL.Host == "" || c.MySQL.Port <= 0 || c.MySQL.User == "" || c.MySQL.DBName == "" {
		return fmt.Errorf("mysql config is incomplete")
	}
	if _, err := c.MySQL.ConnMaxLifetimeDuration(); err != nil {
		return fmt.Errorf("mysql.conn_max_lifetime invalid: %w", err)
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if c.RabbitMQ.URL == "" || c.RabbitMQ.Exchange == "" {
		return fmt.Errorf("rabbitmq config is incomplete")
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("jwt.secret is required")
	}
	if c.JWT.ExpireHours <= 0 {
		return fmt.Errorf("jwt.expire_hours must be positive")
	}
	if c.Storage.RootDir == "" || c.Storage.VideosDir == "" || c.Storage.CoversDir == "" || c.Storage.AvatarsDir == "" {
		return fmt.Errorf("storage config is incomplete")
	}
	// 阶段五开始真正使用 max_video_mb / max_image_mb，
	// 所以这里要在启动阶段提前校验，避免运行到一半才发现配置错误。
	if c.Storage.MaxVideoMB <= 0 || c.Storage.MaxImageMB <= 0 {
		return fmt.Errorf("storage max size config must be positive")
	}
	if c.Pagination.DefaultLimit <= 0 || c.Pagination.MaxLimit <= 0 {
		return fmt.Errorf("pagination limits must be positive")
	}
	if c.Pagination.DefaultLimit > c.Pagination.MaxLimit {
		return fmt.Errorf("pagination.default_limit cannot be greater than pagination.max_limit")
	}
	if c.Feed.FollowingPullModeThreshold <= 0 {
		return fmt.Errorf("feed.following_pull_mode_threshold must be positive")
	}
	if c.Feed.HomeHotMaxEntries <= 0 {
		return fmt.Errorf("feed.home_hot_max_entries must be positive")
	}
	if c.Feed.VideoRelationStreamMaxEntries <= 0 {
		return fmt.Errorf("feed.video_relation_stream_max_entries must be positive")
	}
	if c.Feed.HotCommentCacheEntries <= 0 {
		return fmt.Errorf("feed.hot_comment_cache_entries must be positive")
	}
	if ttl, err := c.Feed.HotCommentCacheTTLDuration(); err != nil {
		return fmt.Errorf("feed.hot_comment_cache_ttl invalid: %w", err)
	} else if ttl <= 0 {
		return fmt.Errorf("feed.hot_comment_cache_ttl must be positive")
	}
	if _, err := c.Jobs.VideoStatsReconcileEveryDuration(); err != nil {
		return fmt.Errorf("jobs.video_stats_reconcile_every invalid: %w", err)
	}
	if _, err := c.Jobs.HotFeedDirtyRefreshEveryDuration(); err != nil {
		return fmt.Errorf("jobs.hot_feed_dirty_refresh_every invalid: %w", err)
	}
	if _, err := c.Jobs.HotFeedRebuildEveryDuration(); err != nil {
		return fmt.Errorf("jobs.hot_feed_rebuild_every invalid: %w", err)
	}
	if _, err := c.Jobs.LockTTLDuration(); err != nil {
		return fmt.Errorf("jobs.lock_ttl invalid: %w", err)
	}
	if c.Jobs.BatchSize <= 0 {
		return fmt.Errorf("jobs.batch_size must be positive")
	}
	return nil
}

// Address 把 host 和 port 组合成标准监听地址。
// 例如：0.0.0.0:18080
func (c AppConfig) Address() string {
	return net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
}

// ReadTimeoutDuration 把配置中的读超时字符串解析为 time.Duration。
func (c AppConfig) ReadTimeoutDuration() (time.Duration, error) {
	return time.ParseDuration(c.ReadTimeout)
}

// WriteTimeoutDuration 把配置中的写超时字符串解析为 time.Duration。
func (c AppConfig) WriteTimeoutDuration() (time.Duration, error) {
	return time.ParseDuration(c.WriteTimeout)
}

// ConnMaxLifetimeDuration 把数据库连接最大存活时间解析为 time.Duration。
func (c MySQLConfig) ConnMaxLifetimeDuration() (time.Duration, error) {
	return time.ParseDuration(c.ConnMaxLifetime)
}

// DSN 生成 MySQL 连接字符串。
// Gorm 底层会使用这个 DSN 去连接数据库。
func (c MySQLConfig) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=UTC",
		c.User,
		c.Password,
		c.Host,
		c.Port,
		c.DBName,
	)
}

// HotCommentCacheTTLDuration 解析热榜评论缓存 TTL。
func (c FeedConfig) HotCommentCacheTTLDuration() (time.Duration, error) {
	return time.ParseDuration(c.HotCommentCacheTTL)
}

// VideoStatsReconcileEveryDuration 解析统计对账任务执行间隔。
func (c JobsConfig) VideoStatsReconcileEveryDuration() (time.Duration, error) {
	return time.ParseDuration(c.VideoStatsReconcileEvery)
}

// HotFeedDirtyRefreshEveryDuration 解析热榜增量刷新任务执行间隔。
func (c JobsConfig) HotFeedDirtyRefreshEveryDuration() (time.Duration, error) {
	return time.ParseDuration(c.HotFeedDirtyRefreshEvery)
}

// HotFeedRebuildEveryDuration 解析热榜重建任务执行间隔。
func (c JobsConfig) HotFeedRebuildEveryDuration() (time.Duration, error) {
	return time.ParseDuration(c.HotFeedRebuildEvery)
}

// LockTTLDuration 解析后台任务分布式锁的过期时间。
func (c JobsConfig) LockTTLDuration() (time.Duration, error) {
	return time.ParseDuration(c.LockTTL)
}
