// 主入口 - 参考 clauldcode-proxy/main.py
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"kiro-proxy/internal/admin"
	"kiro-proxy/internal/admin/runtimelog"
	"kiro-proxy/internal/anthropic"
	"kiro-proxy/internal/anthropic/messagelog"
	"kiro-proxy/internal/apikeys"
	"kiro-proxy/internal/args"
	"kiro-proxy/internal/config"
	"kiro-proxy/internal/httpclient"
	"kiro-proxy/internal/kiro/model"
	"kiro-proxy/internal/kiro/provider"
	"kiro-proxy/internal/kiro/tokenmanager"
	"kiro-proxy/internal/logger"
	"kiro-proxy/internal/tokenusage"
)

func main() {
	// 初始化日志器（可执行文件同目录的 logs/ 目录）
	logsDir := "logs"
	if execPath, err := os.Executable(); err == nil {
		logsDir = filepath.Join(filepath.Dir(execPath), "logs")
	}
	logger.Init(logsDir)
	defer logger.Shutdown()

	// 初始化运行时日志内存缓冲区，并注册为 logger 订阅者
	rtBuf := runtimelog.Init(5000)
	logger.AddSubscriber(rtBuf)

	// 解析命令行参数
	parsedArgs := args.Parse()
	configPath := parsedArgs.ConfigPath
	if configPath == "" {
		configPath = config.DefaultConfigPath()
	}

	// 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatalf("加载配置失败：%v", err)
	}

	// 加载凭证
	credentialsPath := parsedArgs.CredentialsPath
	if credentialsPath == "" {
		credentialsPath = "config/credentials.json"
	}

	credConfig := model.CredentialsConfig{}
	credentialsList, isMultipleFormat, err := credConfig.Load(credentialsPath)
	if err != nil {
		logger.Fatalf("加载凭证失败：%v", err)
	}

	// 按优先级排序
	sort.Slice(credentialsList, func(i, j int) bool {
		return credentialsList[i].Priority < credentialsList[j].Priority
	})
	logger.Infof("已加载 %d 个凭据配置", len(credentialsList))

	var firstCredentials model.KiroCredentials
	if len(credentialsList) > 0 {
		firstCredentials = credentialsList[0]
	} else {
		firstCredentials = model.KiroCredentials{}
	}

	// 获取 API Key
	apiKey := cfg.ApiKey
	if apiKey == nil || *apiKey == "" {
		logger.Fatal("配置文件中未设置 apiKey")
	}

	// 构建代理配置
	var proxyConfig *httpclient.ProxyConfig
	if cfg.ProxyUrl != nil && *cfg.ProxyUrl != "" {
		proxyConfig = &httpclient.ProxyConfig{URL: *cfg.ProxyUrl}
		if cfg.ProxyUsername != nil && cfg.ProxyPassword != nil &&
			*cfg.ProxyUsername != "" && *cfg.ProxyPassword != "" {
			proxyConfig.WithAuth(*cfg.ProxyUsername, *cfg.ProxyPassword)
		}
		logger.Infof("已配置 HTTP 代理：%s", *cfg.ProxyUrl)
	}

	// 创建 TokenManager 和 KiroProvider
	tokenManager, err := tokenmanager.NewMultiTokenManager(
		cfg,
		credentialsList,
		proxyConfig,
		credentialsPath,
		isMultipleFormat,
	)
	if err != nil {
		logger.Fatalf("创建 Token 管理器失败：%v", err)
	}

	kiroProvider := provider.NewKiroProvider(tokenManager, proxyConfig)

	// 初始化 Token 计数器配置
	// TODO: 实现 token_counter 初始化

	// 配置请求限制
	anthropic.ConfigureRequestLimits(
		cfg.RequestMaxBytes,
		cfg.RequestMaxChars,
		cfg.RequestContextTokenLimit,
	)

	// 配置流限制
	anthropic.ConfigureStreamLimits(
		cfg.StreamPingIntervalSecs,
		cfg.StreamMaxIdlePings,
		cfg.StreamIdleWarnAfterPings,
	)

	// 配置转换器限制
	anthropic.ConfigureConverterLimits(
		cfg.ToolResultCurrentMaxChars,
		cfg.ToolResultCurrentMaxLines,
		cfg.ToolResultHistoryMaxChars,
		cfg.ToolResultHistoryMaxLines,
	)

	// 初始化 token 用量追踪
	tokenusage.InitTokenUsageTracker(tokenManager.CacheDir())

	// 初始化消息日志（保存到可执行文件同目录的 logs/ 目录）
	msgLogDir := logsDir
	messagelog.Init(msgLogDir)

	// 初始化多 API Key 管理
	// 使用可执行文件所在目录作为数据目录
	execDir := ""
	if execPath, err := os.Executable(); err == nil {
		execDir = filepath.Join(filepath.Dir(execPath), "config")
	}
	apikeys.InitApiKeyManager(execDir)

	// 创建 HTTP ServeMux
	mux := http.NewServeMux()

	// 挂载 Admin API（如果配置了非空的 admin_api_key）
	adminKey := cfg.AdminApiKey
	adminKeyValid := adminKey != nil && strings.TrimSpace(*adminKey) != ""
	var adminService *admin.Service

	if adminKeyValid {
		adminService = admin.NewService(tokenManager)
		adminService.StartAutoBalanceRefresh()

		// 创建 Admin 路由并挂载到主 mux
		adminMux := http.NewServeMux()
		admin.RegisterRoutes(adminMux, adminService, *apiKey, *adminKey)
		adminHandler := admin.NewAuthMiddleware(adminMux, *adminKey)
		mux.Handle("/api/admin/", http.StripPrefix("/api/admin", adminHandler))

		// 挂载 Admin UI 静态文件（无需认证）
		admin.RegisterUIHandler(mux)

		logger.Println("Admin API 已启用")
	}

	// 挂载 Anthropic API 路由
	var profileArnStr string
	if firstCredentials.ProfileArn != nil {
		profileArnStr = *firstCredentials.ProfileArn
	}
	
	anthropicState := &anthropic.AppState{
		ApiKey:       *apiKey,
		KiroProvider: kiroProvider,
		ProfileArn:   profileArnStr,
	}
	anthropic.RegisterRoutes(mux, anthropicState)

	// 添加 Auth 中间件
	handler := anthropic.NewAuthMiddleware(mux, anthropicState)

	// 添加 CORS 中间件
	handler = anthropic.NewCORSMiddleware(handler)

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	logger.Infof("启动 Anthropic API 端点：%s", addr)

	half := len(*apiKey) / 2
	logger.Infof("API Key: %s***", (*apiKey)[:half])
	logger.Println("可用 API:")
	logger.Println("  GET  /v1/models")
	logger.Println("  POST /v1/messages")
	logger.Println("  POST /v1/messages/count_tokens")

	if adminKeyValid {
		logger.Println("Admin API:")
		logger.Println("  GET  /api/admin/credentials")
		logger.Println("  POST /api/admin/credentials/:index/disabled")
		logger.Println("  POST /api/admin/credentials/:index/priority")
		logger.Println("  POST /api/admin/credentials/:index/reset")
		logger.Println("  GET  /api/admin/credentials/:index/balance")
		logger.Println("Admin UI:")
		logger.Println("  GET  /admin")
	}

	// 创建 HTTP 服务器
	// WriteTimeout 设为 0（不限制），由上游请求超时（720s）控制流式响应时长
	// 若设置 WriteTimeout < 上游超时，流式响应会被服务端提前断开
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// 优雅关闭
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		logger.Println("正在关闭服务器...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Errorf("服务器关闭失败：%v", err)
		}

		// 停止余额自动刷新
		if adminService != nil {
			adminService.StopAutoBalanceRefresh()
		}

		// 刷新 token 用量
		if tracker := tokenusage.GetTokenUsageTracker(); tracker != nil {
			tracker.Flush()
		}
		if mgr := apikeys.GetApiKeyManager(); mgr != nil {
			mgr.Flush()
		}

		logger.Println("服务器已关闭")
	}()

	logger.Infof("服务器运行在 %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Fatalf("服务器启动失败：%v", err)
	}
}
