package router

import (
	"context"
	"log"
	"net/http"
	"time"

	"feed-backend/internal/bootstrap"
	appErrors "feed-backend/internal/common/errors"
	"feed-backend/internal/common/response"
	authDomain "feed-backend/internal/domain/auth"
	feedDomain "feed-backend/internal/domain/feed"
	interactionDomain "feed-backend/internal/domain/interaction"
	userDomain "feed-backend/internal/domain/user"
	videoDomain "feed-backend/internal/domain/video"
	"feed-backend/internal/infra/feedcache"
	"feed-backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerRoutes 集中注册当前阶段所有 HTTP 路由。
// 后面业务接口多起来后，可以继续拆成多个子函数。
func registerRoutes(engine *gin.Engine, runtime *bootstrap.Runtime) {
	// 先挂静态资源路由。
	// 这样后续只要某个文件被保存到了 storage 目录下，
	// 外部就能通过 /static/... 的 URL 访问它。
	registerStaticRoutes(engine, runtime)

	// /health 用来检查服务和核心依赖是否都正常。
	engine.GET("/health", healthHandler(runtime))

	// /ping 是最简单的连通性测试接口。
	// 它不做依赖检查，只验证 HTTP 服务本身是否可达。
	engine.GET("/ping", func(c *gin.Context) {
		response.Success(c, gin.H{"message": "pong"})
	})

	registerAPIRoutes(engine, runtime)
}

// registerStaticRoutes 批量注册阶段五需要的静态资源目录。
// 例如：
// /static/videos/*filepath  -> storage/videos
// /static/covers/*filepath  -> storage/covers
// /static/avatars/*filepath -> storage/avatars
func registerStaticRoutes(engine *gin.Engine, runtime *bootstrap.Runtime) {
	if runtime.Storage == nil {
		return
	}

	for _, mount := range runtime.Storage.StaticMounts() {
		// gin.Dir 的第二个参数传 false，表示不允许列目录。
		// 这样访问目录本身时不会直接把文件列表暴露出去。
		engine.StaticFS(mount.URLPrefix, gin.Dir(mount.Directory, false))
	}
}

// registerAPIRoutes 注册带版本号的业务接口。
func registerAPIRoutes(engine *gin.Engine, runtime *bootstrap.Runtime) {
	apiV1 := engine.Group("/api/v1")
	cache := feedcache.NewCache(runtime.DB, runtime.Redis)

	// 认证领域会同时用到数据库和 JWT 配置，
	// 所以这里在路由层把依赖组装好再交给 Handler。
	authRepo := authDomain.NewRepository(runtime.DB)
	tokenManager := authDomain.NewTokenManager(runtime.Config.JWT.Secret, runtime.Config.JWT.ExpireHours)
	authService := authDomain.NewService(authRepo, tokenManager, cache, runtime.Config.App.StaticBaseURL)
	authHandler := authDomain.NewHandler(authService)

	authGroup := apiV1.Group("/auth")
	authGroup.POST("/register", authHandler.Register)
	authGroup.POST("/login", authHandler.Login)
	authGroup.GET("/me", middleware.Auth(tokenManager), authHandler.Me)

	// 第七、八阶段：Feed 模块。
	// - 第七阶段：首页热榜流
	// - 第八阶段：关注流
	// 可以把首页接口理解成一条“聚合读取链路”：
	// 1. 先从 Redis 热榜拿一页候选视频 ID
	// 2. 再去 MySQL 批量补齐视频、作者、统计
	// 3. 如果用户已登录，再补齐 viewer_state
	//
	// 和第六阶段“发布视频”不同，这里不是写链路，而是读链路。
	// 读代码时建议按这个顺序看：
	// routes -> handler -> service -> repository
	feedRepo := feedDomain.NewRepository(runtime.DB, runtime.Redis, runtime.Config.Feed.HomeHotMaxEntries)
	feedService := feedDomain.NewService(
		feedRepo,
		cache,
		runtime.Config.Pagination.DefaultLimit,
		runtime.Config.Pagination.MaxLimit,
		runtime.Config.App.StaticBaseURL,
	)
	feedHandler := feedDomain.NewHandler(feedService)

	feedGroup := apiV1.Group("/feed")
	feedGroup.GET("/home", middleware.OptionalAuth(tokenManager), feedHandler.Home)
	// 关注流和首页流的最大区别有两个：
	// 1. 它必须登录，所以这里挂的是 Auth，而不是 OptionalAuth
	// 2. 它不看 Redis 热榜，而是直接查“我关注的人发布了什么视频”
	//
	// 初学时可以把它理解成“一个按时间倒序的订阅列表”。
	feedGroup.GET("/following", middleware.Auth(tokenManager), feedHandler.Following)

	videoRepo := videoDomain.NewRepository(runtime.DB)
	videoPublisher := videoDomain.NewPublisher(runtime.RabbitChannel, runtime.Config.RabbitMQ.Exchange)
	videoService := videoDomain.NewService(videoRepo, runtime.Storage, videoPublisher, cache, runtime.Config.App.StaticBaseURL)
	videoHandler := videoDomain.NewHandler(videoService)
	// 这一段是“第六阶段视频模块”的装配入口。
	// 可以把它理解成：
	// 1. Repository 负责碰数据库
	// 2. Publisher 负责发 MQ 事件
	// 3. Service 把“存文件 + 写库 + 发事件”串起来
	// 4. Handler 只处理 HTTP 细节，然后把真正业务交给 Service
	//
	// 初学时建议顺着这个依赖方向去读：
	// routes -> handler -> service -> repository / publisher
	// 这样更容易理解一次“发布视频请求”在后端内部是怎么流动的。

	videoGroup := apiV1.Group("/videos")
	videoGroup.POST("", middleware.Auth(tokenManager), videoHandler.Publish)
	videoGroup.GET("/:video_id", middleware.OptionalAuth(tokenManager), videoHandler.Detail)
	videoGroup.PUT("/:video_id", middleware.Auth(tokenManager), videoHandler.Update)
	videoGroup.DELETE("/:video_id", middleware.Auth(tokenManager), videoHandler.Delete)

	// 第九阶段：互动模块。
	// 这里统一处理：
	// - 点赞 / 取消点赞
	// - 收藏 / 取消收藏
	// - 关注 / 取消关注
	// - 评论创建 / 删除 / 列表
	interactionRepo := interactionDomain.NewRepository(runtime.DB)
	interactionPublisher := interactionDomain.NewPublisher(runtime.RabbitChannel, runtime.RabbitConfirmChannel, runtime.Config.RabbitMQ.Exchange)
	hotCommentCacheTTL, _ := runtime.Config.Feed.HotCommentCacheTTLDuration()
	interactionService := interactionDomain.NewService(
		interactionRepo,
		interactionPublisher,
		cache,
		runtime.Config.App.StaticBaseURL,
		runtime.Config.Feed.VideoRelationStreamMaxEntries,
		runtime.Config.Feed.HotCommentCacheEntries,
		hotCommentCacheTTL,
	)
	interactionHandler := interactionDomain.NewHandler(interactionService)

	videoGroup.POST("/:video_id/likes", middleware.Auth(tokenManager), interactionHandler.LikeVideo)
	videoGroup.DELETE("/:video_id/likes", middleware.Auth(tokenManager), interactionHandler.UnlikeVideo)
	videoGroup.POST("/:video_id/favorites", middleware.Auth(tokenManager), interactionHandler.FavoriteVideo)
	videoGroup.DELETE("/:video_id/favorites", middleware.Auth(tokenManager), interactionHandler.UnfavoriteVideo)
	videoGroup.GET("/:video_id/comments", middleware.OptionalAuth(tokenManager), interactionHandler.ListComments)
	videoGroup.POST("/:video_id/comments", middleware.Auth(tokenManager), interactionHandler.CreateComment)

	commentsGroup := apiV1.Group("/comments")
	commentsGroup.DELETE("/:comment_id", middleware.Auth(tokenManager), interactionHandler.DeleteComment)

	// 第十阶段：我的模块和作者主页。
	// GET /users/me... 和 GET /users/:user_id... 在 Gin 中不能直接以静态/参数路由混用，
	// 所以 GET 系列统一进入 DispatchGET，再在 Handler 内按路径分发。
	userRepo := userDomain.NewRepository(runtime.DB)
	userService := userDomain.NewService(
		userRepo,
		runtime.Storage,
		cache,
		runtime.Config.App.StaticBaseURL,
		runtime.Config.Pagination.DefaultLimit,
		runtime.Config.Pagination.MaxLimit,
	)
	userHandler := userDomain.NewHandler(userService)

	usersGroup := apiV1.Group("/users")
	usersGroup.GET("/*path", middleware.OptionalAuth(tokenManager), userHandler.DispatchGET)
	usersGroup.PUT("/me/profile", middleware.Auth(tokenManager), userHandler.UpdateProfile)
	usersGroup.PUT("/me/avatar", middleware.Auth(tokenManager), userHandler.UpdateAvatar)
	usersGroup.PUT("/me/password", middleware.Auth(tokenManager), userHandler.UpdatePassword)
	usersGroup.POST("/:user_id/follow", middleware.Auth(tokenManager), interactionHandler.FollowUser)
	usersGroup.DELETE("/:user_id/follow", middleware.Auth(tokenManager), interactionHandler.UnfollowUser)
}

// healthHandler 返回一个闭包函数，
// 这样它就能在处理请求时使用 runtime 中保存的依赖对象。
func healthHandler(runtime *bootstrap.Runtime) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 健康检查本身也要设置超时，避免某个依赖卡住后一直不返回。
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		// 先假设所有组件都正常。
		// 如果某一步检查失败，再把对应组件状态改成 down。
		status := gin.H{
			"app":       "ok",
			"mysql":     "ok",
			"redis":     "ok",
			"rabbitmq":  "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}

		// 检查 MySQL。
		if err := pingMySQL(ctx, runtime); err != nil {
			log.Printf("health mysql error: %v", err)
			status["mysql"] = "down"
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"code":    appErrors.CodeServiceUnusable,
				"message": "mysql unavailable",
				"data":    status,
			})
			return
		}

		// 检查 Redis。
		if err := runtime.Redis.Ping(ctx).Err(); err != nil {
			log.Printf("health redis error: %v", err)
			status["redis"] = "down"
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"code":    appErrors.CodeServiceUnusable,
				"message": "redis unavailable",
				"data":    status,
			})
			return
		}

		// RabbitMQ 这里先做最基础的连接状态检查。
		// 后面如果需要更细致，也可以补 channel 可用性或声明操作测试。
		if runtime.RabbitMQ == nil || runtime.RabbitMQ.IsClosed() {
			status["rabbitmq"] = "down"
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"code":    appErrors.CodeServiceUnusable,
				"message": "rabbitmq unavailable",
				"data":    status,
			})
			return
		}

		// 所有检查都通过，返回统一成功响应。
		response.Success(c, status)
	}
}

// pingMySQL 从 Gorm 中取出底层 sql.DB，然后执行 ping。
// 之所以单独写成函数，是为了让 healthHandler 更清晰。
func pingMySQL(ctx context.Context, runtime *bootstrap.Runtime) error {
	sqlDB, err := runtime.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
