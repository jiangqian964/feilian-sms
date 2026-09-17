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
	// shutdownGrace 是整段优雅关停总预算：先停接入层等待在途请求，再停
	// 后台补发 worker；systemd 单元的 TimeoutStopSec 必须大于此值。
	shutdownGrace = 10 * time.Second
	// httpShutdownTimeout 是接入层等待在途请求完成的预算（webhook 单条下游
	// 超时 2s，该预算足以排空批量在途请求）。
	httpShutdownTimeout = 6 * time.Second
	readHeaderTimeout   = 10 * time.Second
	// readTimeout 覆盖完整请求读取（含 body，事件体上限 1MiB）；
	// idleTimeout 限制 keep-alive 空连占用，缓解慢连接资源堆积（#8）。
	readTimeout = 30 * time.Second
	idleTimeout = 120 * time.Second
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
	// 热加载失败时不得静默沿用旧快照：落 Error 日志便于巡检发现（#16）。
	cache.SetReloadErrorHook(func(reloadErr error) {
		logger.Error("配置快照热加载失败，继续沿用上一版本快照", zap.Error(reloadErr))
	})

	settingsRT := service.NewSettingsRuntime(cache)
	sender := channel.NewClient(downstreamCeilingMS)
	forwardSvc := service.NewForwardService(st, cache, sender).WithLogger(logger)
	// 后台补发 worker：扫描 stale pending 并重放，随优雅关停一起停止（#2/#11）。
	forwardSvc.StartResendWorker(service.DefaultResendInterval)
	receiptSvc := service.NewReceiptService(st, cache)

	uiHandler, err := webui.Handler()
	if err != nil {
		logger.Fatal("加载内嵌 WebUI 失败", zap.Error(err))
	}

	srv, err := httpapi.NewServer(httpapi.Deps{
		Settings: settingsRT,
		Forward:  forwardSvc,
		Receipts: receiptSvc,
		Store:    st,
		Logger:   logger,
		UI:       uiHandler,
	})
	if err != nil {
		logger.Fatal("HTTP 装配失败", zap.Error(err))
	}

	httpSrv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		// 不设置 WriteTimeout：webhook 需按下游超时预算同步等待批量下发，
		// 单次下发另有 context 级超时；读侧与空连超时足以缓解慢速连接占用（#8）。
		ReadTimeout: readTimeout,
		IdleTimeout: idleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("sms-gateway 启动",
			zap.String("listen", cfg.Server.Listen))
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

	// 关停顺序（#2/#11）：先停接入层，不再接收新请求并等待在途事件处理完成
	// （事件处理在请求内同步完成下发与回写）；再取消后台上下文、等待补发 worker
	// 退出；SQLite 由 defer 最后关闭，杜绝 worker/回写落在已关闭的连接上。
	httpCtx, httpCancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	if err := httpSrv.Shutdown(httpCtx); err != nil {
		logger.Error("接入层优雅关闭超时或失败，强制关闭", zap.Error(err))
		_ = httpSrv.Close()
	}
	httpCancel()

	fwdCtx, fwdCancel := context.WithTimeout(context.Background(), shutdownGrace-httpShutdownTimeout)
	if err := forwardSvc.Shutdown(fwdCtx); err != nil {
		logger.Error("后台补发服务停止超时，放弃等待（未确认记录保留下次补发）", zap.Error(err))
	}
	fwdCancel()
	logger.Info("sms-gateway 已停止")
}
