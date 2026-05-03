package router

import (
	"feed-backend/internal/bootstrap"
	"feed-backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// NewEngine 创建 Gin 引擎并注册全局中间件和路由。
func NewEngine(runtime *bootstrap.Runtime) *gin.Engine {
	// 当前阶段先用 ReleaseMode，避免开发时输出过多 Gin 默认日志。
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()

	// 中间件的注册顺序就是执行顺序。
	// 当前顺序是：
	// 1. 请求 ID
	// 2. 跨域
	// 3. panic 恢复
	engine.Use(middleware.RequestID())
	engine.Use(middleware.CORS(runtime.Config.CORS))
	engine.Use(middleware.Recovery())

	// 注册具体路由。
	registerRoutes(engine, runtime)
	return engine
}
