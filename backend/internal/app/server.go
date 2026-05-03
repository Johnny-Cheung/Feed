package app

import (
	"context"
	"errors"
	"log"
	"net/http"

	"feed-backend/internal/bootstrap"
	appErrors "feed-backend/internal/common/errors"
	interactionDomain "feed-backend/internal/domain/interaction"
	"feed-backend/internal/jobs"
	"feed-backend/internal/router"
)

// Server 是应用层的总入口对象。
// 它把“运行时依赖”和“HTTP 服务”组织在一起，方便统一启动和关闭。
type Server struct {
	// runtime 保存整个应用运行期间会用到的共享资源：
	// 配置、数据库连接、Redis 客户端、RabbitMQ 连接等。
	runtime *bootstrap.Runtime

	// httpServer 是标准库的 HTTP 服务对象。
	// Gin 最终也是作为它的 Handler 被挂载进去。
	httpServer *http.Server

	// consumers 保存阶段九新增的 RabbitMQ 消费者管理器。
	// 服务关闭时要先停掉这些后台消费者，再关底层连接。
	consumers *interactionDomain.Consumers

	jobScheduler *jobs.Scheduler
}

// NewServer 负责构建一个可以运行的服务实例。
// 这个函数本质上就是“启动前装配流程”。
func NewServer() (*Server, error) {
	// 第一步：加载配置文件并做基础校验。
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return nil, err
	}

	// 第二步：初始化本地文件存储。
	// 这里会同时做两件事：
	// 1. 校验 storage 相关配置
	// 2. 预先创建 storage/videos、storage/covers、storage/avatars 等目录
	storage, err := bootstrap.NewStorage(cfg.Storage)
	if err != nil {
		return nil, errors.Join(appErrors.ErrStorageInit, err)
	}

	// 第三步：初始化 MySQL。
	// 这里会建立连接并主动 ping 一次，避免程序启动后才发现数据库不可用。
	db, err := bootstrap.NewMySQL(cfg.MySQL)
	if err != nil {
		return nil, errors.Join(appErrors.ErrDBInit, err)
	}

	// 第四步：执行 Gorm 自动建表。
	// 当前项目在阶段三先使用 AutoMigrate，让表结构直接从模型生成。
	if err := bootstrap.RunAutoMigrate(db); err != nil {
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
		return nil, errors.Join(appErrors.ErrMigration, err)
	}

	// 第五步：初始化 Redis。
	// 如果 Redis 初始化失败，要把前面已经成功创建的数据库连接关掉，避免资源泄漏。
	redisClient, err := bootstrap.NewRedis(cfg.Redis)
	if err != nil {
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
		return nil, errors.Join(appErrors.ErrRedisInit, err)
	}

	// 第六步：初始化 RabbitMQ，并声明项目需要用到的 exchange。
	// 如果 RabbitMQ 初始化失败，同样要把前面成功创建的资源关掉。
	rabbitConn, rabbitChannel, rabbitConfirmChannel, err := bootstrap.NewRabbitMQ(cfg.RabbitMQ)
	if err != nil {
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
		_ = redisClient.Close()
		return nil, errors.Join(appErrors.ErrRabbitMQInit, err)
	}

	// 第七步：把所有运行时依赖打包进 Runtime。
	// 后面路由、业务模块、定时任务都可以共享这些对象。
	runtime := &bootstrap.Runtime{
		Config:               cfg,
		DB:                   db,
		Redis:                redisClient,
		RabbitMQ:             rabbitConn,
		RabbitChannel:        rabbitChannel,
		RabbitConfirmChannel: rabbitConfirmChannel,
		// 把 Storage 注入到 Runtime 后，路由层和业务层就都能复用它。
		Storage: storage,
	}

	jobOptions, err := schedulerOptionsFromConfig(cfg)
	if err != nil {
		_ = runtime.Close()
		return nil, err
	}

	// 第七步之后：启动阶段九需要的后台消费者。
	// 它们会订阅 RabbitMQ 队列，异步更新 video_stats 和首页热榜。
	consumers, err := interactionDomain.StartConsumers(
		runtime.DB,
		runtime.Redis,
		runtime.RabbitMQ,
		runtime.Config.RabbitMQ.QueueVideoStats,
		runtime.Config.RabbitMQ.QueueHotFeed,
		runtime.Config.RabbitMQ.QueueFollowingInbox,
		runtime.Config.RabbitMQ.QueueUserFollow,
		runtime.Config.Feed.FollowingPullModeThreshold,
		runtime.Config.Feed.HomeHotMaxEntries,
	)
	if err != nil {
		_ = runtime.Close()
		return nil, errors.Join(appErrors.ErrRabbitMQInit, err)
	}

	jobScheduler := jobs.NewScheduler(runtime.DB, runtime.Redis, jobOptions)
	jobScheduler.Start()

	// 第八步：创建 Gin 引擎并注册路由、中间件。
	engine := router.NewEngine(runtime)

	// 第九步：解析配置中的超时时间字符串。
	// 例如 "10s" 会被解析成 time.Duration。
	readTimeout, err := cfg.App.ReadTimeoutDuration()
	if err != nil {
		jobScheduler.Close()
		_ = consumers.Close()
		_ = runtime.Close()
		return nil, err
	}

	writeTimeout, err := cfg.App.WriteTimeoutDuration()
	if err != nil {
		jobScheduler.Close()
		_ = consumers.Close()
		_ = runtime.Close()
		return nil, err
	}

	// 第十步：组装标准库 http.Server。
	// 这里真正确定了服务监听地址和请求处理器。
	httpServer := &http.Server{
		Addr:         cfg.App.Address(),
		Handler:      engine,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	return &Server{
		runtime:      runtime,
		httpServer:   httpServer,
		consumers:    consumers,
		jobScheduler: jobScheduler,
	}, nil
}

// Run 启动 HTTP 服务并开始监听端口。
func (s *Server) Run() error {
	log.Printf("server starting on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Close 负责优雅关闭服务。
// 关闭顺序很重要：
// 1. 先停止接收新的 HTTP 请求
// 2. 再关闭数据库、缓存、消息队列等底层资源
func (s *Server) Close(ctx context.Context) error {
	log.Println("server shutting down")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	var errs []error
	if s.jobScheduler != nil {
		s.jobScheduler.Close()
	}
	if s.consumers != nil {
		if err := s.consumers.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.runtime.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func schedulerOptionsFromConfig(cfg *bootstrap.Config) (jobs.SchedulerOptions, error) {
	videoStatsInterval, err := cfg.Jobs.VideoStatsReconcileEveryDuration()
	if err != nil {
		return jobs.SchedulerOptions{}, err
	}

	hotFeedInterval, err := cfg.Jobs.HotFeedRebuildEveryDuration()
	if err != nil {
		return jobs.SchedulerOptions{}, err
	}

	hotFeedDirtyRefreshInterval, err := cfg.Jobs.HotFeedDirtyRefreshEveryDuration()
	if err != nil {
		return jobs.SchedulerOptions{}, err
	}

	lockTTL, err := cfg.Jobs.LockTTLDuration()
	if err != nil {
		return jobs.SchedulerOptions{}, err
	}

	return jobs.SchedulerOptions{
		Enabled:                     cfg.Jobs.Enabled,
		RunOnStart:                  cfg.Jobs.RunOnStart,
		VideoStatsReconcileInterval: videoStatsInterval,
		HotFeedDirtyRefreshInterval: hotFeedDirtyRefreshInterval,
		HotFeedRebuildInterval:      hotFeedInterval,
		LockTTL:                     lockTTL,
		BatchSize:                   cfg.Jobs.BatchSize,
		HomeHotMaxEntries:           cfg.Feed.HomeHotMaxEntries,
	}, nil
}
