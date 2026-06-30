# TShock Web Panel 管理手册

## 目录

1. [项目概述](#项目概述)
2. [系统架构](#系统架构)
3. [部署安装](#部署安装)
4. [配置说明](#配置说明)
5. [功能说明](#功能说明)
6. [API 接口](#api-接口)
7. [常见问题](#常见问题)
8. [开发说明](#开发说明)
9. [版本历史](#版本历史)

---

## 项目概述

TShock Web Panel 是一个基于 Web 的 Terraria 服务器管理面板，通过 TShock REST API 实现对 Terraria 服务器的远程管理。

### 主要特性

- 🎮 **玩家管理** — 查看在线玩家、踢出、封禁、禁言、治疗、击杀、传送
- 👤 **用户管理** — 创建/编辑/删除用户账号，分组下拉选择
- 👪 **分组管理** — 创建/删除用户组、查看权限
- 🌍 **世界管理** — 列表/创建/切换/删除/重命名世界，自动重建容器；世界备份/恢复（版本管理）
- 🎯 **事件控制** — 血月、日食、满月、沙尘暴、入侵等（状态高亮）
- ⚔️ **Boss/怪物** — 生成 Boss、生成怪物
- 📦 **物品发放** — 搜索物品（5400+条目，自动加载）、给玩家发放物品
- ⚙️ **装备预设** — 可视化表格编辑器，词缀选择器（类型过滤+中文名+效果提示），一键应用
- 📊 **实时监控** — WebSocket 双向通信（日志流解析玩家上下线事件实时推送，状态+数据每5秒同步校准）
- 🗺 **玩家轨迹** — 实时追踪玩家移动轨迹，Canvas 可视化（缩放/平移/传送线过滤/其他玩家位置标注）
- 🔧 **服务器设置** — 刷怪率、难度、出生点、保护、建造限制等
- 📱 **移动适配** — 使用 100dvh 适配移动端视口
- 🎨 **亮色主题** — 完整亮色主题，对比度优化（dim/强调色调深）
- ✅ **系统验证** — 一键测试所有功能，含日志检查

---

## 系统架构

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   Web 浏览器     │────▶│   Panel 后端     │────▶│  TShock Server  │
│   (前端界面)     │◀────│   (Go + REST)   │◀────│  (REST API)     │
└─────────────────┘     └─────────────────┘     └─────────────────┘
         │                     │
         │  WebSocket          ▼
         └────────────── Docker Socket
                        (容器管理)
```

### 技术栈

| 组件 | 技术 |
|------|------|
| 前端 | HTML/CSS/JavaScript（原生，无框架，~2900行） |
| 后端 | Go（~2700行，gorilla/websocket） |
| 通信 | REST API + WebSocket（双向：日志流+玩家上下线事件+玩家信息/轨迹推送、状态+数据5秒同步、命令执行） |
| 服务器 | TShock 6.1.0.0（Terraria 1.4.5.6）via Docker |
| 容器管理 | Docker Socket API（世界管理时重建容器） |

### 核心设计

- **前端 → 后端 → TShock** — 所有请求通过后端代理，TShock Token 不暴露给前端
- **RESTful API** — 后端提供统一的 RESTful 接口，内部转换为 TShock API 调用
- **Token 自动刷新** — TShock 重启后自动用保存的凭据重新获取 Token
- **Docker API** — 世界管理通过 Docker Socket 直接操作容器（切换/创建世界时自动重建）
- **SSC 已禁用** — 客户端权威，服务端装备修改不会跨登录持久化；装备恢复已移除，世界文件备份/恢复可用
- **预设 = 物品套装** — 通过 `/give` 发放到背包，玩家手动装备

---

## 部署安装

### 前置要求

- **操作系统**：Linux（Ubuntu 22.04/24.04 推荐）
- **Docker** 20.10+（含 Docker Compose）
- **Go** 1.21+（编译面板后端）
- **.NET SDK** 8.0+（编译 TShock 插件，需包含 Roslyn csc.dll）
- **内存**：2GB+（TShock 服务器 + 面板容器）

### 编译环境详解

本项目包含两个需要编译的组件，编译环境必须在**宿主机**上准备（Dockerfile 不编译，只复制预编译产物）。

#### 1. Go 编译环境（面板后端）

| 项目 | 说明 |
|------|------|
| 语言 | Go 1.21+ |
| 依赖 | `github.com/gorilla/websocket v1.5.3`（go.mod 已声明） |
| 编译命令 | `CGO_ENABLED=0 go build -o panel main.go` |
| `CGO_ENABLED=0` | **必须**，面板容器基于 alpine（无 glibc），需要纯静态链接 |
| 产物 | `panel` 二进制文件（Linux amd64） |
| 安装方式 | `apt install golang-go` 或从 https://go.dev/dl/ 下载 |

#### 2. .NET SDK 编译环境（TShock 插件）

| 项目 | 说明 |
|------|------|
| 语言 | C# / .NET 9.0 运行时 |
| SDK | .NET SDK 8.0+（包含 Roslyn csc.dll） |
| 编译方式 | **必须使用 csc 直接编译**，不能用 `dotnet build` |
| `dotnet build` 问题 | 会优化掉 TerrariaServer/OTAPI 程序集引用，TShock 静默跳过加载 |
| csc 路径 | `/usr/lib/dotnet/sdk/<版本>/Roslyn/bincore/csc.dll` |
| 产物 | `TeleportRestPlugin.dll`（.NET 类库） |
| 安装方式 | `apt install dotnet-sdk-8.0` 或从 https://dotnet.microsoft.com/ 下载 |

**csc 编译参数：**

```bash
CSC=$(find /usr/lib/dotnet/sdk -name "csc.dll" -path "*/Roslyn/*" | head -1)

# 引用列表：
# - /tmp/net9rt/*.dll          — .NET 9 运行时 DLL（从 TShock 容器提取）
# - TerrariaServer.dll         — Terraria 服务端主程序集（从容器提取）
# - OTAPI.dll                  — OTAPI 框架（从容器提取）
# - OTAPI.Runtime.dll          — OTAPI 运行时（从容器提取）
# - TShockAPI.dll              — TShock API（从容器提取）

dotnet $CSC \
  -target:library \            # 编译为 DLL
  -nostdlib+ \                 # 不自动引用标准库
  -reference:... \             # 所有依赖 DLL
  -out:plugins/TeleportRestPlugin.dll \
  plugin/TeleportRestPlugin.cs
```

> **注意**：依赖 DLL 需要从运行中的 TShock 容器提取（`docker cp`），首次部署需先启动 TShock 容器一次以获取这些文件。

#### 3. 编译顺序与依赖关系

```
┌──────────────────┐     ┌──────────────────┐
│  1. 启动 TShock   │────▶│  2. 提取依赖 DLL  │
│     容器（首次）   │     │     docker cp     │
└──────────────────┘     └────────┬─────────┘
                                  │
                                  ▼
┌──────────────────┐     ┌──────────────────┐
│  4. 编译 Go 面板  │     │  3. 编译插件 DLL  │
│     go build      │     │     csc           │
└────────┬─────────┘     └────────┬─────────┘
         │                        │
         ▼                        ▼
┌──────────────────────────────────────────┐
│  5. docker-compose up -d                 │
│     面板容器 COPY 二进制，TShock 加载插件  │
└──────────────────────────────────────────┘
```

### 一键环境准备

项目根目录提供 `setup.sh` 脚本，自动完成环境安装、编译和首次部署：

```bash
chmod +x setup.sh
./setup.sh
```

详见脚本内注释。

### 目录结构

```
/root/terraria-server/
├── setup.sh                  # 一键环境准备脚本
├── docker-compose.yml
├── panel/
│   ├── main.go              # Go 后端
│   ├── go.mod
│   ├── Dockerfile
│   ├── MANUAL.md
│   └── static/
│       ├── index.html       # 前端单页面
│       ├── itemdb.json      # 物品数据库（5400+条目，自动加载）
│       ├── buffdb.json      # Buff 数据库（174个）
│       └── prefixdb.json    # 词缀数据库（85个，含中文名+类型标签+效果提示）
├── tshock/
│   ├── config.json
│   ├── sscconfig.json       # SSC 已禁用
│   └── logs/
├── worlds/
├── worldbackups/              # 世界备份文件（host 持久化）
├── presets/                  # 预设 JSON 文件（host 持久化）
├── trails/                   # 轨迹 JSON 文件（host 持久化，每玩家一个文件）
├── plugins/                  # TeleportRestPlugin.dll（/tprest 命令插件）
└── plugin/
    └── TeleportRestPlugin.cs   # 传送插件源码
```

### 安装步骤

1. **构建面板镜像**
   ```bash
   cd panel
   CGO_ENABLED=0 go build -o panel main.go
   docker build -t terraria-panel .
   cd ..
   ```

2. **启动服务**
   ```bash
   docker-compose up -d
   ```

3. **访问面板**
   ```
   http://your-server-ip:4891
   ```

### 首次启动注意事项

- **世界生成耗时**：使用 `-autocreate` 首次启动时，TShock 需 1-2 分钟生成世界（小世界约 60 秒，大世界可达 2 分钟），期间客户端无法连接。查看日志中 `Server started` 表示生成完成。
- **TShock 配置必须预配置**：`tshock/config.json` 中的 `RestApiEnabled: true` 和 `ApplicationRestTokens` 必须在首次启动前配好，否则面板无法连接 REST API。
- **Panel 登录**：面板本身无固定密码，登录时输入任意用户名和密码即可通过（Panel 只验证 TShock App Token 连通性）。但建议输入 TShock 中已注册的 superadmin 账号凭据，以便后续 Token 自动刷新时能正确调用 TShock 用户认证 API。

### 多实例并行部署

同一台服务器可运行多套 TShock + Panel 实例，需修改以下内容：

| 配置项 | 实例1（默认） | 实例2 |
|--------|--------------|-------|
| 游戏端口映射 | `7777:7777` | `7778:7777` |
| REST 端口映射 | `7878:7878` | `7879:7878` |
| 面板端口映射 | `4891:4891` | `4892:4892` |
| `container_name`（terraria） | `terraria-server` | `terraria-server2` |
| `container_name`（panel） | `terraria-panel` | `terraria-panel2` |
| `NETWORK_NAME` | `terraria-server_default` | `terraria-server2_default` |
| `HOST_BASE_PATH` | `/root/terraria-server` | `/root/terraria-server2` |
| `TSHOCK_API_URL` | `http://terraria-server:7878` | `http://terraria-server2:7878` |
| `PANEL_PORT` | `4891` | `4892` |

**部署步骤：**

```bash
# 1. 创建目录
mkdir -p /root/terraria-server2/{panel/static,plugin,plugins,tshock,worlds,presets,trails,worldbackups}

# 2. 复制源码和配置
cp panel/main.go panel/go.mod panel/go.sum panel/Dockerfile /root/terraria-server2/panel/
cp panel/static/* /root/terraria-server2/panel/static/
cp plugin/TeleportRestPlugin.cs /root/terraria-server2/plugin/
cp tshock/config.json tshock/sscconfig.json /root/terraria-server2/tshock/
touch /root/terraria-server2/tshock/setup.lock

# 3. 编译面板和插件
cd /root/terraria-server2/panel && CGO_ENABLED=0 go build -o panel main.go
# 插件编译见 plugin/PLUGIN_SOLUTION.md

# 4. 准备 docker-compose.yml（按上表修改端口和名称）
# 5. 启动
cd /root/terraria-server2 && docker-compose up -d
```

**注意**：每个实例的 `tshock/config.json` 中 `RestApiPort` 保持 `7878` 不变（容器内部端口），端口差异通过 Docker 映射解决。

---

## 配置说明

### 环境变量

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `TSHOCK_API_URL` | `http://terraria-server:7878` | TShock REST API 地址 |
| `TSHOCK_APP_TOKEN` | `opencode-panel-key-2024` | Application REST Token |
| `PANEL_PORT` | `4891` | 面板监听端口 |
| `CONTAINER_NAME` | `terraria-server` | Terraria Docker 容器名 |
| `SERVER_PORT` | `7777` | 游戏服务器端口 |
| `API_PORT` | `7878` | REST API 端口 |
| `NETWORK_NAME` | `terraria-server_default` | Docker 网络名 |
| `HOST_BASE_PATH` | `/root/terraria-server` | 宿主机基础路径 |
| `TRAIL_MAX_PER_PLAYER` | `1000` | 每个玩家最大轨迹点数（最少10） |
| `TRAIL_DIR` | `/trails` | 轨迹持久化目录 |

### docker-compose.yml

```yaml
services:
  terraria:
    image: ghcr.io/pryaxis/tshock:stable
    container_name: terraria-server
    ports:
      - "7777:7777"
      - "7878:7878"
    volumes:
      - ./tshock:/tshock
      - ./worlds:/worlds
      - ./plugins:/plugins           # 插件目录（-additionalplugins /plugins，含 TeleportRestPlugin.dll）
      - ./presets:/presets           # 装备预设持久化
      - ./worldbackups:/worldbackups # 世界备份持久化
      - /etc/localtime:/etc/localtime:ro
    command: -world /worlds/MyWorld.wld -autocreate 2 -worldname "MyWorld" -maxplayers 8 -difficulty 0
    networks:
      - default
    restart: unless-stopped

  panel:
    build: ./panel
    container_name: terraria-panel
    init: true
    ports:
      - "4891:4891"
    environment:
      - TSHOCK_API_URL=http://terraria-server:7878
      - TSHOCK_APP_TOKEN=opencode-panel-key-2024
      - PANEL_PORT=4891
      - CONTAINER_NAME=terraria-server
      - SERVER_PORT=7777
      - API_PORT=7878
      - NETWORK_NAME=terraria-server_default
      - HOST_BASE_PATH=/root/terraria-server
      - TRAIL_MAX_PER_PLAYER=1000
    volumes:
      - ./panel/static:/app/static
      - ./tshock:/tshock:ro
      - ./worlds:/worlds
      - ./presets:/presets           # 装备预设持久化
      - ./trails:/trails             # 轨迹数据持久化
      - ./worldbackups:/worldbackups # 世界备份持久化
      - ./docker-compose.yml:/app/docker-compose.yml
      - /var/run/docker.sock:/var/run/docker.sock
    networks:
      - default
    restart: unless-stopped
```

### TShock 配置 (config.json)

面板依赖以下 TShock 配置项（非默认值以 ⚠ 标注）：

| 配置项 | 值 | 说明 |
|--------|-----|------|
| `ServerPort` | `7777` | 游戏端口 |
| `MaxSlots` | `8` | 最大玩家数 |
| `AutoSave` | `true` | 自动保存 |
| `SaveWorldOnCrash` | `true` | 崩溃时保存 |
| `SaveWorldOnLastPlayerExit` | `true` | 最后玩家退出时保存 |
| `SpawnProtection` | `false` | 出生点保护（面板可切换） |
| `DisableBuild` | `false` | 建造限制（面板可切换） |
| `DisableHardmode` | `false` | 禁止困难模式（面板可切换） |
| `PvPMode` | `"normal"` | PvP 模式 |
| `RequireLogin` | `false` | ⚠ 不强制登录（方便游客加入） |
| `AllowLoginAnyUsername` | `true` | ⚠ 允许任意用户名登录 |
| `DisableUUIDLogin` | `false` | UUID 登录 |
| `KickEmptyUUID` | `true` | 踢出空 UUID |
| `PreventBannedItemSpawn` | `false` | ⚠ 允许生成被封禁物品（预设发放需要） |
| `GiveItemsDirectly` | `false` | 直接发放物品模式 |
| `ForceTime` | `"normal"` | 时间模式 |
| `ForceXmas` | `false` | 强制圣诞（面板可切换） |
| `ForceHalloween` | `false` | 强制万圣节（面板可切换） |
| `AllowCrimsonCreep` | `true` | 允许猩红蔓延 |
| `AllowCorruptionCreep` | `true` | 允许腐化蔓延 |
| `AllowHallowCreep` | `true` | 允许神圣蔓延 |
| `RespawnSeconds` | `0` | ⚠ 即时重生 |
| `RespawnBossSeconds` | `0` | ⚠ Boss 战后即时重生 |
| `MaxHP` | `500` | 最大生命值上限 |
| `MaxMP` | `200` | 最大魔力值上限 |
| `DefaultRegistrationGroupName` | `"default"` | 注册用户默认组 |
| `DefaultGuestGroupName` | `"guest"` | 游客默认组 |
| `BCryptWorkFactor` | `7` | 密码加密强度 |
| `MinimumPasswordLength` | `4` | ⚠ 最短密码4位 |
| `MaximumLoginAttempts` | `3` | 最大登录尝试次数 |
| `EnableChatAboveHeads` | `true` | ⚠ 头顶聊天显示 |
| `ChatFormat` | `"{1}{2}{3}: {4}"` | 聊天格式 |
| `SuperAdminChatPrefix` | `"(Super Admin) "` | 超级管理员前缀 |
| `EnableGeoIP` | `true` | ⚠ 启用 GeoIP |
| `DisplayIPToAdmins` | `true` | ⚠ 管理员可见 IP |
| `DisableSpewLogs` | `true` | ⚠ 禁止刷屏日志 |
| `DisableCustomDeathMessages` | `true` | ⚠ 禁用自定义死亡消息 |
| `DisableTombstones` | `false` | 墓碑开关 |
| `RestApiEnabled` | `true` | ⚠ **必须开启**，面板通过 REST API 通信 |
| `RestApiPort` | `7878` | ⚠ REST API 端口，面板 `TSHOCK_API_URL` 需匹配 |
| `LogRest` | `false` | 记录 REST 请求日志 |
| `EnableTokenEndpointAuthentication` | `true` | Token 端点认证 |
| `ApplicationRestTokens` | 见下方 | ⚠ **必须配置**，面板认证凭据 |

**ApplicationRestTokens 配置（必须）：**

面板通过 Application Token 绕过用户登录直接调用 TShock REST API：

```json
"ApplicationRestTokens": {
  "opencode-panel-key-2024": {
    "Username": "rest_api",
    "UserGroupName": "superadmin"
  }
}
```

- Token 值 `opencode-panel-key-2024` 需与面板环境变量 `TSHOCK_APP_TOKEN` 一致
- `UserGroupName` 必须为 `superadmin` 以获得完整管理权限
- 修改 Token 后需重启 TShock 容器

### TShock SSC 配置 (sscconfig.json)

```json
{
  "Settings": {
    "Enabled": false,
    "ServerSideCharacterSave": 5,
    "StartingHealth": 100,
    "StartingMana": 20,
    "StartingInventory": [
      {"netID": -15, "prefix": 0, "stack": 1},
      {"netID": -13, "prefix": 0, "stack": 1},
      {"netID": -16, "prefix": 0, "stack": 1}
    ]
  }
}
```

⚠ **SSC 必须保持关闭** (`Enabled: false`)。原因：
- SSC 关闭时，客户端权威管理玩家的装备/饰品/染料等槽位状态
- `/give` 发放到**背包**的物品是持久的，重登录后仍在，玩家手动拖到装备栏即可
- 但通过插件直接写入**装备槽**的修改会在重登录后丢失（客户端覆盖服务端装备数据）
- 因此预设系统使用 `/give` 发放到背包，而非直接写入装备槽——这是 SSC 关闭下最可靠的方式

**StartingInventory 说明：**
- `netID: -15` = 铜镐, `netID: -13` = 铜斧, `netID: -16` = 铜剑
- 新玩家首次加入时自动获得这些物品

---

## 功能说明

### 首页仪表盘

| 卡片 | 说明 | 数据来源 |
|------|------|----------|
| 在线 | 当前在线玩家数 | `/status` API |
| 运行 | 服务器运行时间 | `/status` API |
| 时间 | 游戏内 HH:MM + 白天/夜晚 | `/world/read` API |
| 天象 | 血月状态（服务器同步） | `/world/read` API |
| 入侵 | 入侵事件和规模 | `/world/read` API |
| 出生点 | 保护出生点状态 | 本地跟踪 |
| 建造 | 建造限制状态 | 本地跟踪 |
| 难度 | Normal/Expert/Master/Journey | `/worldinfo` 命令 |
| 世界 | 当前世界文件名 | docker-compose command |

### 玩家在线时长

- 后端在解析 TShock 日志流时实时检测 `Broadcast: xxx has joined/left` 事件
- 玩家加入时记录连接时间，离开时移除
- WS 实时推送 `player_join`/`player_leave` 事件，前端即时更新玩家列表
- `streamStatus` 每 5 秒轮询 `/playing` 作为同步校准（防止漏事件）
- 前端玩家卡片显示在线时长，格式：`· X分Y秒` 或 `· X时Y分`
- 前端每秒通过 setInterval 刷新时长显示

### 快捷操作

| 按钮 | 功能 | API |
|------|------|-----|
| 💾 保存 | 保存世界 | `/api/autosave` |
| 🗡 屠NPC | 击杀所有敌对 NPC | `/api/world/butcher` |
| ☀ 白天(4:30) | 设置时间 | `/api/time` |
| 🌙 夜晚(19:30) | 设置时间 | `/api/time` |
| 🔄 重载 | 重载 TShock 配置 | `/api/rawcmd` |
| ❤️ 全体治疗 | 治疗所有在线玩家 | `/api/player/heal` |
| 📢 广播 | 发送广播消息 | `/api/broadcast` |
| ⏻ 关服 | 保存并关闭服务器 | `/api/rawcmd` |

### 世界设置（可折叠）

| 功能 | 说明 |
|------|------|
| 困难模式开关 | 切换困难模式 |
| 切换难度 | Normal → Expert → Master 循环 |
| 保护出生点 | 开关出生点保护（金色高亮） |
| 建造限制 | 开关全局建造限制（金色高亮） |
| 刷怪率 | 设置刷怪率 (1×/3×/10×) |
| 自动保存 | 开关自动保存 |

### 世界事件（可折叠）

事件按钮有**状态高亮**（金色边框），点击切换开/关：

| 事件 | 说明 | 状态同步 |
|------|------|----------|
| 🔴 血月 | 触发血月事件 | ✓ 服务器同步 |
| 🌑 日食 | 触发日食事件 | 本地跟踪 |
| 🌕 满月 | 触发满月事件 | 本地跟踪 |
| 🌪 沙尘暴 | 触发沙尘暴 | 本地跟踪 |
| 🌧 史莱姆雨 | 触发史莱姆雨 | 本地跟踪 |
| 🏮 灯笼夜 | 触发灯笼夜 | 本地跟踪 |
| ☄ 流星雨 | 触发流星雨 | 本地跟踪 |

### 入侵事件（可折叠）

| 事件 | 命令 |
|------|------|
| 👺 哥布林 | `/worldevent invasion goblins 1` |
| 🏴‍☠ 海盗 | `/worldevent invasion pirates 1` |
| ⛄ 雪人 | `/worldevent invasion snowmen 1` |
| 🎃 南瓜月 | `/worldevent invasion pumpkinmoon 1` |
| ❄ 霜月 | `/worldevent invasion frostmoon 1` |
| 👽 火星人 | `/worldevent invasion martians 1` |

### Boss / 怪物（可折叠）

- 输入 Boss ID → 点击「生成 Boss」
- 输入怪物 ID 数量 → 点击「生成怪物」

### 传送 / 位置（可折叠）

- 🏠 回家
- ✅ 允许传送
- 输入传送点名称 → 🌀 传送
- 输入 X/Y 坐标 → 📍 传送到坐标

### 玩家娱乐（可折叠）

- 🤚 扇耳光 / 😤 骚扰 / 🎆 烟花
- 🛌 休息 / 🎉 队伍 / 💬 表情 / 🔄 复活
- Buff 输入（给自己/给玩家）

### 物品 / 封禁（可折叠）

- 给自己物品（输入 ID 数量）
- 封禁物品/弹幕/方块
- 显示日志 / 服务器密码

### 其他功能（可折叠）

- 🛡 神模式 / 🌿 催长植物 / ☠ 蔓延腐化
- 🎃 强制万圣节 / 🎄 强制圣诞节
- 🏚 设地牢 / 🌋 沉降液体 / 🎣 重置渔夫
- 📜 白名单 / 🔄 检查更新

### 玩家管理

**单个玩家操作（点击玩家卡片按钮）：**
- 🎁 给物品 — 打开发放物品弹窗
- ⚙️ 应用预设 — 弹出预设菜单，选择预设应用到该玩家
- 📍拉 — 传送该玩家到自己位置
- 📍去 — 传送到该玩家位置
- ❤️ 治疗
- 💀 击杀（确认条）
- 👢 踢出（确认条）
- 🔨 封禁（确认条）
- 🔇 禁言

**批量操作（选中多个玩家后）：**
- 🎁 给物品 / ❤️ 治疗 / 🔇 禁言
- 👢 踢出 / 🔨 封禁 / 💀 击杀

### 玩家信息弹窗

点击玩家可查看详细信息，展示各槽位物品名称：
- 背包 / 装备 / 染料
- 猪猪存钱罐 / 保险箱 / 熔炉
- Buff 列表
- **玩家轨迹** — Canvas 可视化移动轨迹（详见下方）

### 玩家轨迹系统

后端独立 goroutine `trackPlayerTrails` 每 5 秒轮询 TShock `/v3/players/read` 获取每个在线玩家的坐标，存储最多 1000 个轨迹点（`TrailPoint{x, y, time}`，可通过 `TRAIL_MAX_PER_PLAYER` 环境变量配置）。玩家下线后轨迹数据保留在内存和磁盘，不自动清除。

**数据持久化：**
- 轨迹数据每 10 秒自动保存到 `./trails/` 目录（每玩家一个 JSON 文件）
- Panel 启动时自动加载历史轨迹
- 容器关闭时（SIGTERM）触发保存
- 清理操作后立即保存并清理文件

**前端 Canvas 可视化：**
- 打开玩家信息弹窗时自动加载轨迹
- 显示数量下拉选择：50/100/200(默认)/500/全部
- 每 5 秒通过 WebSocket 自动刷新位置和轨迹（WS 断开时回退 HTTP 轮询）
- 轨迹线渐变：起点暗淡 → 终点明亮绿色
- 起点暗色圆点 + 终点明亮圆点（标注玩家名+坐标）
- **传送线过滤**：连续两点距离 > 200 格时断开连线（避免画出传送飞行线）
- **其他玩家位置**：青色圆点标注其他在线玩家位置和坐标
- **缩放**：鼠标滚轮缩放（0.5×~20×）
- **平移**：鼠标拖拽/触摸拖拽平移画布
- **网格线**：根据轨迹范围自动计算网格间距
- **缩放保持**：数据刷新时如果范围变化<10%则保留当前缩放/平移状态
- **控制按钮**：复位（重置缩放/平移）、清此玩家、清离线、清全部

**API 端点：**
- `GET /api/player/trail?player=NAME&count=N` — 获取玩家最近 N 个轨迹点（count=0 返回全部）
- `POST /api/player/trail/clear` — 清除轨迹数据
  - `{player: "xxx"}` — 清除指定玩家
  - `{scope: "offline"}` — 清除所有离线玩家
  - `{scope: "all"}` — 清除全部

### 物品发放

1. 点击玩家旁边的 🎁 按钮
2. 在弹窗中搜索物品（支持中文/英文/ID）
3. 选择物品和数量
4. 点击「确认发放」

**物品数据库：**
- 5400+ 条目，页面初始化时自动加载
- 支持手动输入物品 ID 直接发放
- `/give` 支持 4 个参数：`/give <item> <player> [amount] [prefix]`

### 装备预设

**预设 = 物品套装**，通过 `/give` 将物品发放到玩家背包，玩家需手动装备。SSC 已禁用，服务端无法直接设置装备槽位。

#### 预设格式

物品以 `id:prefix:count` 字符串存储：

```json
{
  "name": "战士套装",
  "items": ["3063:36:1", "2763:0:1", "2765:0:1"],
  "createdAt": "2026-06-26 10:00:00"
}
```

示例：`"3063:36:1"` = 天顶剑 + 传奇词缀 + 数量1

#### 可视化表格编辑器

预设管理面板提供可视化表格编辑器，每个物品行显示：
- 物品名称
- 词缀下拉选择器（按物品类型过滤）
- 数量输入
- 删除按钮

**词缀下拉选择器：**
- 按物品类型自动过滤可选词缀
  - 武器 → 近战/远程/魔法/通用
  - 工具 → 近战/通用
  - 饰品/翅膀 → 饰品
  - 未知类型 → 显示全部
- 显示类型标签，如 `传奇 [近战]`
- 悬停显示效果详情提示

#### 搜索添加物品

- 搜索物品后点击可快速添加（无词缀，数量1）
- 点击 ⚙ 可自定义词缀和数量后再添加

#### 编辑预设

- 点击编辑按钮，物品加载到可视化编辑器
- 进入编辑模式，可修改物品/词缀/数量
- 保存时通过 `/api/preset/update` 提交，支持 `originalName` 字段实现重命名

#### 应用预设

1. 点击玩家卡片的 ⚙️ 按钮，选择预设
2. 或选中多个玩家，在预设面板点击「应用」
3. 后端对每个物品执行 `/give <item> <player> <count> <prefix>`
4. 弹出结果窗口，显示每个物品的成功/失败状态和 TShock 响应

**预设持久化：** `./presets/` 目录挂载到两个容器，host bind mount 保证容器重建不丢失。

### 用户管理

| 功能 | 说明 |
|------|------|
| 添加用户 | 输入用户名、密码、选择分组（下拉框） |
| 编辑用户 | 修改密码、更换分组（下拉框） |
| 删除用户 | 删除用户账号 |

### 分组管理

| 功能 | 说明 |
|------|------|
| 查看分组 | 显示所有分组和父组 |
| 查看权限 | 点击分组名称查看权限列表（弹窗） |
| 创建分组 | 输入组名、选择父组（下拉框） |
| 删除分组 | 删除指定分组 |

### 世界管理（可折叠）

| 功能 | 说明 |
|------|------|
| 世界列表 | 显示所有 .wld 文件，当前世界标记"当前" |
| 切换世界 | 点击「切换」按钮，自动重建容器（~6秒） |
| 创建新世界 | 输入名称、选择大小和难度，服务器自动生成 |
| 删除世界 | 删除指定世界文件 |
| 重命名世界 | 重命名世界文件 |
| 📂 版本管理 | 备份/恢复世界，查看备份历史 |

**当前世界检测：** 通过读取 docker-compose.yml 的 `command` 行确定实际运行的世界文件名，而非 TShock `/status` API 的 `world` 字段（内部名可能与文件名不同）。若两者不同，列表中文件名下方显示"内部名: XXX"。

**世界大小：**
- 小 (4200×1200)
- 中 (6400×1800)
- 大 (8400×2400)

**世界难度：**
- 普通 (0) / 专家 (1) / 大师 (2) / 旅途 (3)

**操作流程：**
1. 点击「🔄 刷新列表」查看所有世界
2. 点击「➕ 创建新世界」展开创建表单
3. 或点击世界后面的「切换」按钮切换
4. 显示进度条，等待容器重建完成
5. 自动刷新列表和仪表盘状态

#### 世界备份/恢复

每行世界后面有 📂 版本按钮，点击弹出备份管理窗口：

**备份：**
- 点击「备份」创建当前世界文件的副本
- 备份前自动执行 `/save` 保存世界，但**不会重启服务器**
- 备份文件存储在 `./worldbackups/` 目录（宿主机持久化，挂载到面板容器）
- 备份格式：`<worldname>_<timestamp>.wld` + `<worldname>_<timestamp>.meta.json`
- meta.json 包含：timestamp、label（可编辑）、size

**恢复：**
- 选择一个备份点击「恢复」
- 恢复会覆盖当前世界文件，然后执行 `/off` 关服
- 容器配置了 `restart: unless-stopped`，会自动重启并加载恢复的世界

**管理：**
- 查看备份列表（时间、标签、大小）
- 删除备份
- 编辑备份标签

### 系统验证

点击 📋 日志页的「🔍 运行测试」按钮，自动验证 12 项功能：

| 测试项 | 说明 |
|--------|------|
| 服务器状态 | TShock API 连接 |
| 世界信息 | 世界名称/大小 |
| 用户列表 | 用户数量 |
| 分组列表 | 分组数量 |
| MOTD | 消息公告 |
| 广播功能 | 消息发送 |
| 命令执行 | rawcmd 功能 |
| 世界模式 | 难度切换 |
| 血月状态 | 事件查询 |
| 玩家列表 | 在线玩家 |
| 日志文件 | 日志目录访问 |
| 日志检查 | 无异常错误（忽略 TShock 更新检查） |

### 传送功能

**📍拉** — 把某在线玩家拉到当前玩家身边（弹出玩家选择菜单）
**📍去** — 让当前玩家传送到某在线玩家身边（弹出玩家选择菜单）

**实现方式：** TeleportRestPlugin 插件注册了 `/tprest` 命令（`AllowServer=true`），绕过 TShock 内置 `/tp` 命令的 `AllowServer=false` 限制。

- `/tprest <from> <to>` — 将 from 玩家传送到 to 玩家位置
- `/tprest <to>` — 将命令执行者传送到 to 玩家位置（REST 场景下为 Server 玩家）
- 插件使用 `Teleport(Vector2, bool useBottom, byte style)` 重载（`useBottom=true`），与 TShock 内置 `/tp` 行为一致，正确设置脚底位置
- 权限节点：`tpothers`（需确保 superadmin 组拥有此权限，默认已包含）
- **WS 传输**：前端 WS 连接时通过 `player_tp` 消息类型发送传送请求，实时获取结果；WS 断开时回退 HTTP `rc()` 调用

详见 [插件方案文档](../plugin/PLUGIN_SOLUTION.md)。

### 实时日志与 WebSocket

- WebSocket 双向通信（`/api/ws`）
- **日志流实时解析**：后端 `streamLogs` 每 2 秒读取日志增量，解析 `Broadcast: xxx has joined/left` 事件
  - 检测到上线 → 推送 `player_join` 事件 + 记录 joinTimes
  - 检测到下线 → 推送 `player_leave` 事件 + 清除 joinTimes
  - 前端收到事件后即时更新玩家列表和 DOM，无需等待 5 秒轮询
- **状态同步校准**：`streamStatus` 每 5 秒轮询 `/status` + `/world/read` + `/playing`，推送完整 `status_update`（含 players + joinTimes），防止日志事件遗漏
- 命令执行通过 WebSocket 发送
- 颜色标记：ERROR 红色、WARN 青色、INFO 绿色
- 自动滚动到底部

**WS 消息类型：**

| 类型 | 方向 | 说明 |
|------|------|------|
| `log` | 服务端→客户端 | 日志行 |
| `player_join` | 服务端→客户端 | 玩家上线事件 `{name: "xxx"}` |
| `player_leave` | 服务端→客户端 | 玩家下线事件 `{name: "xxx"}` |
| `status_update` | 服务端→客户端 | 完整状态 `{server, world, players, joinTimes}` |
| `player_info` | 客户端→服务端 | 请求玩家信息+轨迹 `{payload: "玩家名"}` |
| `player_info` | 服务端→客户端 | 玩家信息+轨迹响应 `{player, info, trail}` |
| `player_tp` | 客户端→服务端 | 传送请求 `{from: "xxx", to: "yyy"}` |
| `player_tp` | 服务端→客户端 | 传送结果 `{from, to, result/error}` |
| `cmd` | 客户端→服务端 | 执行命令 |
| `cmd_result` | 服务端→客户端 | 命令执行结果 |
| `error` | 服务端→客户端 | 错误消息 |

### 自动补全

在底部指令输入框输入命令时，自动补全覆盖 114 个 TShock 命令：
- 输入时实时弹出匹配列表
- 点击或按 Tab 补全
- ↑↓ 键选择，Enter 执行

---

## API 接口

### 认证

所有 API 需要通过 Cookie `sid` 认证。登录时保存用户名/密码，TShock 重启后自动刷新 Token。

```
POST /api/login
Body: {"username": "xxx", "password": "xxx"}
Response: {"token": "xxx"}
Cookie: sid=xxx
```

### 服务器端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/check` | GET | 检查登录状态 |
| `/api/login` | POST | 登录 |
| `/api/logout` | POST | 登出（销毁 Token） |
| `/api/status` | GET | 服务器状态（含 Token 自动刷新） |
| `/api/rawcmd` | POST | 执行原始命令 |
| `/api/users` | GET | 用户列表 |
| `/api/bans` | GET | 封禁列表 |
| `/api/playerinfo` | GET | 玩家详细信息 |
| `/api/worldread` | GET | 世界信息 |
| `/api/usercreate` | POST | 创建用户 |
| `/api/userdelete` | POST | 删除用户 |
| `/api/userupdate` | POST | 更新用户 |
| `/api/grouplist` | GET | 分组列表 |
| `/api/groupcreate` | POST | 创建分组 |
| `/api/groupdelete` | POST | 删除分组 |
| `/api/groupread` | GET | 读取分组 |
| `/api/autosave` | POST | 自动保存开关 |
| `/api/motd` | GET | MOTD |
| `/api/rules` | GET | 规则 |
| `/api/bloodmoon` | GET | 血月状态 |
| `/api/broadcast` | POST | 广播消息 |
| `/api/logs` | GET | 日志 |
| `/api/logs/stream` | GET | 日志流 |
| `/api/ws` | WebSocket | 双向实时通信 |

### 世界端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/world/hardmode` | POST | 困难模式开关 |
| `/api/world/mode` | POST | 设置世界难度 |
| `/api/world/spawnrate` | POST | 设置刷怪率 |
| `/api/world/maxspawns` | POST | 设置刷怪上限 |
| `/api/world/setspawn` | POST | 设出生点 |
| `/api/world/protectspawn` | POST | 保护出生点 |
| `/api/world/antibuild` | POST | 建造限制 |
| `/api/world/butcher` | POST | 屠杀 NPC |
| `/api/world/create` | POST | 创建新世界 |
| `/api/world/switch` | POST | 切换世界 |
| `/api/world/backups` | GET | 世界备份列表（?world=名称） |
| `/api/world/backup` | POST | 创建世界备份 |
| `/api/world/backup` | DELETE | 删除世界备份 |
| `/api/world/restore` | POST | 恢复世界备份 |
| `/api/world/backup/label` | POST | 修改备份标签 |
| `/api/event` | POST | 触发事件 |
| `/api/time` | POST | 设置时间 |

### 玩家端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/player/heal` | POST | 治疗玩家 |
| `/api/player/kill` | POST | 击杀玩家 |
| `/api/player/kick` | POST | 踢出玩家 |
| `/api/player/ban` | POST | 封禁玩家 |
| `/api/player/mute` | POST | 禁言玩家 |
| `/api/player/give` | POST | 给玩家物品 |
| `/api/player/tp` | POST | 传送玩家 |
| `/api/player/jointimes` | GET | 玩家在线时长（秒） |
| `/api/player/trail` | GET | 玩家移动轨迹（?player=名&count=N，count=0返回全部） |
| `/api/player/trail/clear` | POST | 清除轨迹数据（{player,scope}） |

### 预设端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/presets` | GET | 预设列表 |
| `/api/preset/create` | POST | 创建预设 |
| `/api/preset/update` | POST | 更新预设 |
| `/api/preset/delete` | POST | 删除预设 |
| `/api/preset/apply` | POST | 应用预设到玩家 |

**创建预设请求体：**
```json
{
  "name": "战士套装",
  "items": ["3063:36:1", "2763:0:1", "2765:0:1"]
}
```

**更新预设请求体（支持重命名）：**
```json
{
  "name": "新名称",
  "items": ["3063:36:1"],
  "originalName": "旧名称"
}
```

**删除预设请求体：**
```json
{
  "name": "战士套装"
}
```

**应用预设请求体：**
```json
{
  "presetName": "战士套装",
  "player": "玩家名"
}
```

**应用预设响应：**
```json
{
  "status": "ok",
  "message": "应用完成",
  "results": ["3063:36:1: TShock响应", "2763:0:1: TShock响应"]
}
```

### 其他端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/test` | GET | 系统验证（12项测试） |

---

## 常见问题

### Q: 面板无法登录

**A:** 检查以下配置：
1. TShock REST API 是否启用（`RestApiEnabled: true`）
2. Application REST Token 是否正确配置
3. 面板和 TShock 是否在同一 Docker 网络

### Q: 命令执行失败

**A:** 检查以下权限：
1. 用户是否在 `superadmin` 组
2. REST Token 是否关联到正确的用户组
3. TShock 日志查看具体错误

### Q: 世界切换后状态不更新

**A:** 世界切换需要重建容器（~6秒）。面板会自动轮询等待服务器重启，完成后自动刷新仪表盘和世界列表。

### Q: 世界创建需要多长时间

**A:** 世界生成需要 30-120 秒（取决于世界大小）。面板显示进度条，完成后自动刷新。

### Q: 日志不显示

**A:** 检查以下配置：
1. 面板容器是否挂载了 `/tshock` 目录（只读）
2. TShock 日志路径配置是否正确

### Q: 预设应用后玩家装备没变

**A:** 预设通过 `/give` 将物品发放到玩家背包，玩家需要手动从背包装备到装备栏。SSC 已禁用，服务端无法直接设置装备槽位。

### Q: 词缀下拉列表为空或选项不对

**A:** 词缀按物品类型自动过滤。如果物品类型无法识别，会显示全部词缀。确保 `prefixdb.json` 包含正确的类型标签。

### Q: 事件按钮状态不准确

**A:** 血月状态从服务器 API 实时同步。其他事件（日食/满月/沙尘暴等）通过 localStorage 跟踪，刷新页面保持。如果服务器重启，状态会重置。

### Q: 复制玩家数据在非 HTTPS 环境不工作

**A:** `navigator.clipboard.writeText` 仅在安全上下文（HTTPS/localhost）可用。面板已添加 `document.execCommand('copy')` 降级方案，非 HTTPS 环境下自动使用。

### Q: 世界备份/恢复如何工作

**A:** 备份功能复制当前 .wld 文件到 `./worldbackups/` 目录，不会重启服务器（仅先执行 `/save`）。恢复时覆盖当前世界文件并执行 `/off` 关服，容器自动重启加载恢复的世界。这与之前的装备恢复不同——世界备份/恢复操作的是世界文件本身，与 SSC 无关。

---

## 开发说明

### 项目结构

```
panel/
├── main.go              # Go 后端（~2700 行）
├── go.mod               # Go 模块定义
├── Dockerfile           # Docker 构建文件
├── MANUAL.md            # 本手册
└── static/
    ├── index.html       # 前端单页面应用（~2900 行）
    ├── itemdb.json      # 物品数据库（5400+ 条目，自动加载）
    ├── buffdb.json      # Buff 数据库（174个）
    └── prefixdb.json    # 词缀数据库（85个，含中文名+类型标签+效果提示）
```

### 构建

```bash
# Go 后端
cd panel
CGO_ENABLED=0 go build -o panel main.go
```

### 更新部署

```bash
# ⚠ 必须先编译新二进制，Dockerfile 只 COPY 不编译
cd /root/terraria-server/panel && CGO_ENABLED=0 go build -o panel main.go

# 重建并部署面板容器
cd /root/terraria-server && docker stop terraria-panel && docker rm terraria-panel && docker-compose build --no-cache panel && docker-compose up -d panel
```

**注意：** Dockerfile 使用 `COPY panel .` 复制本地已编译的二进制，不会从源码编译。修改 Go 代码后必须先执行 `go build`，否则部署的仍是旧版本。

---

## 版本历史

| 版本 | 日期 | 更新内容 |
|------|------|----------|
| 1.0.0 | 2026-06-25 | 初始版本 |
| 1.1.0 | 2026-06-25 | 新增 PlayerRestorePlugin 插件，实现装备槽位恢复 + 跨玩家迁移 |
| 1.2.0 | 2026-06-26 | 插件部署路径修正，备份格式双兼容，WebSocket 推送状态/玩家，预设持久化 |
| 2.0.0 | 2026-06-26 | 移除备份/恢复系统（SSC 禁用导致恢复不可靠）；移除 PlayerRestorePlugin DLL；预设改为物品套装模式（id:prefix:count 格式）；新增可视化预设编辑器（词缀类型过滤+中文名+效果提示）；物品数据库改为自动加载；WebSocket 改为双向通信（状态+玩家每5秒推送）；新增世界删除/重命名；新增玩家信息弹窗；移动端适配（100dvh） |
| 2.1.0 | 2026-06-27 | 新增世界备份/恢复（版本管理）：备份 .wld+.meta.json 到 ./worldbackups/，恢复后自动重启；当前世界检测改用 docker-compose command（修复内部名/文件名不一致问题）；玩家在线时长追踪（后端 goroutine 轮询，前端实时显示）；词缀数据库重构为 85 条中文词缀（含类型+效果描述）；预设编辑器支持重命名（originalName）；剪贴板非 HTTPS 降级方案；修复玩家断线未从列表移除（names.length>=0）；修复重复 refreshPlayerList 覆盖；docker-compose 新增 worldbackups 卷挂载 |
| 2.2.0 | 2026-06-27 | 玩家上下线实时检测：后端 streamLogs 解析 TShock 日志 Broadcast 行，检测 has joined/has left 事件，实时推送 player_join/player_leave WS 事件；前端即时更新玩家列表和 DOM（不再依赖 5 秒轮询）；joinTimes 在日志解析时记录/清除；streamStatus 5 秒轮询保留为同步校准；修复 parsePlayerEvent 误识别 Utils/Broadcast 为玩家名（只匹配 Broadcast: 行，去掉 (N/A) 后缀） |
| 2.3.0 | 2026-06-27 | 亮色主题对比度修复（--dim #8888a0→#5a5a72, --green/--cyan/--gold/--red/--orange 全部调深）；select 下拉箭头改用 var(--dim) 跟随主题；预设列表改为卡片布局（名称+操作在顶部一行，物品列表在下方独立展开，不再因物品多导致名称换行）；新建预设时清空编辑器（修复上一次编辑内容残留）；编辑预设时不再被 showCreatePreset 清空（非编辑模式才清空）；WS status_update 无 players 时清空 joinTimes 防止列表残留；传送按钮改为弹出玩家列表选择目标（📍拉=把某人拉到该玩家身边，📍去=该玩家去某人身边）；传送功能待完成：TShock /tp 命令 AllowServer=false 限制 REST API 执行，需二进制 patch TShockAPI.dll 或编写传送插件绕过（补丁已准备好，待部署） |
| 2.4.0 | 2026-06-28 | 玩家轨迹系统：后端 trackPlayerTrails goroutine 每5秒轮询 TShock /v3/players/read 获取在线玩家坐标，存储最多1000个 TrailPoint{x,y,time}（TRAIL_MAX_PER_PLAYER 环境变量配置），玩家下线保留轨迹不自动清除；轨迹持久化到 ./trails/ 目录（每10秒自动保存，启动时加载，SIGTERM 时保存）；GET /api/player/trail?player=NAME&count=N（count=0返回全部）；POST /api/player/trail/clear（支持指定玩家/离线/全部清除）；前端 Canvas 可视化轨迹（渐变线+起止点标注+网格线）；传送线过滤（距离>200格断开连线）；其他在线玩家位置标注（青色圆点+名称坐标）；缩放（鼠标滚轮0.5×~20×）+平移（鼠标/触摸拖拽）；缩放保持（范围变化<10%保留缩放/平移）；控制按钮（复位/清此玩家/清离线/清全部）；显示数量下拉（50/100/200/500/全部）；玩家信息+轨迹改为 WS 传输（player_info 消息类型，WS 断开回退 HTTP 轮询）；HTTP 客户端添加10秒超时防阻塞；docker-compose 新增 trails 卷挂载和 init:true；Dockerfile 新增 STOPSIGNAL；修复 gsY 未定义变量（→gs）；修复 loadOtherPlayerPositions() 未被定时器调用；修复 trail 为空时不应隐藏 canvas；修复 Dockerfile 只复制旧二进制不编译的问题（部署需先 go build） |
| 2.5.0 | 2026-06-28 | TeleportRestPlugin 插件：注册 `/tprest` 命令（AllowServer=true），绕过 TShock 内置 `/tp` 的 AllowServer=false 限制；插件使用 `Teleport(Vector2, bool useBottom, byte style)` 重载（useBottom=true），修复传送偏移（头脚错位+水平偏移）；面板后端 playerTpHandler 改为调用 `/tprest from to`；前端传送改为 WS 优先（player_tp 消息类型，WS 断开回退 HTTP rc()）；插件编译必须使用 csc（非 dotnet build）以保留程序集引用（详见 plugin/PLUGIN_SOLUTION.md）；手册新增首次启动注意事项和多实例并行部署说明 |

---

## 许可证

MIT License
