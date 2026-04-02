# kiro-proxy (Go)

Kiro.py 的 Go 语言重写版，提供 Kiro IDE 凭据管理和 Anthropic API 兼容代理服务。

## 功能

- Anthropic API 兼容代理（流式 / 非流式）
- 多凭据管理与自动轮换
- 负载均衡（优先级模式）
- Token 自动刷新与过期管理
- Admin API（凭据管理、余额查询、启用/禁用）
- 单二进制部署，无运行时依赖

## 快速开始

### 环境要求

- Go 1.23+

### 安装

```bash
git clone <repo-url>
cd go
```

### 配置

```bash
make setup
```

这会从示例文件复制 `config.json` 和 `credentials.json`，然后编辑它们：

**config.json** 关键字段：

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `host` | 监听地址 | `0.0.0.0` |
| `port` | 监听端口 | `8990` |
| `region` | AWS 区域 | `us-east-1` |
| `kiroVersion` | Kiro IDE 版本号 | `0.10.0` |
| `apiKey` | 代理 API Key（客户端调用时使用） | 必填 |
| `adminApiKey` | Admin API 认证密钥 | 可选 |
| `loadBalancingMode` | 负载均衡：`priority` | `priority` |

**credentials.json** 凭据数组：

```json
[
  {
    "refreshToken": "your-refresh-token",
    "authMethod": "social",
    "clientId": "oidc-kiro",
    "clientSecret": "your-client-secret",
    "priority": 0,
    "authRegion": "us-east-1",
    "apiRegion": "us-east-1"
  }
]
```

### 启动

```bash
# 开发模式（推荐，修改代码后重新执行即可）
make dev

# 编译后运行
make start

# 仅编译
make build
```

## Makefile 命令

```
make setup          初始化配置文件（从示例复制）
make dev            开发模式运行（go run，无需预编译）
make build          编译二进制到 build/
make start          编译并运行
make fmt            格式化代码
make vet            静态检查
make test           运行测试
make clean          清理构建产物
make build-all      交叉编译 Linux / Windows / macOS arm64
```

## 命令行参数

```
-c, --config       配置文件路径（默认 ~/.config/kiro-proxy/config.json）
    --credentials  凭据文件路径（默认 credentials.json）
```

示例：

```bash
./build/kiro-proxy --config /etc/kiro/config.json --credentials /etc/kiro/credentials.json
```

## API

### Anthropic 兼容接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/v1/models` | 获取模型列表 |
| POST | `/v1/messages` | 发送消息（流式/非流式） |
| POST | `/v1/messages/count_tokens` | 统计 Token 数量 |
| POST | `/cc/v1/messages` | 缓冲流式（适合不支持 SSE 的客户端） |

请求头需携带 `x-api-key: <apiKey>` 或 `Authorization: Bearer <apiKey>`。

### Admin API

需要配置 `adminApiKey`，请求头携带 `x-admin-key: <adminApiKey>`。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/api/admin/credentials` | 获取凭据列表快照 |
| POST | `/api/admin/credentials/:id/disabled` | 启用/禁用凭据 |
| POST | `/api/admin/credentials/:id/priority` | 设置优先级 |
| POST | `/api/admin/credentials/:id/reset` | 重置失败计数 |
| GET  | `/api/admin/credentials/:id/balance` | 查询余额 |

## 项目结构

```
go/
├── cmd/
│   └── main.go                 # 入口
├── internal/
│   ├── admin/                  # Admin API
│   │   ├── router.go           # 路由注册
│   │   ├── service.go          # 业务逻辑
│   │   ├── types.go            # 类型定义
│   │   ├── middleware.go       # 认证中间件
│   │   └── error.go            # 错误处理
│   ├── anthropic/              # Anthropic API 兼容层
│   │   ├── router.go           # 路由注册
│   │   ├── handlers.go         # 请求处理器
│   │   ├── stream.go           # 流式响应
│   │   ├── converter.go        # 请求转换
│   │   ├── types.go            # 类型定义
│   │   └── middleware.go       # 认证/CORS 中间件
│   ├── kiro/                   # Kiro 核心
│   │   ├── provider/           # API 调用
│   │   ├── tokenmanager/       # 多凭据管理
│   │   ├── model/              # 数据模型
│   │   ├── parser/             # 事件流解析器
│   │   └── machineid/          # Machine ID 生成
│   ├── config/                 # 配置加载
│   ├── httpclient/             # HTTP 客户端工厂
│   ├── tokencount/             # Token 计数
│   ├── args/                   # 命令行参数
│   └── common/                 # 公共工具
├── config.example.json         # 配置示例
├── credentials.example.json    # 凭据示例
├── Makefile
├── go.mod
└── go.sum
```

## License

MIT
