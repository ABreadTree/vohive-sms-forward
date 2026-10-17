# iStoreOS 自检与心跳上报实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 在当前短信专用服务增加只读自检、可配置且有健康门禁的 HTTPS 心跳上报，并在 iStoreOS 验证。

**Architecture:** 自检状态与发送状态分开，新增 internal/heartbeat 包；通过接口读取模组、归档和数据库，不侵入短信发送路径。ECS 产品实现位于后续独立仓库，当前只交付双方协议和测试接收器。

**Tech Stack:** Go 1.24、标准库 HTTP/HMAC/JSON、现有 SQLite/gousb、现有网页和 OpenWrt/procd。

**Spec:** docs/superpowers/specs/2026-10-17-heartbeat-monitor-design.md（本次分期为 iStoreOS；ECS 章节是跨仓库契约）。

## Global Constraints

- 默认关闭；每 60 秒自检、每 600 秒上报；允许周期 60—86400 秒。
- 自检 45 秒时限、快照 150 秒有效；归档成功时间 150 秒内。
- 单次 HTTP 超时 10 秒；当前周期最多重试 3 次，退避 5/15/30 秒；禁止重定向。
- 仅正常上报；不上报历史积压心跳；认证头不进入 URL；正文上限 4 KiB。
- 心跳配置独立 0600 文件；保存时不可回显/记录密钥，目录 0700。
- 不发测试短信、不改变模组设置、不启动网络业务、不实现 ECS 服务。
- 当前工作区包含已验收但未提交的迁移，不覆盖、重置或混入不相关提交。

## Review Focus

- 收发短信占用 AT 锁导致检查变慢：不可拿旧正常值报心跳（任务 1/3）。
- 归档一部分存储失败、缓存有旧成功时间：当前错误应阻断正常（任务 1）。
- 配置禁用/换地址时旧 HTTP 请求或重试仍在运行：应取消且不带旧密钥发向新地址（任务 3）。
- HTTPS 返回 200 却正文/确认无效，或重定向到其他主机：不可标记发送成功（任务 2）。
- 页面保存失败/重载或密钥留空：不能意外清空旧密钥或偷偷继续使用新配置（任务 4）。

---

### Task 1: 新鲜、只读的健康检查

**Files:** 创建 internal/heartbeat/health.go、health_test.go；修改 internal/modem（新增 health.go/test）、internal/archive/archive.go/test、internal/store（新增 health.go/test）。

**Interfaces:**
- (*modem.Modem).Health(ctx context.Context) (modem.HealthStatus, error)；heartbeat 通过适配器消费，modem 不导入 heartbeat。
- 具体模组结果包含 ATOK、SIMReady、Registered、DataOff 布尔值；不含硬件身份或号码。
- archive.Health() 返回 LastSuccess、LastAttempt、LastErrorCategory；store.Health(ctx context.Context) error 执行只读查询。
- heartbeat.Checker.Check(ctx context.Context) Report；Report 包含 checked_at、healthy、固定检查分类，超时/陈旧均 false。

- [x] 编写失败测试：SIM非READY、未注册、注册状态1/5、支持的注册响应格式、AT错误、数据未核验、归档过期/当前错误、数据库错误和检查超时。
- [x] 运行 `go test ./internal/modem ./internal/archive ./internal/store ./internal/heartbeat`，确认新增用例先失败。
- [x] 实现专用当前状态读取、归档尝试/成功元数据及健康汇总；模组读检查共用现有锁，不启动每个检查的无界 goroutine，不修改现有发送与归档语义。
- [x] 同命令通过并运行相关 race 测试；保留现有长短信和驱动释放回归用例。

### Task 2: 私有配置与跨仓库协议客户端

**Files:** 创建 internal/heartbeat/config.go、client.go 及对应测试；创建 docs/heartbeat-protocol.md。

**Interfaces:**
- Config {Enabled bool, DeviceID string, Name string, Endpoint string, Secret string, IntervalSeconds int}。
- LoadConfig(path string) (Config,error)、SaveConfig(path string,cfg Config) error：严格 JSON、限长、拒绝链接/非0600/非预期owner。
- Client.Send(ctx context.Context,cfg Config,report Report,envelope Envelope) error；Envelope 固定时间戳/nonce/正文，重试复用。
- 固定路径 /v1/heartbeat，HMAC-SHA256 按设计第5节；接收成功需 HTTP 200 且 JSON `{"ok":true}`，响应大小受限。

- [x] 先写配置权限、原子保存、未知字段、无效设备ID/HTTPS地址/密钥/周期、重定向、响应错误及签名向量失败测试。
- [x] 运行 `go test ./internal/heartbeat` 证实失败。
- [x] 实现标准库配置及发送器；密钥至少32随机字节的hex编码（64 hex字符），拒绝地址 userinfo/query/fragment，正文及认证头仅包含约定的设备ID/展示名，不含 SIM、手机号等隐私身份信息。
- [x] 文档固定请求/响应字段、头名称 X-VoHive-Device/Timestamp/Nonce/Signature、签名输入、秒单位、幂等重试和服务器120秒时间窗；提供不含真实密钥的签名测试向量。
- [x] 通过 TLS httptest 验证完整请求，测试传输错误不泄密；同测试命令通过。

### Task 3: 可取消的调度与主进程集成

**Files:** 创建 internal/heartbeat/manager.go、manager_test.go；修改 cmd/vohive-mac/main.go 及新增 heartbeat_startup_test.go。

**Interfaces:**
- Manager.Start(context.Context)、Stop()、Status() Status、Save(Config) error、CheckNow(context.Context) (Report,error)、SendNow(context.Context) error。
- 工厂注入 Checker、Sender、时钟/定时器和随机源，生产值使用 Task1/2。
- Start 默认启用本地自检；仅配置 Enabled=true 才发心跳。Status 安全返回设置但剔除 Secret，仅 secret_configured 布尔值。

- [x] 先写假时钟调度用例：立即检查、600秒、不健康抑制、150秒快照过期、45秒上限、3次重试、重试前重新门禁、禁用/换配置取消旧请求、Stop取消。
- [x] 运行 `go test ./internal/heartbeat ./cmd/vohive-mac` 先失败。
- [x] 实现检查/发送串行任务调度，检查与发送状态锁不跨网络/AT调用持有；所有worker有停止路径，不能堆积检查goroutine。
- [x] main 采用 dataDir/heartbeat.json，归档启动后启动自检，停止时先关闭心跳再关模组/数据库；默认缺配置不影响短信启动。
- [x] TLS集成：真实Manager→测试接收器正常上报、故障不报、重新健康恢复；验证换密钥与换地址不发旧配置；测试/race通过。

### Task 4: 本地 API 与网页设置

**Files:** 创建 internal/server/heartbeat.go、heartbeat_test.go、heartbeat_page_runtime_test.go；修改 internal/server/server.go、static/index.html。

**Interfaces:**
- SetHeartbeatSettings(HeartbeatSettings)；接口方法对应 Task3。
- GET /api/heartbeat 返回脱敏配置、自检及发送状态。
- POST /api/heartbeat/config 保存配置；POST /api/heartbeat/check 自检；POST /api/heartbeat/send 自检通过后发送。
- 沿用本地 Host/Origin、application/json、4KiB限制、严格字段和Cache-Control:no-store。

- [x] 先写API跨源/大包/错误字段/不回显密钥、保存保留旧密钥、发送健康门禁、UI无密钥浏览器存储及刷新安全用例。
- [x] `go test ./internal/server` 先失败。
- [x] 实现独立卡片：自检结果、最近发送状态、启用/ID/名称/HTTPS地址/密钥/周期、保存、自检、上报；密码字段提交后清空，接口报错仅固定类别。
- [x] 页面状态轮询只读安全快照，不让刷新直接反复抢AT；页面运行测试和现有短信专用隐藏网络测试通过。

### Task 5: 打包、回归与目标验证

**Files:** 修改 docs/openwrt.md、README.zh-CN.md、README.md；创建 docs/heartbeat-setup.md；更新 docs/istoreos-verification.md 的新增功能验收章节。

**Interfaces:** 沿用已验收IPK打包方式与目标UCI，不覆盖 notify.json/短信数据库/实际USB标识；ECS部署未完成时仅默认关闭。

- [x] 运行 `go test -race -count=1 ./...`、`go vet ./...`、`python3 -m unittest discover -s openwrt -p 'test_*.py'`、init shell语法及 `git diff --check`。
- [x] 复用目标ARM64/musl构建缓存生成新版本IPK并记录源码/二进制/包hash；不覆盖旧回滚产物。
- [x] 升级前备份目标二进制/配置与数据库一致备份；升级后核对真实SIM、短信归档、Bark/TG配置、数据guard off、自检结果及自启动。
- [x] 测试接收地址为明确的测试夹具；无真实ECS地址时不虚构线上心跳送达。验证默认关闭不发送，并以TLS集成日志证明协议发送通过。
- [x] 按 requesting-code-review 做整体验证审查；修复问题后复测，不把软件测试等同ECS报警验收。
- [x] 完成文档说明：ECS独立仓库依据协议对接；当前只交付iStoreOS端；代码提交保持任务范围，若未获提交指令则保留工作区修改。
