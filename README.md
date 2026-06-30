# TShock Web Panel

基于 Web 的 Terraria TShock 服务器管理面板，通过 REST API + WebSocket 实现远程管理，纯鼠标驱动，移动端适配。

## 功能概览

- **仪表盘** — 服务器状态、游戏时间、天象、入侵事件一览
- **玩家管理** — 在线列表、踢出/封禁/禁言/治疗/击杀、批量操作
- **玩家轨迹** — 实时追踪移动轨迹，Canvas 可视化（缩放/平移/传送线过滤/其他玩家标注）
- **传送** — 📍拉/📍去，通过 TeleportRestPlugin 绕过 TShock AllowServer 限制
- **装备预设** — 可视化表格编辑器，词缀类型过滤+中文名+效果提示，一键应用
- **物品发放** — 5400+ 物品数据库，搜索/ID 输入，给玩家发放物品
- **世界管理** — 列表/创建/切换/删除/重命名世界，备份/恢复（版本管理）
- **用户/分组** — 创建/编辑/删除用户账号和分组
- **事件控制** — 血月/日食/满月/沙尘暴/入侵/Boss/怪物
- **实时日志** — WebSocket 双向通信，日志流解析玩家上下线事件
- **亮色/暗色主题** — 完整双主题，移动端 100dvh 适配

## 系统架构

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Web 浏览器  │────▶│  Panel 后端  │────▶│ TShock Server│
│  (前端 SPA)  │◀────│  (Go + WS)  │◀────│  (REST API)  │
└─────────────┘     └─────────────┘     └─────────────┘
       │                   │
       │  WebSocket        ▼
       └────────── Docker Socket
                  (容器管理)
```

- **前端**：原生 HTML/CSS/JS 单页面应用（~2900 行），无框架依赖
- **后端**：Go（~2700 行），gorilla/websocket，RESTful API 代理
- **通信**：REST API + WebSocket 双向（日志流/玩家事件/轨迹/传送/状态同步）
- **服务器**：TShock 6.1.0.0（Terraria 1.4.5.6）via Docker
- **插件**：TeleportRestPlugin（C#），注册 `/tprest` 命令绕过 AllowServer 限制

## 前置要求

- Linux（Ubuntu 22.04/24.04 推荐）
- Docker 20.10+（含 Docker Compose）
- Go 1.21+（编译面板后端）
- .NET SDK 8.0+（编译 TShock 插件，需包含 Roslyn csc.dll）
- 2GB+ 内存

## 一键部署

```bash
chmod +x setup.sh
./setup.sh
```

脚本自动完成：安装依赖 → 创建目录 → 启动 TShock → 提取 DLL → 编译插件 → 编译面板 → 部署 → 验证。

## 手动部署

### 1. 配置 TShock REST Token

编辑 `tshock/config.json`，添加 Application REST Token：

```json
"ApplicationRestTokens": {
  "your-rest-token-here": {
    "Username": "rest_api",
    "UserGroupName": "superadmin"
  }
}
```

### 2. 编译面板

```bash
cd panel
CGO_ENABLED=0 go build -o panel main.go
```

> Dockerfile 只 `COPY panel .`，不会从源码编译。修改代码后必须先 `go build`。

### 3. 编译插件（可选，已有预编译 DLL 可跳过）

```bash
# 准备依赖 DLL（从 TShock 容器提取）
mkdir -p /tmp/net9rt
docker cp terraria-server:/usr/share/dotnet/shared/Microsoft.NETCore.App/9.0.4/. /tmp/net9rt/
mkdir -p /tmp/tpplugin_build
docker cp terraria-server:/server/TerrariaServer.dll /tmp/tpplugin_build/
docker cp terraria-server:/server/OTAPI.dll /tmp/tpplugin_build/
docker cp terraria-server:/server/OTAPI.Runtime.dll /tmp/tpplugin_build/
docker cp terraria-server:/server/ServerPlugins/TShockAPI.dll /tmp/tpplugin_build/

# 编译（必须用 csc，不能用 dotnet build）
CSC=$(find /usr/lib/dotnet/sdk -name "csc.dll" -path "*/Roslyn/*" | head -1)
REFS=""
for dll in /tmp/net9rt/*.dll; do REFS="$REFS -reference:$dll"; done
REFS="$REFS -reference:/tmp/tpplugin_build/TerrariaServer.dll"
REFS="$REFS -reference:/tmp/tpplugin_build/OTAPI.dll"
REFS="$REFS -reference:/tmp/tpplugin_build/OTAPI.Runtime.dll"
REFS="$REFS -reference:/tmp/tpplugin_build/TShockAPI.dll"

dotnet $CSC -target:library -nostdlib+ $REFS \
  -out:plugins/TeleportRestPlugin.dll plugin/TeleportRestPlugin.cs
```

> **重要**：`dotnet build` 会优化掉 TerrariaServer/OTAPI 程序集引用，导致 TShock 静默跳过插件加载。必须使用 `csc` 直接编译。详见 [plugin/PLUGIN_SOLUTION.md](plugin/PLUGIN_SOLUTION.md)。

### 4. 启动服务

```bash
# 创建必要目录
mkdir -p tshock worlds plugins presets trails worldbackups

# 启动
docker-compose up -d
```

### 5. 访问面板

```
http://your-server-ip:4891
```

## 环境变量

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `TSHOCK_API_URL` | `http://terraria-server:7878` | TShock REST API 地址 |
| `TSHOCK_APP_TOKEN` | `your-rest-token-here` | Application REST Token |
| `PANEL_PORT` | `4891` | 面板监听端口 |
| `CONTAINER_NAME` | `terraria-server` | Terraria Docker 容器名 |
| `SERVER_PORT` | `7777` | 游戏服务器端口 |
| `API_PORT` | `7878` | REST API 端口 |
| `NETWORK_NAME` | `terraria-server_default` | Docker 网络名 |
| `HOST_BASE_PATH` | `/root/terraria-server` | 宿主机基础路径 |
| `TRAIL_MAX_PER_PLAYER` | `1000` | 每个玩家最大轨迹点数 |

## WebSocket 消息类型

| 类型 | 方向 | 说明 |
|------|------|------|
| `log` | 服务端→客户端 | 日志行 |
| `player_join` | 服务端→客户端 | 玩家上线 |
| `player_leave` | 服务端→客户端 | 玩家下线 |
| `status_update` | 服务端→客户端 | 完整状态（5秒同步） |
| `player_info` | 双向 | 玩家信息+轨迹请求/响应 |
| `player_tp` | 双向 | 传送请求/结果 |
| `cmd` | 客户端→服务端 | 执行命令 |
| `cmd_result` | 服务端→客户端 | 命令结果 |

## 项目结构

```
├── setup.sh                    # 一键环境准备脚本
├── docker-compose.yml          # Docker Compose 配置
├── .gitignore
├── panel/
│   ├── main.go                 # Go 后端（~2700 行）
│   ├── go.mod / go.sum         # Go 模块依赖
│   ├── Dockerfile              # 面板容器构建（alpine + 预编译二进制）
│   ├── MANUAL.md               # 完整管理手册
│   └── static/
│       ├── index.html          # 前端单页面应用（~2900 行）
│       ├── itemdb.json         # 物品数据库（5400+ 条目）
│       ├── buffdb.json         # Buff 数据库（174 个）
│       └── prefixdb.json       # 词缀数据库（85 个，含中文名+效果）
└── plugin/
    ├── TeleportRestPlugin.cs   # 传送插件源码（v1.1.0）
    ├── PLUGIN_SOLUTION.md      # 插件方案详细文档
    └── BINARY_PATCH.md         # 二进制补丁方案（已放弃，保留参考）
```

## 关键设计决策

- **SSC 已禁用** — 客户端权威，预设通过 `/give` 发放到背包，玩家手动装备
- **Token 自动刷新** — TShock 重启后自动用保存的凭据重新认证
- **WS 优先** — WebSocket 连接时所有数据通过 WS 推送，断开回退 HTTP 轮询
- **无弹窗** — 不使用 `alert()`/`confirm()`/`prompt()`，全部使用内联 UI
- **纯鼠标驱动** — 所有操作可通过点击完成，最小化键盘输入

## TeleportRestPlugin

TShock 内置 `/tp` 命令设置了 `AllowServer=false`，REST API 无法执行。此插件注册 `/tprest` 命令（`AllowServer=true`），直接调用 `TSPlayer.Teleport()` 实现传送。

- `/tprest <from> <to>` — 将 from 传送到 to
- `/tprest <to>` — 将执行者传送到 to
- 使用 `Teleport(Vector2, bool useBottom, byte style)` 重载，正确设置脚底位置
- 权限节点：`tpothers`

详见 [plugin/PLUGIN_SOLUTION.md](plugin/PLUGIN_SOLUTION.md)。

## 更新部署

```bash
# 面板更新
cd panel && CGO_ENABLED=0 go build -o panel main.go
cd .. && docker stop terraria-panel && docker rm terraria-panel
docker-compose build --no-cache panel && docker-compose up -d panel

# TShock 更新（避免端口冲突，不用 docker restart）
docker stop terraria-server && sleep 3 && docker rm terraria-server
docker-compose up -d terraria
```

## 许可证

MIT License
