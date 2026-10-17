# SDD ledger — plan: docs/superpowers/plans/2026-10-17-istoreos-heartbeat.md

Ruling: 使用当前工作区 — 用户已确认在当前仓库/当前会话直接执行；上次迁移全部未提交，创建基于HEAD的worktree会遗漏已验收代码；不重置、不提交既有修改。
Pre-flight: Task1的modem/archive/store类型各自定义，通过heartbeat适配器聚合，无包循环。
Pre-flight: Task2配置/协议由Task3消费；Task3脱敏状态由Task4消费，生命周期集成在Task3。
Ruling: ECS本轮不实现也不创建空远程repo — 用户要求先完成iStoreOS，独立仓库建设属于后续阶段。
Task 1: in progress
Task 1: complete — 健康判定、注册状态解析、AT锁取消、归档部分失败/写失败及数据库健康测试通过。
Task 2: complete — 私有配置、HTTPS/HMAC、重定向/响应拒绝测试通过；协议文档收尾中。
Task 3: complete — 默认禁用上报、600秒周期、重试、重配置取消与周期截止测试通过。
Task 4: complete — API跨源/脱敏/严格JSON和网页编辑保留/密钥清空运行测试通过。
Task 5: in progress — macOS完整race/vet及3项打包测试通过；ARM64原生构建预热中。
Review: 独立审查发现3项，已新增失败测试复现并修复：归档写失败传播、完成时重新核验归档新鲜度、重试限制在当前周期。
Ruling: 设备重启清空了/tmp构建缓存 — 复用现有容器层及隔离挂载，重新创建临时目录；不因旧容器exited就声称旧缓存仍存在。
Ruling: 预热轮完成ARM64 race测试后主动终止其尚未完成的普通vet — 为切换最终源码，不是因为观察超时；普通vet会重复构建非race SQLite。最终轮改用go vet -race复用CGO；主机普通go vet已通过。预热轮不作为最终产物验收证据。
Final source snapshot SHA256: 86dbafb2de66fba225489dc7c68c789a0f918539e273dd66d4ac992925ec06ee。
Final ARM64 workflow: race全套 → vet -race → Python打包测试 → opt-in目标TLS测试二进制 → release/IPK 0.2.0-1。

Task 5: complete — 已安装0.2.0-1，真实七项自检健康，重启及默认禁用配置持久化通过，实机TLS测试夹具上报成功且异常阻断。
Review: 3项修复经独立复审通过；非阻断发送超时状态提示也已补充。
Ruling: 保留工作区未提交状态 — 本轮与上轮已验收修改共存，不自动提交、合并、推送或清理。
Ruling: 打包器默认版本同步0.2.0-1 — 测试先因旧默认0.1失败，修复后3项通过，避免用户手动打包得到误导版本。
最后源快照及哈希以 dist/istoreos-0.2.0-heartbeat-aarch64/verification.json 为准，后续文档编辑不改变已部署代码。
