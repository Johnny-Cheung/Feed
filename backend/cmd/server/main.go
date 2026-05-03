package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"feed-backend/internal/app"
)

// main 是程序入口。
// 这里主要做四件事：
// 1. 构建应用级 Server
// 2. 启动 HTTP 服务
// 3. 监听退出信号（Ctrl+C、容器停止等）
// 4. 收到退出信号后优雅关闭服务
func main() {
	// NewServer 会完成配置加载、依赖初始化、路由注册等启动前准备。
	srv, err := app.NewServer()
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	// 用一个带缓冲的 channel 接收服务启动后的异常退出。
	// 带缓冲 1 的好处是：即使主 goroutine 还没来得及接收，
	// 子 goroutine 也能先把错误写进去，不会被阻塞。
	serverErrCh := make(chan error, 1)

	// HTTP 服务放到独立 goroutine 中运行，
	// 这样主 goroutine 才能继续监听系统信号。
	go func() {
		// ListenAndServe 正常关闭时会返回 http.ErrServerClosed，
		// 这不算真正的异常，所以要排除掉。
		if runErr := srv.Run(); runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
			serverErrCh <- runErr
		}
	}()

	// signal.NotifyContext 会在收到指定系统信号时自动取消 ctx。
	// 这里监听 SIGINT 和 SIGTERM：
	// - SIGINT：通常来自 Ctrl+C
	// - SIGTERM：通常来自容器停止或进程管理器停止
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// select 同时等待两类事件：
	// 1. 服务异常退出
	// 2. 收到系统退出信号
	select {
	case runErr := <-serverErrCh:
		log.Fatalf("server exited: %v", runErr)
	case <-ctx.Done():
		log.Println("shutdown signal received")
	}

	// 创建一个带超时的关闭上下文。
	// 这样即使某些连接迟迟不结束，也不会无限等待。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Close 内部会先优雅关闭 HTTP 服务，再关闭数据库、Redis、RabbitMQ 等资源。
	if err := srv.Close(shutdownCtx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}
