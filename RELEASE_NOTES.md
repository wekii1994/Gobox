## 🚀 Gobox v1.19.8-pre1-6 更新说明

本版本重点修复了自动选路优选切节点误断连接、单元测试回归、通知栏网速闪烁以及后台快捷启停空指针等 4 个核心 Bug：

### 🛠️ 核心修复与优化
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
