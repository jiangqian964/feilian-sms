// sms-gateway 是飞连短信事件转发通用短信网关的主进程入口。
// 启动顺序：引导配置 → 数据密钥 → SQLite/迁移 → 配置快照 → 编排服务 →
// 入站 HTTP 装配 → 信号驱动的 5 秒优雅退出。
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"feilian-sms/internal/bootstrap"
	"feilian-sms/internal/channel"
	"feilian-sms/internal/httpapi"
	"feilian-sms/internal/logging"
	"feilian-sms/internal/service"
	"feilian-sms/internal/store"
	"feilian-sms/internal/webui"
)

const (
	// downstreamCeilingMS 是 HTTP 客户端层面的兜底超时上限；
	// 单次下发实际超时由系统设置（默认 2000ms）经 context 热生效控制，
	// 这里只给一个足够大的硬上限，避免客户端 Timeout 反向压缩热配置。
	downstreamCeilingMS = 60000
	shutdownGrace       = 5 * time.Second
	readHeaderTimeout   = 10 * time.Second
)

func main() {
	configPath := flag.String("config", "", "引导配置 YAML 路径（留空则纯环境变量启动）")
	flag.Parse()

	cfg, err := bootstrap.Load(*configPath)
	if err != nil {
		panic("加载引导配置失败: " + err.Error())
	}
	logger, err := logging.New(cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		panic("初始化日志失败: " + err.Error())
	}
	defer func() { _ = logger.Sync() }()

	dataKey, err := bootstrap.ResolveDataKey(cfg)
	if err != nil {
		logger.Fatal("解析数据密钥失败", zap.Error(err))
	}
	st, err := store.Open(cfg.SQLite.Path, dataKey)
	if err != nil {
		logger.Fatal("打开 SQLite 失败", zap.String("path", cfg.SQLite.Path), zap.Error(err))
	}
	defer func() { _ = st.Close() }()

	cache, err := store.NewCache(st)
	if err != nil {
		logger.Fatal("初始化配置快照失败", zap.Error(err))
	}

	settingsRT := service.NewSettingsRuntime(cache)
	sender := channel.NewClient(downstreamCeilingMS)
	forwardSvc := service.NewForwardService(st, cache, sender).WithLogger(logger)
	receiptSvc := service.NewReceiptService(st, cache)

	uiHandler, err := webui.Handler()
	if err != nil {
		logger.Fatal("加载内嵌 WebUI 失败", zap.Error(err))
	}

	srv, err := httpapi.NewServer(httpapi.Deps{
		Settings:   settingsRT,
		Forward:    forwardSvc,
		Receipts:   receiptSvc,
		Store:      st,
		AdminCIDRs: cfg.Server.AdminCIDRs,
		Logger:     logger,
		UI:         uiHandler,
	})
	if err != nil {
		logger.Fatal("HTTP 装配失败", zap.Error(err))
	}

	httpSrv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("sms-gateway 启动",
			zap.String("listen", cfg.Server.Listen),
			zap.Strings("admin_cidrs", cfg.Server.AdminCIDRs))
		if listenErr := httpSrv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			serverErr <- listenErr
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	select {
	case <-sigCtx.Done():
		logger.Info("收到退出信号，开始优雅关闭", zap.Duration("grace", shutdownGrace))
	case listenErr := <-serverErr:
		logger.Fatal("HTTP 服务异常退出", zap.Error(listenErr))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("优雅关闭超时或失败，强制关闭", zap.Error(err))
		_ = httpSrv.Close()
	}
	logger.Info("sms-gateway 已停止")
}
