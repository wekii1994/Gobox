## 🚀 Gobox v1.19.8-pre1-8 更新说明

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
