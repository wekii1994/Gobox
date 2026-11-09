## 🚀 Gobox v1.19.8-pre1-1 更新说明

本版本为 Gobox 首个核心故障感知与自愈移植版本，完整继承并优化了 hclash 的高可用与低功耗网络调度能力：

### 🌟 核心特性
1. **出站组被动故障感知（Passive Failure Detection）**
   - 5 秒滑动窗口连续失败追踪与目标域名去重，连续失败达 3 次立即触发快速健康检测。
   - 深度识别 POSIX 系统调用（`ECONNREFUSED` / `ECONNRESET` / `EHOSTUNREACH` / `ENETUNREACH`）与 Windows Winsock WSA（10061, 10054, 10065, 10051）硬故障代码。
   - 智能过滤客户端主动取消（`context.Canceled`），杜绝切换 App 或关闭网页时的误切。

2. **精准断链机制（Dead Connection Purging）**
   - 真正故障倒换时毫秒级清理失效节点的僵尸长连接，避免 TCP 挂死等待。
   - 引入网络探测保护锁（`isNetworkProbing`），在正常延迟微调优选时不误杀健康长连接。

3. **巡查周期与容差参数优化（PWR-01 对齐）**
   - 遵循 hclash 功耗优化标准，巡查周期上限对齐为 300 秒（5 分钟），兼顾自动切换与移动端省电。
   - 容差强制收敛于 50ms（范围 50~80ms），防止高延迟节点粘连或低延迟频繁横跳。
   - 支持同时对 `proxy-providers` 及其健康检查注入 `expected-status: "200-204"` 与 300s 周期。

4. **切网自愈探活与兜底（Network Reset Probing）**
   - Wi-Fi 与移动网络切换时配置 300ms 沉淀延迟与 1200ms 退避重试机制。
   - 支持激活组快速测速与 `probeProvidersFallback` 订阅提供者保底并发探活。
   - 切网测速完成后自动比对节点变动并定向清理失效旧连接。

5. **高并发有界 Worker 消费队列**
   - 测速队列改造为最多 10 个固定 Worker 并发消费，无论几百个节点均不造成协程风暴，超时瞬间释放退出。

6. **全栈故障转移统计与看板（Failover Stats & Toolbox Dashboard）**
   - Go 核心层：原子写入、去抖持久化存储、快照获取及清空重置 IPC Action。
   - Flutter 表现层：工具箱新增「故障转移统计」页面，实时展示今日/累计切换次数、节点健康比率与流水审计。
   - 完整支持多语言本地化（中简、中繁、英、日、韩、俄、波斯）。
