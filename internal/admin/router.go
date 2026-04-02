// Package admin Admin API 路由配置 - 参考 clauldcode-proxy/admin/router.py
package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"kiro-proxy/internal/admin/runtimelog"
	"kiro-proxy/internal/anthropic/messagelog"
	"kiro-proxy/internal/apikeys"
	"kiro-proxy/internal/sysstat"
	"kiro-proxy/internal/tokenusage"
)

// RegisterRoutes 注册所有 Admin API 路由
func RegisterRoutes(mux *http.ServeMux, service *Service, proxyAPIKey, adminAPIKey string) {
	// GET /credentials - 获取所有凭据
	mux.HandleFunc("GET /credentials", func(w http.ResponseWriter, r *http.Request) {
		creds, err := service.GetAllCredentials()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, creds.ToDict())
	})

	// POST /credentials - 添加凭据
	mux.HandleFunc("POST /credentials", func(w http.ResponseWriter, r *http.Request) {
		var req AddCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		resp, err := service.AddCredential(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, resp)
	})

	// GET /credentials-raw - 获取原始凭据
	mux.HandleFunc("GET /credentials-raw", func(w http.ResponseWriter, r *http.Request) {
		content, err := service.GetRawCredentials()
		if err != nil {
			writeError(w, &InternalError{Message: err.Error()})
			return
		}
		writeJSON(w, map[string]string{"content": content})
	})

	// PUT /credentials-raw - 保存原始凭据
	mux.HandleFunc("PUT /credentials-raw", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		if err := service.SaveRawCredentials(body.Content); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "已保存，请重启服务生效"})
	})

	// PUT /credentials/groups - 批量设置凭据分组
	mux.HandleFunc("PUT /credentials/groups", func(w http.ResponseWriter, r *http.Request) {
		// JSON key 只能是 string，需先 decode 为 map[string]string 再转 int
		var rawGroups map[string]string
		if err := json.NewDecoder(r.Body).Decode(&rawGroups); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		groups := make(map[int]string, len(rawGroups))
		for k, v := range rawGroups {
			id, err := strconv.Atoi(k)
			if err != nil {
				continue
			}
			groups[id] = v
		}
		service.SetCredentialGroupsBatch(groups)
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// POST /credentials/reset-all - 重置所有计数器
	mux.HandleFunc("POST /credentials/reset-all", func(w http.ResponseWriter, r *http.Request) {
		service.ResetAllCounters()
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// DELETE /credentials/{id} - 删除凭据
	mux.HandleFunc("DELETE /credentials/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/credentials/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" {
			writeError(w, &BadRequestError{Message: "缺少凭据 ID"})
			return
		}

		id, err := strconv.Atoi(parts[0])
		if err != nil {
			writeError(w, &BadRequestError{Message: "无效的凭据 ID"})
			return
		}

		if err := service.DeleteCredential(id); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// POST /credentials/{id}/disabled - 设置凭据禁用状态
	mux.HandleFunc("POST /credentials/{id}/disabled", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			writeError(w, &BadRequestError{Message: "无效的凭据 ID"})
			return
		}
		
		var req struct {
			Disabled bool `json:"disabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		
		if err := service.SetDisabled(id, req.Disabled); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// POST /credentials/{id}/priority - 设置凭据优先级
	mux.HandleFunc("POST /credentials/{id}/priority", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			writeError(w, &BadRequestError{Message: "无效的凭据 ID"})
			return
		}
		
		var req struct {
			Priority int `json:"priority"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		
		if err := service.SetPriority(id, req.Priority); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// POST /credentials/{id}/reset - 重置失败计数
	mux.HandleFunc("POST /credentials/{id}/reset", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			writeError(w, &BadRequestError{Message: "无效的凭据 ID"})
			return
		}
		
		if err := service.ResetAndEnable(id); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// GET /credentials/{id}/balance - 获取凭据余额
	mux.HandleFunc("GET /credentials/{id}/balance", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			writeError(w, &BadRequestError{Message: "无效的凭据 ID"})
			return
		}
		
		forceRefresh := r.URL.Query().Get("forceRefresh") == "true" || r.URL.Query().Get("forceRefresh") == "1"
		balance, err := service.GetBalance(r.Context(), id, forceRefresh)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, balance)
	})

	// POST /credentials/{id}/group - 设置凭据分组
	mux.HandleFunc("POST /credentials/{id}/group", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			writeError(w, &BadRequestError{Message: "无效的凭据 ID"})
			return
		}
		
		var req struct {
			Group string `json:"group"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		
		if err := service.SetCredentialGroup(id, req.Group); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// GET /stats - 获取统计信息
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		stats := service.GetStats()
		writeJSON(w, stats)
	})

	// GET /version - 获取版本信息
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"current":     "dev",
			"latest":      "dev",
			"hasUpdate":   false,
			"behindCount": 0,
		})
	})

	// GET /log - 获取日志开关状态
	mux.HandleFunc("GET /log", func(w http.ResponseWriter, r *http.Request) {
		ml := messagelog.Get()
		enabled := ml != nil && ml.Enabled()
		writeJSON(w, map[string]interface{}{"enabled": enabled})
	})

	// PUT /log - 设置日志开关
	mux.HandleFunc("PUT /log", func(w http.ResponseWriter, r *http.Request) {
		ml := messagelog.Get()
		if ml == nil {
			writeJSON(w, map[string]interface{}{"success": false, "message": "消息日志模块未初始化"})
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		ml.SetEnabled(body.Enabled)
		msg := "消息日志已关闭"
		if body.Enabled {
			msg = "消息日志已开启"
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": msg})
	})

	// GET /logs/runtime - 获取运行时日志
	mux.HandleFunc("GET /logs/runtime", func(w http.ResponseWriter, r *http.Request) {
		buf := runtimelog.Get()
		if buf == nil {
			writeJSON(w, map[string]interface{}{
				"entries":    []interface{}{},
				"nextCursor": 0,
				"bufferSize": 0,
			})
			return
		}
		limit := 100
		if l := r.URL.Query().Get("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil {
				limit = v
			}
		}
		cursor := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			if v, err := strconv.Atoi(c); err == nil {
				cursor = v
			}
		}
		level := r.URL.Query().Get("level")
		keyword := r.URL.Query().Get("q")
		var result map[string]interface{}
		if cursor > 0 {
			result = buf.Since(cursor, limit, level, keyword)
		} else {
			result = buf.Tail(limit, level, keyword)
		}
		writeJSON(w, result)
	})

	// GET /system/stats - 系统资源监控
	mux.HandleFunc("GET /system/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, sysstat.GetStats())
	})

	// GET /key-usage-stats - 获取 key 用量统计
	mux.HandleFunc("GET /key-usage-stats", func(w http.ResponseWriter, r *http.Request) {
		mgr := apikeys.GetApiKeyManager()
		if mgr == nil {
			writeJSON(w, map[string]interface{}{"keys": []interface{}{}, "groups": map[string]interface{}{}})
			return
		}
		writeJSON(w, map[string]interface{}{
			"keys":   mgr.GetUsageStats(),
			"groups": mgr.GetGroups(),
		})
	})

	// GET /token-usage/hourly - 获取今日小时级用量
	mux.HandleFunc("GET /token-usage/hourly", func(w http.ResponseWriter, r *http.Request) {
		tracker := tokenusage.GetTokenUsageTracker()
		if tracker == nil {
			writeJSON(w, map[string]interface{}{"hourly": map[string]interface{}{}})
			return
		}
		writeJSON(w, map[string]interface{}{"hourly": tracker.GetHourly()})
	})

	// GET /token-usage/history - 获取历史用量
	mux.HandleFunc("GET /token-usage/history", func(w http.ResponseWriter, r *http.Request) {
		tracker := tokenusage.GetTokenUsageTracker()
		if tracker == nil {
			writeJSON(w, map[string]interface{}{"history": map[string]interface{}{}})
			return
		}
		days := 7
		if d := r.URL.Query().Get("days"); d != "" {
			if v, err := strconv.Atoi(d); err == nil {
				days = v
			}
		}
		writeJSON(w, map[string]interface{}{"history": tracker.GetHistory(days)})
	})

	// GET /models - 获取模型列表
	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"models": []interface{}{}})
	})

	// GET /routing - 获取路由配置
	mux.HandleFunc("GET /routing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"freeModels":   service.GetFreeModels(),
			"customModels": service.GetCustomModels(),
		})
	})

	// PUT /routing - 更新路由配置
	mux.HandleFunc("PUT /routing", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			FreeModels   []string `json:"freeModels"`
			CustomModels []string `json:"customModels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		if body.CustomModels != nil {
			service.SetCustomModels(body.CustomModels)
		}
		service.SetFreeModels(body.FreeModels)
		writeJSON(w, map[string]interface{}{"success": true, "message": "路由配置已更新"})
	})

	// GET /keys - 获取 API Key 列表
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		mgr := apikeys.GetApiKeyManager()
		var keys []interface{}
		var groups interface{} = map[string]interface{}{}
		if mgr != nil {
			for _, k := range mgr.GetAllKeys() {
				keys = append(keys, k)
			}
			groups = mgr.GetGroups()
		}
		// 把代理主 Key 作为管理员条目插入列表头部
		masked := proxyAPIKey
		if len(proxyAPIKey) > 12 {
			masked = proxyAPIKey[:7] + "..." + proxyAPIKey[len(proxyAPIKey)-4:]
		}
		adminKey := map[string]interface{}{
			"key":            proxyAPIKey,
			"maskedKey":      masked,
			"name":           "管理员",
			"group":          "admin",
			"rate":           nil,
			"monthlyQuota":   nil,
			"effectiveRate":  0,
			"effectiveQuota": -1,
			"billedTokens":   0,
			"billedMonth":    "",
			"totalRawTokens": 0,
			"requestCount":   0,
			"enabled":        true,
			"createdAt":      "",
			"isAdmin":        true,
		}
		keys = append([]interface{}{adminKey}, keys...)
		writeJSON(w, map[string]interface{}{
			"keys":   keys,
			"groups": groups,
		})
	})

	// POST /keys - 添加 API Key
	mux.HandleFunc("POST /keys", func(w http.ResponseWriter, r *http.Request) {
		mgr := apikeys.GetApiKeyManager()
		if mgr == nil {
			writeJSON(w, map[string]interface{}{"success": false, "message": "Key manager not initialized"})
			return
		}
		var body struct {
			Name         string   `json:"name"`
			Group        string   `json:"group"`
			Rate         *float64 `json:"rate"`
			MonthlyQuota *int     `json:"monthlyQuota"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			writeError(w, &BadRequestError{Message: "name is required"})
			return
		}
		entry := mgr.AddKey(body.Name, body.Group, body.Rate, body.MonthlyQuota)
		writeJSON(w, map[string]interface{}{"success": true, "key": entry})
	})

	// PUT /keys/groups/{name} - 设置 Key 分组
	mux.HandleFunc("PUT /keys/groups/", func(w http.ResponseWriter, r *http.Request) {
		mgr := apikeys.GetApiKeyManager()
		if mgr == nil {
			writeJSON(w, map[string]interface{}{"success": false, "message": "Key manager not initialized"})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/keys/groups/")
		if name == "" {
			writeError(w, &BadRequestError{Message: "缺少分组名"})
			return
		}
		var body struct {
			Rate         float64 `json:"rate"`
			MonthlyQuota int     `json:"monthlyQuota"`
		}
		body.Rate = 1.0
		body.MonthlyQuota = -1
		_ = json.NewDecoder(r.Body).Decode(&body)
		mgr.SetGroup(name, body.Rate, body.MonthlyQuota)
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// DELETE /keys/groups/{name} - 删除 Key 分组
	mux.HandleFunc("DELETE /keys/groups/", func(w http.ResponseWriter, r *http.Request) {
		mgr := apikeys.GetApiKeyManager()
		if mgr == nil {
			writeJSON(w, map[string]interface{}{"success": false, "message": "Key manager not initialized"})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/keys/groups/")
		if !mgr.DeleteGroup(name) {
			writeError(w, &BadRequestError{Message: "Group not found or still in use"})
			return
		}
		writeJSON(w, map[string]interface{}{"success": true})
	})

	// POST /keys/{key}/regenerate - 重新生成 API Key
	// POST /keys/{key}/reset - 重置 Key 用量
	// PUT /keys/{key} - 更新 API Key
	// DELETE /keys/{key} - 删除 API Key
	mux.HandleFunc("/keys/", func(w http.ResponseWriter, r *http.Request) {
		mgr := apikeys.GetApiKeyManager()
		if mgr == nil {
			writeJSON(w, map[string]interface{}{"success": false, "message": "Key manager not initialized"})
			return
		}
		// 解析路径: /keys/{key}[/regenerate|/reset]
		path := strings.TrimPrefix(r.URL.Path, "/keys/")
		parts := strings.SplitN(path, "/", 2)
		keyStr := parts[0]
		action := ""
		if len(parts) == 2 {
			action = parts[1]
		}
		switch {
		case r.Method == http.MethodPost && action == "regenerate":
			result := mgr.RegenerateKey(keyStr)
			if result == nil {
				writeError(w, &NotFoundError{ID: 0})
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "key": result})
		case r.Method == http.MethodPost && action == "reset":
			if !mgr.ResetUsage(keyStr) {
				writeError(w, &NotFoundError{ID: 0})
				return
			}
			writeJSON(w, map[string]interface{}{"success": true})
		case r.Method == http.MethodPut && action == "":
			var fields map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				writeError(w, &BadRequestError{Message: "请求体解析失败"})
				return
			}
			result := mgr.UpdateKey(keyStr, fields)
			if result == nil {
				writeError(w, &NotFoundError{ID: 0})
				return
			}
			writeJSON(w, map[string]interface{}{"success": true, "key": result})
		case r.Method == http.MethodDelete && action == "":
			if !mgr.DeleteKey(keyStr) {
				writeError(w, &NotFoundError{ID: 0})
				return
			}
			writeJSON(w, map[string]interface{}{"success": true})
		default:
			writeError(w, &BadRequestError{Message: "不支持的操作"})
		}
	})

	// GET /git/log - 获取远程 commit 列表
	mux.HandleFunc("GET /git/log", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"currentHash": "",
			"commits":     []interface{}{},
		})
	})

	// POST /restart - 重启服务
	mux.HandleFunc("POST /restart", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"success": true, "message": "重启功能暂未实现"})
	})

	// POST /update - 更新并重启
	mux.HandleFunc("POST /update", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"success": true, "message": "更新功能暂未实现"})
	})

	// GET /claude/settings - 读取 Claude Code settings.json
	mux.HandleFunc("GET /claude/settings", func(w http.ResponseWriter, r *http.Request) {
		import_os_home := os.UserHomeDir
		homeDir, err := import_os_home()
		settingsPath := ""
		if err == nil {
			settingsPath = homeDir + "/.claude/settings.json"
		}
		data, readErr := os.ReadFile(settingsPath)
		if readErr != nil {
			writeJSON(w, map[string]interface{}{"settings": map[string]interface{}{}, "path": settingsPath, "exists": false})
			return
		}
		var settings interface{}
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
			writeJSON(w, map[string]interface{}{"settings": map[string]interface{}{}, "path": settingsPath, "exists": true})
			return
		}
		writeJSON(w, map[string]interface{}{"settings": settings, "path": settingsPath, "exists": true})
	})

	// PUT /claude/settings - 写入 Claude Code settings.json
	mux.HandleFunc("PUT /claude/settings", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Settings json.RawMessage `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		homeDir, err := os.UserHomeDir()
		if err != nil {
			writeError(w, &InternalError{Message: "无法获取用户目录"})
			return
		}
		settingsPath := homeDir + "/.claude/settings.json"
		if err := os.MkdirAll(homeDir+"/.claude", 0755); err != nil {
			writeError(w, &InternalError{Message: "创建目录失败"})
			return
		}
		if err := os.WriteFile(settingsPath, body.Settings, 0644); err != nil {
			writeError(w, &InternalError{Message: "写入失败: " + err.Error()})
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "已保存"})
	})

	// GET /claude/profiles - 列出配置文件
	mux.HandleFunc("GET /claude/profiles", func(w http.ResponseWriter, r *http.Request) {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			writeJSON(w, map[string]interface{}{"profiles": []interface{}{}})
			return
		}
		claudeDir := homeDir + "/.claude"
		entries, err := os.ReadDir(claudeDir)
		if err != nil {
			writeJSON(w, map[string]interface{}{"profiles": []interface{}{}})
			return
		}
		profiles := []map[string]interface{}{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if len(name) < 5 || name[len(name)-5:] != ".json" {
				continue
			}
			lower := strings.ToLower(name)
			if !strings.Contains(lower, "settings") {
				continue
			}
			filePath := claudeDir + "/" + name
			data, err := os.ReadFile(filePath)
			baseURL, model := "", ""
			if err == nil {
				var parsed map[string]interface{}
				if json.Unmarshal(data, &parsed) == nil {
					if env, ok := parsed["env"].(map[string]interface{}); ok {
						if v, ok := env["ANTHROPIC_BASE_URL"].(string); ok {
							baseURL = v
						}
					}
					if v, ok := parsed["model"].(string); ok {
						model = v
					}
				}
			}
			profiles = append(profiles, map[string]interface{}{
				"filename": name,
				"path":     filePath,
				"baseUrl":  baseURL,
				"model":    model,
				"isActive": name == "settings.json",
			})
		}
		writeJSON(w, map[string]interface{}{"profiles": profiles})
	})

	// POST /claude/profiles/switch - 切换配置文件
	mux.HandleFunc("POST /claude/profiles/switch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Filename string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, &BadRequestError{Message: "请求体解析失败"})
			return
		}
		if body.Filename == "" || body.Filename == "settings.json" {
			writeError(w, &BadRequestError{Message: "无效的目标文件"})
			return
		}
		homeDir, err := os.UserHomeDir()
		if err != nil {
			writeError(w, &InternalError{Message: "无法获取用户目录"})
			return
		}
		claudeDir := homeDir + "/.claude"
		targetPath := claudeDir + "/" + body.Filename
		activePath := claudeDir + "/settings.json"
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			writeError(w, &NotFoundError{ID: 0})
			return
		}
		targetData, err := os.ReadFile(targetPath)
		if err != nil {
			writeError(w, &InternalError{Message: "读取目标文件失败"})
			return
		}
		if activeData, err := os.ReadFile(activePath); err == nil {
			_ = os.WriteFile(claudeDir+"/settings-prev.json", activeData, 0644)
		}
		if err := os.WriteFile(activePath, targetData, 0644); err != nil {
			writeError(w, &InternalError{Message: "切换失败: " + err.Error()})
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "message": "已切换到 " + body.Filename})
	})

	// GET /claude/sessions - 读取 Claude Code 会话列表
	mux.HandleFunc("GET /claude/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"sessions": []interface{}{}})
	})

	// GET /update/status - 获取更新状态
	mux.HandleFunc("GET /update/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"log": []interface{}{}})
	})

	// GET /git/status - 获取 git 状态
	mux.HandleFunc("GET /git/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"hasLocalChanges": false,
			"changedFiles":    []interface{}{},
		})
	})
}

// writeJSON 写入 JSON 响应
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// writeError 写入错误响应
func writeError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	message := err.Error()

	switch err.(type) {
	case *NotFoundError:
		code = http.StatusNotFound
	case *BadRequestError:
		code = http.StatusBadRequest
	case *UpstreamError:
		code = http.StatusBadGateway
	case *InvalidCredentialError:
		code = http.StatusBadRequest
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}
