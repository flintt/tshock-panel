# TShockAPI.dll 二进制补丁方案

## 问题

TShock 的 `/tp`、`/tphere`、`/tpnpc`、`/tppos` 等命令设置了 `AllowServer=false`，通过 REST API 执行时返回 "You must use this command in-game."

### 根因

TShockAPI/Commands.cs 的 `HandleCommand` 方法中有两处检查：

```csharp
if (!cmd.AllowServer && !player.RealPlayer)
    player.SendErrorMessage("You must use this command in-game.");
```

编译后的 IL 代码结构（TShock 6.1.0.0）：

```
偏移      指令              说明
58685:    ldloc.0 (0x06)    加载 AllowServer 值
58686:    brtrue.s (0x2D) 0x27   如果 true（允许服务端）跳过错误块 → 跳到 58727
58688:    ldarg.0           ...
58689:    callvirt          获取 RealPlayer 属性
58693:    ldloc.0 (0x06)    加载 RealPlayer 值
58694:    brtrue.s (0x2D) 0x1f   如果 true（真实玩家）跳过错误块 → 跳到 58727
58697:    ldstr             加载错误字符串
58701:    call              调用 SendErrorMessage
```

两个 `brtrue.s` 都跳转到偏移 58727（错误块之后）。

## 当前补丁方案：brfalse.s

将两个 `brtrue.s` (0x2D) 替换为 `brfalse.s` (0x2C)，跳转偏移不变。

| 偏移 | 原始 | 补丁 | 效果 |
|------|------|------|------|
| 58686 | `brtrue.s` (0x2D) | `brfalse.s` (0x2C) | AllowServer=false 时跳过错误（而非 true 时跳过） |
| 58694 | `brtrue.s` (0x2D) | `brfalse.s` (0x2C) | RealPlayer=false 时跳过错误（而非 true 时跳过） |

### 逻辑变化

**原版逻辑：**
- AllowServer=true → 跳过错误 ✓（REST 和游戏内都正常）
- AllowServer=false 且 RealPlayer=true → 跳过错误 ✓（游戏内正常）
- AllowServer=false 且 RealPlayer=false → 报错 ✗（REST 被拦截）

**补丁后逻辑：**
- AllowServer=false → 跳过错误 ✓（REST 可以执行 AllowServer=false 的命令）
- AllowServer=true → 不跳过，继续检查 RealPlayer
- RealPlayer=false → 跳过错误 ✓（REST 正常）
- RealPlayer=true → 不跳过 → 报错 ✗（**游戏内执行 AllowServer=false 的命令被拦截**）

### 副作用

游戏内玩家无法执行 `AllowServer=false` 的命令（如 `/help`、`/warp` 等），但 `/tp` 等原本 AllowServer=true 的命令不受影响。

实际影响：
- ✅ `/tp` — 正常（AllowServer=true）
- ❌ `/help` — 游戏内被拦截
- ❌ `/warp` — 游戏内被拦截
- ✅ REST API 执行所有命令 — 正常

### 补丁文件

- `/tmp/TShockAPI_original.dll` — 原始 DLL（MD5: 40536ee8c50e81ee0b4bd042851fbaca）
- `/tmp/TShockAPI_patched4.dll` — brfalse.s 补丁（当前部署版本）

### 部署步骤

⚠ 需要重启 TShock，会影响在线玩家

```bash
# 1. 保存世界
curl "http://localhost:7878/v3/server/rawcmd?token=opencode-panel-key-2024&cmd=/save"

# 2. 部署补丁到两个路径（TShock 会从两个位置加载 DLL）
docker cp /tmp/TShockAPI_patched4.dll terraria-server:/tshock/ServerPlugins/TShockAPI.dll
docker cp /tmp/TShockAPI_patched4.dll terraria-server:/server/ServerPlugins/TShockAPI.dll

# 3. 重启
docker restart terraria-server

# 4. 验证
curl "http://localhost:7878/v3/server/rawcmd?token=opencode-panel-key-2024&cmd=/tp+huang+op8"
# 应返回 "Teleported huang to op8."
```

### 恢复原始 DLL

```bash
docker cp /tmp/TShockAPI_original.dll terraria-server:/tshock/ServerPlugins/TShockAPI.dll
docker cp /tmp/TShockAPI_original.dll terraria-server:/server/ServerPlugins/TShockAPI.dll
docker restart terraria-server
```

## 其他尝试过的补丁方案

### 方案 1：br.s（无条件跳转）— ❌ 失败

将 `brtrue.s` (0x2D) 替换为 `br.s` (0x2B)，偏移不变。

失败原因：`brtrue.s` 从栈弹出一个值，`br.s` 不弹栈，导致 .NET IL 验证器报 "Common Language Runtime detected an invalid program."

### 方案 2：pop + br.s — ❌ 失败

将 `ldloc.0` (0x06) 改为 `pop` (0x26)，`brtrue.s` 改为 `br.s` (0x2B)。

失败原因：.NET 报 "Bad method token"，可能是 pop 导致栈状态与方法签名不匹配。

### 方案 3：ldloc.0 → ldc.i4.1 — ❌ 失败

将 `ldloc.0` (0x06) 改为 `ldc.i4.1` (0x17)，向栈推入常量 true，使 brtrue.s 始终跳转。

失败原因：.NET 报 "Bad method token"，可能是 ldc.i4.1 改变了栈上值的类型（int vs bool），验证器拒绝。

### 方案 4：brfalse.s — ✅ 可用（当前方案）

详见上文。有游戏内命令副作用。

## 偏移定位方法（TShock 更新后需重新定位）

1. 在 TShockAPI.dll 中搜索 UTF-16LE 字符串 `"You must use this command in-game."`
2. 找到 `ldstr` (0x72) 指令引用该字符串的位置
3. 向前搜索两个 `brtrue.s` (0x2D) 指令，它们跳转到同一目标（错误块之后）
4. 这两个 brtrue.s 就是需要 patch 的位置

```python
import sys
d = open('TShockAPI.dll','rb').read()
msg = 'You must use this command in-game.'.encode('utf-16-le')
idx = d.find(msg)
print(f'Error message at offset {idx}')
# 从 idx 向前搜索 brtrue.s 对
```

## 注意事项

- TShock 容器内有**两个** DLL 路径需要同时 patch：`/tshock/ServerPlugins/TShockAPI.dll` 和 `/server/ServerPlugins/TShockAPI.dll`
- TShock 容器更新后补丁会丢失，需重新 patch
- patch 偏移可能因 TShock 版本更新而变化
- 推荐优先使用方案 A（插件），二进制 patch 仅作为临时方案
