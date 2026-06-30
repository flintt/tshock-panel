# TShock 传送插件方案

## 问题背景

TShock 内置的 `/tp`、`/tphere`、`/tpnpc`、`/tppos` 等命令设置了 `AllowServer=false`，通过 REST API（rawcmd）执行时返回：

```
"You must use this command in-game."
```

面板的传送功能（📍拉/📍去）通过 REST API → rawcmd 调用 `/tp`，因此被此限制拦截。

### 根因

TShockAPI/Commands.cs 的 `HandleCommand` 方法：

```csharp
if (!cmd.AllowServer && !player.RealPlayer)
    player.SendErrorMessage("You must use this command in-game.");
```

REST API 执行命令时 `player` 为 `TSPlayer.Server`（`RealPlayer=false`），而 `/tp` 命令的 `AllowServer=false`，因此条件成立，报错。

---

## 最终方案：TeleportRestPlugin（自定义 /tprest 命令）

### 思路

注册一个自定义 TShock 聊天命令 `/tprest`，设置 `AllowServer=true`，在命令处理函数中直接调用 `TSPlayer.Teleport()` 方法，绕过内置 `/tp` 的 AllowServer 限制。

### 源码

文件：`/root/terraria-server/plugin/TeleportRestPlugin.cs`

```csharp
using System;
using System.Collections.Generic;
using Microsoft.Xna.Framework;
using TShockAPI;
using Terraria;
using TerrariaApi.Server;

namespace TeleportRest
{
    [ApiVersion(2, 1)]
    public class TeleportRestPlugin : TerrariaPlugin
    {
        public override string Name => "TeleportRest";
        public override Version Version => new Version(1, 1, 0);
        public override string Author => "Panel";
        public override string Description => "Server-safe teleport command for REST API";

        public TeleportRestPlugin(Main game) : base(game) { }

        public override void Initialize()
        {
            try
            {
                Commands.ChatCommands.Add(new Command("tpothers", TpRestCmd, "tprest")
                {
                    AllowServer = true,
                    HelpText = "Teleports a player (server-safe, for REST API)"
                });

                Console.WriteLine("[TeleportRest] Registered /tprest command (AllowServer=true)");
            }
            catch (Exception ex)
            {
                Console.WriteLine("[TeleportRest] Init error: " + ex.Message + "\n" + ex.StackTrace);
            }
        }

        private void TpRestCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 1)
                {
                    args.Player.SendErrorMessage("Usage: /tprest <from> <to> or /tprest <to>");
                    return;
                }

                string fromName = null;
                string toName = null;

                if (args.Parameters.Count >= 2)
                {
                    fromName = args.Parameters[0];
                    toName = args.Parameters[1];
                }
                else
                {
                    toName = args.Parameters[0];
                }

                var toList = TSPlayer.FindByNameOrID(toName);
                if (toList.Count != 1)
                {
                    args.Player.SendErrorMessage("Target player not found or ambiguous: " + toName);
                    return;
                }
                TSPlayer target = toList[0];

                TSPlayer source;
                if (fromName != null)
                {
                    var fromList = TSPlayer.FindByNameOrID(fromName);
                    if (fromList.Count != 1)
                    {
                        args.Player.SendErrorMessage("Source player not found or ambiguous: " + fromName);
                        return;
                    }
                    source = fromList[0];
                }
                else
                {
                    source = args.Player;
                }

                source.Teleport(target.TPlayer.Bottom, true, 1);

                source.SendInfoMessage("You were teleported to " + target.Name + ".");
                args.Player.SendInfoMessage("Teleported " + source.Name + " to " + target.Name + ".");
            }
            catch (Exception ex)
            {
                args.Player.SendErrorMessage("Teleport error: " + ex.Message);
            }
        }
    }
}
```

### 关键设计

| 设计点 | 说明 |
|--------|------|
| `AllowServer = true` | 核心绕过：REST API（Server 玩家）可执行此命令 |
| `Teleport(Vector2, bool, byte)` | 使用 `Teleport(target.TPlayer.Bottom, true, 1)` 重载，`useBottom=true` 正确设置脚底位置 |
| `TSPlayer.FindByNameOrID()` | 按名称/ID 查找玩家，与 TShock 命令行为一致 |
| 权限 `tpothers` | 使用 TShock 内置权限节点，superadmin 默认拥有 |
| Teleport 第3参数 `1` | 样式参数，1 = 粒子效果 |

---

## 编译方法

### 关键发现：必须使用 csc，不能用 dotnet build

**`dotnet build` 会优化掉对 TerrariaServer/OTAPI 程序集的引用**（即使 csproj 中显式引用），导致编译出的 DLL 缺少这些程序集引用。TShock 的 Server API 插件加载器在加载 DLL 时会检查程序集引用，如果缺少 TerrariaServer/OTAPI 引用则**静默跳过**，不报任何错误。

`csc` 直接编译不会优化引用，所有 `-reference` 指定的程序集都会保留在 DLL 元数据中。

### 编译步骤

```bash
# 1. 准备依赖 DLL
# 从 TShock 容器中提取运行时 DLL
mkdir -p /tmp/net9rt
docker cp terraria-server:/usr/share/dotnet/shared/Microsoft.NETCore.App/9.0.4/. /tmp/net9rt/

mkdir -p /tmp/tpplugin_build
docker cp terraria-server:/server/TerrariaServer.dll /tmp/tpplugin_build/
docker cp terraria-server:/server/OTAPI.dll /tmp/tpplugin_build/
docker cp terraria-server:/server/OTAPI.Runtime.dll /tmp/tpplugin_build/
docker cp terraria-server:/server/ServerPlugins/TShockAPI.dll /tmp/tpplugin_build/

# 2. 编译
CSC=$(find /usr/lib/dotnet/sdk -name "csc.dll" -path "*/Roslyn/*" | head -1)

REFS=""
for dll in /tmp/net9rt/*.dll; do
  REFS="$REFS -reference:$dll"
done
REFS="$REFS -reference:/tmp/tpplugin_build/TerrariaServer.dll"
REFS="$REFS -reference:/tmp/tpplugin_build/OTAPI.dll"
REFS="$REFS -reference:/tmp/tpplugin_build/OTAPI.Runtime.dll"
REFS="$REFS -reference:/tmp/tpplugin_build/TShockAPI.dll"

dotnet $CSC \
  -target:library \
  -nostdlib+ \
  $REFS \
  -out:/root/terraria-server/plugins/TeleportRestPlugin.dll \
  /root/terraria-server/plugin/TeleportRestPlugin.cs
```

### 编译参数说明

| 参数 | 说明 |
|------|------|
| `-target:library` | 编译为 DLL |
| `-nostdlib+` | 不自动引用标准库（手动指定运行时 DLL） |
| `-reference:/tmp/net9rt/*.dll` | .NET 9 运行时 DLL（mscorlib, System.* 等） |
| `-reference:TerrariaServer.dll` | Terraria 服务端主程序集 |
| `-reference:OTAPI.dll` | OTAPI 框架 |
| `-reference:OTAPI.Runtime.dll` | OTAPI 运行时 |
| `-reference:TShockAPI.dll` | TShock API |

---

## 部署

### 首次部署

```bash
# 编译（见上方）
# ...

# 复制到容器
docker cp /root/terraria-server/plugins/TeleportRestPlugin.dll terraria-server:/plugins/TeleportRestPlugin.dll

# 重启 TShock（注意：不能用 docker restart，可能端口冲突）
docker stop terraria-server && sleep 3 && docker rm terraria-server && docker-compose up -d terraria
```

TShock 启动参数 `-additionalplugins /plugins` 使其加载 `/plugins/` 目录下的 DLL。

### 验证

```bash
# 查看日志确认插件加载
docker logs terraria-server 2>&1 | grep -i "teleportrest"
# 预期输出：
# [Server API] Info Plugin TeleportRest v1.1.0 (by Panel) initiated.
# [TeleportRest] Registered /tprest command (AllowServer=true)

# 测试传送
curl "http://127.0.0.1:7878/v3/server/rawcmd?token=opencode-panel-key-2024&cmd=/tprest+huang+op8"
# 预期：Teleported huang to op8.
```

### 更新插件

```bash
# 1. 重新编译
# 2. 停止 → 删除 → 重建容器（同首次部署）
docker stop terraria-server && sleep 3 && docker rm terraria-server && docker-compose up -d terraria
```

---

## 面板后端集成

面板 Go 后端的 `playerTpHandler` 已改为调用 `/tprest`：

```go
// main.go:1440
cmd := "/tprest"
if body.From != "" && body.To != "" {
    cmd = fmt.Sprintf("/tprest %s %s", body.From, body.To)
} else if body.To != "" {
    cmd = fmt.Sprintf("/tprest %s", body.To)
}
result, err := rawcmdProxy(cmd, s)
```

前端传送改为 WS 优先传输：

- WS 连接时：发送 `{"type":"player_tp","payload":{"from":"xxx","to":"yyy"}}`，服务端响应 `{"type":"player_tp","payload":{"from","to","result"}}`
- WS 断开时：回退 HTTP `rc()` 调用 rawcmd
- 前端收到 `player_tp` 响应后显示 toast 通知

API 接口不变：`POST /api/player/tp` → `{from: "xxx", to: "yyy"}`

---

## 尝试过的方案（已放弃）

### 方案 A：二进制补丁 TShockAPI.dll（brfalse.s）

将 `HandleCommand` 中两个 `brtrue.s` 替换为 `brfalse.s`，反转 AllowServer 检查逻辑。

**结果**：REST 传送可用，但游戏内 22 个命令被误杀（`/login`、`/register`、`/home`、`/spawn`、`/help`、`/warp`、`/item` 等），因为 brfalse.s 同时反转了 RealPlayer 检查。

详见 [BINARY_PATCH.md](./BINARY_PATCH.md)。

### 方案 B：注册自定义 REST 端点

尝试通过 `TShock.RestApi` 注册 REST 端点（如 `/v3/players/teleport`）。

**失败原因**：`TShock.RestApi` 和 `TShock.RestManager` 是**字段**（Field）而非属性（Property），原始代码用反射查找 `GetProperty("RestApi")` 找不到。此外，REST 端点需要 `RestCommandD` 委托类型和 `Rest` 类，通过反射调用过于复杂。

### 方案 D：Teleport(float, float, byte) 坐标偏移（v1.0.0 Bug）

v1.0.0 使用 `Teleport(float x, float y, byte style)` 重载传入 `TPlayer.Bottom` 坐标，导致传送后玩家头部出现在目标脚底、水平方向偏移半个身位。

**根因**：`Teleport(float, float, byte)` 将坐标视为玩家 position（左上角），而 `TPlayer.Bottom` 是脚底中心坐标，两者坐标系不一致。

**修复**：改用 `Teleport(Vector2, bool useBottom, byte style)` 重载（`useBottom=true`），与 TShock 内置 `/tp` 行为一致——先设置 `TPlayer.Bottom = pos`，再根据 Bottom 反算 position 进行传送。

### 方案 E：其他二进制补丁

| 方案 | 做法 | 结果 |
|------|------|------|
| br.s | brtrue.s → br.s | "invalid program"（栈不平衡） |
| pop + br.s | ldloc.0 → pop, brtrue.s → br.s | "Bad method token" |
| ldc.i4.1 | ldloc.0 → ldc.i4.1 | "Bad method token" |

---

## 关键经验总结

### 1. dotnet build 会破坏插件加载

`dotnet build` 会优化掉对 TerrariaServer/OTAPI 程序集的引用，即使 csproj 中显式引用且 `Private=false`。TShock Server API 静默跳过缺少这些引用的 DLL，**不报任何错误**。必须使用 `csc` 直接编译。

### 2. TShock 插件加载机制

TShock Server API 扫描 `ServerPlugins/` 目录（和 `-additionalplugins` 指定目录），对每个 DLL：
1. 检查是否实现 `TerrariaPlugin`（通过程序集引用判断）
2. 如果缺少 TerrariaServer/OTAPI 引用 → 静默跳过
3. 检查 `ApiVersion` 属性是否匹配
4. 实例化并调用 `Initialize()`

### 3. TShock 命令 vs REST 端点

| 方式 | 优点 | 缺点 |
|------|------|------|
| 自定义命令 | 简单可靠，与 TShock 命令系统集成，权限自动管理 | 需通过 rawcmd 间接调用 |
| REST 端点 | 直接 HTTP 调用，可自定义响应格式 | 注册机制复杂（反射），需处理认证 |

选择自定义命令更简单，因为面板后端已有 `rawcmdProxy` 基础设施。

### 4. TShock 容器 DLL 双路径

TShock 容器有两个 ServerPlugins 目录：
- `/tshock/ServerPlugins/` — TShock 安装目录
- `/server/ServerPlugins/` — 主加载目录

二进制补丁需要同时替换两个路径的 DLL，但插件只需放在 `/plugins/` 目录（通过 `-additionalplugins` 加载）。

### 5. TShock API 中的 Field vs Property

TShock 的 `TShock` 静态类中，`RestApi` 和 `RestManager` 是**字段**（`F:TShockAPI.TShock.RestApi`），不是属性。通过反射查找时必须用 `GetField()` 而非 `GetProperty()`。这是 V2 插件失败的直接原因。

### 6. Teleport 重载选择

`TSPlayer.Teleport()` 有两个重载：
- `Teleport(float x, float y, byte style)` — 坐标作为 position（左上角），不适合传 Bottom
- `Teleport(Vector2 pos, bool useBottom, byte style)` — `useBottom=true` 时先设置 `Bottom = pos`，再反算 position

必须使用第二个重载，否则会出现头脚错位和水平偏移。需要 `using Microsoft.Xna.Framework;` 以使用 `Vector2`。

---

## 文件清单

| 文件 | 说明 |
|------|------|
| `/root/terraria-server/plugin/TeleportRestPlugin.cs` | 插件源码（v1.1.0，修复传送偏移） |
| `/root/terraria-server/plugins/TeleportRestPlugin.dll` | 编译后的插件 DLL（部署到容器） |
| `/root/terraria-server/plugin/BINARY_PATCH.md` | 二进制补丁方案文档（已放弃，保留作参考） |
| `/root/terraria-server/panel/main.go` | Go 后端，`playerTpHandler` 使用 `/tprest`，WS `player_tp` 消息处理 |
| `/tmp/TShockAPI_original.dll` | 原始未修改的 TShockAPI.dll（备份） |
| `/tmp/TShockAPI_patched4.dll` | brfalse.s 补丁版（有副作用，不再使用） |
