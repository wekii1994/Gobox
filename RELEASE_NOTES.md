## 🚀 Gobox v1.19.8-3 更新说明

本版本在 `v1.19.8-2`（完全对齐上游 `appshubcc/Bettbox` `v1.19.8` 重推源码）的基础上，完整合入代码审查修复（`0b9391ed` F1–F7）并解决后续复现的两个并发与共享状态残留问题：

### 🛠️ 核心可靠性与并发修复（F1–F7，来自 `0b9391ed`）
1. **策略组健康切换分类与并发安全**
   - 仅当旧节点已失活时才触发 `OnGroupNowChanged` 计入故障转移，健康节点延迟优化重选走 `OnGroupSelectionChanged` 并保留现有活跃连接。
   - 隔离合成延迟探测上下文（`WithLatencyTestContext`），避免探测成功/失败误清真实被动故障计数或误标记真实流量活跃时间。
   - 为 `URLTest.selected` 补充读锁保护，并与 `ForceSet` / `Set` 协同避免并发读写竞争；合并 `URLTest` 与 `Fallback` 健康检查选择通知逻辑。
2. **网络探测与路由刷新生命周期治理**
   - 停止网络探测时取消进行中的后台任务并等待收敛完成，门控与去抖按事件策略统一生效，防止旧探测任务写回过期节点状态。
3. **故障转移统计持久化与页面一致性**
   - 统计文件改为临时文件落盘后原子重命名，防范半写入损坏；切换或不可读目录时保留既有统计保护并只回退不覆盖。
   - 实现 Go 端单飞写合并与重置屏障，配合 `FailoverStatsNotifier` 请求代数防抖，避免异步重置与刷新交叉写回过期统计。

### 🐛 残留边界问题修复（`e0d13088`）
1. **共享自动策略组多入口活跃标记隔离**
   - 在 `GroupBase` 按上游 `Selector` 调用链来源维护流量活跃记录；当多个 `Selector` 入口或直连规则共用同一个 `URLTest`/`Fallback` 策略组时，切走其中一个 `Selector` 只清理该入口归因的活跃标记，保留其他仍在转发流量的入口状态。
2. **统计页关闭重进后的跨实例串行同步**
   - 将 `FailoverStatsNotifier` 的排队互斥与请求代数抽离到容器级共享协调器，确保旧页面退出前已发起或排队中的清空/刷新先执行完成，紧接着重进统计页时不再读回未清空前的旧计数。

---

## 📦 历史版本（v1.19.8-2）

本版本完全对齐上游 `appshubcc/Bettbox` `v1.19.8` 正式版重推源码（`88b9597a`）：

### ✨ 上游 v1.19.8 重编版同步
1. **更新内置托管分流脚本（`assets/scripts/managed.js`）**
   - 支持仅包含 `proxy-providers` 节点提供器的订阅配置，避免无静态节点时误报未找到代理节点。
   - 调整直连探测地址为 `http://dns.alidns.com`，并在 `proxyServerDNS` 与 `hosts` 中补充 `https://doh.pub/dns-query#DIRECT` 及 `dns.alidns.com` / `doh.pub` 双栈解析条目。
2. **UI 与主题行为对齐**
   - 仪表盘快捷切换主题模式（自动 / 浅色 / 深色）时新增轻量提示反馈。
   - 同步上游移除「纯黑模式」（`pureBlackMode`）相关配置、界面入口及词条。

---

## 📦 历史版本（v1.19.8-1）

本版本正式同步上游 `appshubcc/Bettbox` `v1.19.8` 正式版全量代码，保留并适配 Gobox 全部自定义特性，并修复审查中发现的策略组容差与缩放交互问题：

### ✨ 上游 v1.19.8 同步与适配
1. **同步上游最新正式版内核与特性**
   - 合入上游 `v1.19.8` 的 `IPv6` TUN 开关透传、Android 导出函数 Panic 恢复保护、托盘/测速工具代理引用计数、`ManagedProps` 自定义区域规则与脚本版本升级检测、仪表盘水印开关及桌面端 UI 细节优化。
   - 同步移除上游废弃的 `overrideTestUrl` 与 `excludeChina`，并对齐全量多语言词条与 `freezed` / `riverpod` 生成代码。
2. **完整保留 Gobox 核心定制功能**
   - 保留被动故障感知与秒级故障转移统计（`FailoverStats`）、`pageScale` 页面整体缩放设置、前台通知栏物理节点名实时显示、默认跳过免责声明及全部策略组/测速兼容策略。

### 🛠️ 缺陷修复与并发加固
1. **修复 `URLTest` 容差判断失效与 `selected` 并发竞态**
   - 修正当当前节点恰好为首个成员时 `fastNotExist` 恒为 `true` 导致跳过 `tolerance` 容差校验与 `oldNodeAlive` 误判的问题。
   - 为 `URLTest.selected` 与 `fastNode` 增加互斥锁保护，防止并发测速、强制切组与状态序列化期间的数据竞争。
2. **修复 `pageScale` 缩放模式下的全局坐标偏移**
   - 为右键上下文菜单、级联子菜单、`CommonPopupBox` 弹出锚点与取色器画板补充 `Overlay` / 局部坐标系转换，避免非 `1.0` 缩放倍率下弹出菜单与取色点击错位。

---

## 📦 历史版本（v1.19.8-pre1-12）

本版本围绕 Android 前台服务通知可靠性与构建发布链路继续加固：

### 🛠️ 核心修复与优化
1. **统一通知管理与 `startForeground()` 真实状态校验**
   - 提取共享 `ServiceNotificationHelper`，将 `GoboxService` 与 `GoboxVpnService` 的通知栏状态、构建器及前后台切换逻辑收口统一。
   - 修复 `BaseServiceInterface.kt` 中 `Service.startForeground()` 吞掉异常导致 `hasStartedForeground` 误置位的问题，改为真实返回 `Boolean` 状态；同时修复 VPN 服务在“未启动前台 + 息屏 + 存在缓存网速”场景下可能跳过系统 `startForeground()` 的隐患。
2. **串行化通知栏更新，消除开关瞬间的协程竞态**
   - 使用 `Mutex` 串行化前台通知的启动、标题更新、网速更新与恢复动作，并在 `VpnPlugin` 中采用 `Dispatchers.Main.immediate` 按 FIFO 顺序派发；在关闭网速通知瞬间，后台滞后 ticker 与陈旧更新不再回写覆盖通知栏，同时避免快速反切时误清启用状态。
3. **清理 Dart 侧冗余遗留状态与优化 CI Release 提取逻辑**
   - 移除 `lib/plugins/service.dart` 中已无调用方的 `_speedNotificationProfile`、`hasSpeedNotificationProfile` 与 `needsSpeedNotificationSync`。
   - 优化 `.github/workflows/build.yaml` 的发布说明截取规则：改为按 `## .*历史版本` 分界并裁剪尾部分隔符，避免当前版本更新说明中出现 `---` 时被误截断。

---

## 📦 历史版本（v1.19.8-pre1-11）

本版本针对 Android 通知栏网速动态切换场景进行深度闭环优化：

### 🛠️ 核心修复与优化
1. **彻底解决动态关闭网速通知时通知栏残留旧网速冻结问题**
   - 在 `android/app/src/main/kotlin/com/wekii/gobox/plugins/VpnPlugin.kt` 的 `handleUpdateNotificationTitle` 中补充 `GlobalState.isSpeedNotificationEnabled = false` 并显式调用 `stopSpeedTicker()` 取消轮询任务。
   - 在 `GoboxVpnService.kt` 与 `GoboxService.kt` 中，当接收到纯标题更新时彻底清空 `pendingSpeedInfo = null` 并复位内容为默认服务运行文案，杜绝由于状态未复位导致通知栏常驻静态历史网速。

---

## 📦 历史版本（v1.19.8-pre1-10）

本版本继续深化代码审查，集中修复 3 项深层隐患并优化构建工具链：

### 🛠️ 核心修复与优化
1. **解包底层 ProxyAdapter 恢复网络切换并发测速能力**
   - 在 `core/connections.go` 导出 `UnwrapProxyAdapter`，并在 `core/network_probe.go` 针对 `contextHealthCheckedGroup`、`failureResetter`、`ProxyGroup`、`candidateCountGroup`、`timeoutAwareGroup` 全面引入解包机制。防止 Mihomo 统计或鉴权包装层导致接口类型断言失败而静默跳过并发快速探测。
2. **异步清理故障转移残留长短连接**
   - 在 `outboundgroup.OnGroupNowChanged` 回调中将 `handleCloseGroupStaleConnections` 转为独立 goroutine 异步执行，彻底消除大量连接场景下遍历全局连接表导致的同步阻塞，保障前端 UI 与通知栏毫秒级响应。
3. **Android NDK 候选目录版本降序排序**
   - 在 `setup.dart` 中对 NDK 路径列表执行降序排序，确保在系统同时存在多个 NDK 版本时始终优先选择最新版 NDK，避免命中老旧不兼容版本。
4. **Android 14+ 前台服务启动标志位全链路加固**
   - 完善 `GoboxVpnService.kt` 与 `GoboxService.kt` 启动逻辑，仅在 `startForeground()` 成功后置位 `hasStartedForeground = true`，杜绝偶发系统异常导致的状态失步。

---

## 📦 历史版本（v1.19.8-pre1-9）

本版本集中修复由全链路 Code Review 排查出的 5 项并发与边界关键 Bug：

### 🛠️ 核心修复与优化
1. **修复通知栏实时网速逻辑倒挂冻结**
   - 移除了 `lib/controller.dart` 中误将持续网速分流进静态标题更新的守卫条件；开启网速通知后，每秒可靠调用 `updateNotificationSpeed` 推送实时动态网速；关闭网速显示时仅在物理节点变动时调用 `updateNotificationTitle` 同步标题。
2. **多策略组并发测速缓存隔离（解决 Cache Thrashing）**
   - 将 `groupbase.go` 中的单例探测池升级为 `map[any]map[string]uint16`；彻底消除多个自动选择组并发测速或两阶段网络探测重试时，不同轮次（Round）互相重置覆盖并清空彼此测速缓存的并发踩踏 Bug。
3. **Android 14+ 前台服务标记置位时机后移**
   - 修正 `GoboxVpnService.kt` 与 `GoboxService.kt` 中 `hasStartedForeground = true` 的置位时机至 `startForeground()` 成功之后；若遇系统权限等偶发异常，状态不再被错误锁死，后续更新可重试进入前台，避免被 Android 14 系统机制误判定为后台服务进而被冻结强杀。
4. **后台无界面 Isolate 直连多语言组件安全兜底**
   - 在 `resolvePhysicalOutboundTitle` 中增加防空指针保护；当通过控制中心快捷磁贴由后台独立 Isolate（`_service`）拉起直连模式时，安全降级回 `'Direct'`，防止底层 Localizations 未初始化抛出 `NullCheckError` 异常闪退。
5. **URLTest 空代理列表防 Panic 保护**
   - 为 `URLTest.Now()` 增加 `if fastProxy == nil { return "" }` 安全防护，防止节点被正则过滤全部清空等极端配置下触发 Go 内核致命空指针 Panic。

---

## 📦 历史版本（v1.19.8-pre1-8）

本版本重点修复“故障转移统计误记未使用的闲置地区策略组”的统计口径问题：

### 🛠️ 核心修复与优化
1. **按真实流量活跃度严格过滤故障转移统计**
   - 新增合成测速上下文标记 `constant.WithLatencyTestContext()` / `IsLatencyTestContext()`，并在 `Proxy.URLTest()` 统一打标；手动点击卡片测速、后台 300s 巡检（`HealthCheck`）与切网测试请求不再被误判为用户真实流量或触发 `onDialFailed()` 被动故障计数。
   - 在 `outboundgroup.GroupBase` 新增原子活跃时间戳 `lastTrafficAt`（仅在真实 `DialContext` / `ListenPacketContext` 转发进入且非测速请求时刷新）；并在 `Selector` 切换选组时通过 `resetDeselectedChainTraffic()` 立即清空被切走链路的活跃状态。
   - `OnGroupNowChanged` 记录 `recordFailover()` 与清理旧连接前校验 `isTrafficActiveGroup()`（5 分钟活跃窗口），未承接流量的闲置策略组（如 `家宽节点-自动选择`、`台湾-自动选择`、`香港-自动选择`、`自动选择`）仅刷新 UI `notifyGroupsChanged()`，不再污染故障转移统计数据。

---

### 📦 历史版本（v1.19.8-pre1-7）
本版本继承 v1.19.8-pre1-7 及更早版本全部能力：
1. **常驻通知栏标题同步与 1s 网速 Ticker 彻底解耦**（v1.19.8-pre1-7）
   - 在 Android Kotlin（`VpnPlugin` / `ServicePlugin` / `GoboxVpnService` / `GoboxService`）与 Dart（`Service` / `Vpn` / `AppController` / `main.dart`）双端新增独立 `updateNotificationTitle` MethodChannel 通道。
   - 关闭"通知栏显示实时网速"时，切节点或亮屏不再意外开启 1s 网速 ticker；开启网速显示且已初始化通知标题后，每秒 `updateTraffic()` 不再重复发送 Dart→Kotlin 标题 IPC；启停与 `restoreNotification()` 同步清理并复位相关状态。

2. **补全 `GroupBase.TestTimeout()` 并恢复策略组真实超时预算**
   - 在 `outboundgroup.GroupBase` 新增 `TestTimeout() time.Duration`，使 `calculateGroupProbeBudget()` 能读取 `URLTest`/`Fallback` 策略组配置的真实 `test-timeout`（未配置或 <= 0 时默认 5s）。

3. **显式区分健康检查并发进行中状态（`ErrHealthCheckInFlight`）**
   - `outboundgroup.ErrHealthCheckInFlight` 取代 `URLTest` 与 `Fallback` 的静默 `nil, nil`；`probeGroupsConcurrently()` 识别该状态并视为本轮满足，避免误判失败触发重复重试。

4. **重写 `classifyAutoGroups()` 选择器链路遍历逻辑**
   - 移除 `depth < 6` 深度上限，改用每条链路独立的 `chainVisited` 环检测集合，支持深层链路、安全终止环路及多入口汇聚到同一下游策略组，并保持 `primary` / `secondary` 低功耗探测分级。

5. **加固 `failover_stats.go` 加载校验与损坏备份**
   - JSON 解析失败时备份至 `failover_stats.json.corrupt` 并记录告警日志；新增 `sanitizeLoadedStats()` 规范化负数统计、`Today <= Total`、非法日期、无效记录、负时间戳及超长记录列表。

6. **补全剩余多语言 `pageScale` 与收口 `patch.go` YAML 标量解析**
   - 补齐 `zh_TC`、`fa`、`ja`、`ko`、`ru` 的 `pageScale` 与 `pageScaleDesc` 本地化词条。
   - 在 `core/patch.go` 提取共享 `parsePositiveInt` / `normalizeHealthCheckInterval` 辅助函数，保持全部既定夹紧参数不变并统一修复非数字字符串 `interval` 兜底。

---

### 📦 历史版本（v1.19.8-pre1-6）
本版本继承 v1.19.8-pre1-6 全部能力（健康延迟优选与故障转移区分、`getGroupTestURL` 兼容降级、瞬时网速同步与熄屏缓存修复、无界面独立 isolate 空指针修复等）：
1. **区分健康延迟优选与节点死亡倒换，防止误断连接与误计 Failover**
   - 在 `outboundgroup` 新增 `OnGroupSelectionChanged` 回调。当 `URLTest.fast()` 因延迟抖动超过容差而切换到更快的健康节点（且原节点依然存活）时，仅触发前端与通知栏刷新，不再错误触发故障转移计数或强制断开正常长连接。
   - 仅在原节点确实失活/移除时触发 `OnGroupNowChanged` 记录故障转移并清理僵尸连接。

2. **修复 `getGroupTestURL` 兼容降级路径并增强包装适配器解包**
   - 保留 `TestURL()` 无锁零反射快速路径以彻底规避 `singleflight` 递归死锁，同时恢复 `MarshalJSON()` 兜底解析逻辑（修复了 `TestFailoverStatsURLSpecificAliveCheck` 回归失败）。
   - 新增 `unwrapProxyAdapter`，支持正确解包 `*adapter.Proxy` 包装后的策略组读取 `TestURL` 与节点存活状态。

3. **修复常驻通知栏节点切换瞬间网速文本闪烁与熄屏漏同步问题**
   - `syncNotificationTitleNow()` 改用最近一次采样瞬时网速 `_lastTraffic`，不再误传入累计总流量 `totalTraffic`。
   - 仅在亮屏且通知 IPC 发送成功后更新 `_lastNotificationTitle` 缓存，解决熄屏期间后台切节点后亮屏通知栏不刷新的问题。

4. **修复安卓通知栏快捷开关 / 开机自启无界面模式下的空指针崩溃隐患**
   - 抽取共享纯函数 `resolvePhysicalOutboundTitle()`；在 `_service` 后台独立 isolate 启动时改为直接向 Go 内核查询 `getProxiesRaw()` 解析物理节点名，不再访问尚未初始化的 `globalState.appController`。

---

### 🌟 历史累积核心能力
- **内核切换即时通知 Flutter 与常驻通知栏秒级同步**（`GroupsChangedMessage` 广播、`syncNotificationTitleNow()` 与 `TestURL()` 防死锁优化）。
- **常驻通知栏穿透解析真实出站节点**（自顶向下递归层析解析当前承载流量的物理节点名）。
- **首次启动跳过免责声明强阻断**（默认自动同意，无感丝滑启动）。
- **全局页面等比无损缩放系统**（60% ~ 120% 自由滑块调节，大屏响应式自动重排网格）。
- **内核被动故障秒级感知与无感自愈**（滑动窗口多域名硬故障去重，3 次连续失败触发快速重测）。
- **精准断链机制**（故障切换秒级斩断死连接，正常微调保留健康连接）。
- **巡查周期 300s 与 50ms 容差压制**（PWR-01 低功耗标准对齐）。
- **有界工作池并发控制**（URLTest 限制 10 Workers，彻底解决大机场协程爆炸）。
- **切网两阶段自愈引擎**（300ms 快速探测 + 1200ms 退避重试 + Provider 兜底）。
- **可视化故障转移看板**（内核级原子流水记录，Riverpod 异步状态管理）。
