# iStoreOS 验收记录（进行中）

## 目标与完成条件

Radxa E20C / iStoreOS 24.10.6 / Linux 6.6.127 / aarch64_generic / musl。
只收发短信、归档、Bark/Telegram 转发，以及指定 Telegram 私聊二次确认发送。
不启用模组上网、流量采样或链路恢复。最终完成还需要真实 USB 收发与通知送达验收。

## 已取得的证据

- 目标已有 `libusb-1.0-0 1.0.27-r1`、`libpthread`、`ca-bundle`。
  库名已按 ABI 后缀修正识别，未覆盖目标运行库。
- 目标原有网络的未认证 HTTPS 检查：Telegram 公共入口 HTTP 302、Bark
  公共入口 HTTP 200，两者 TLS 校验结果均为 0。这只是网络前提，不是推送送达证明。
- 目标时区为 Asia/Shanghai，存在 `/etc/localtime`。
- IPK 打包单元测试覆盖文件内容、权限、配置保留、root 属主、可重复输出，
  以及错误 CPU/libc、截断段和非可执行 ELF 拒绝路径；3 项测试通过并完成独立复审。
- 补测发现旧 PDU 解码器会接受 TP-UDL 大于实际载荷并返回截断正文。
  已以先失败再修复的回归测试验证长度/UDH/UTF-16 防护；中文、emoji、GSM7、
  二进制及乱序拼接→SQLite 去重→双渠道排队的本地测试通过，独立复审无重要问题。
  长度规则依据 [ETSI TS 23.040 §9.2.3.16](https://etsi.org/deliver/etsi_ts/123000_123099/123040/16.00.00_60/ts_123040v160000p.pdf)。

## 最终源码原生构建：通过

- 设备现有 Docker 容器 `codex-vohive-arm64-build`；最终验证轮已终止，退出码 0。
- 官方镜像固定摘要：
  `sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191`。
- 工具链 Go 1.24.13 linux/arm64、musl 1.2.5；2 CPU、2300 MiB，无 USB/主机网络挂载。
- 最终源码快照 SHA-256：
  `c5ee5740641a6e5c0e16e68d26813ed37012215edfffdd84d6c2e0443c35e2de`。
- 最终轮（含 PDU 和 ELF 校验修复）的 `go test -race -count=1 ./...`、`go vet ./...`、
  3 项 Python 打包测试全部通过。release 使用 `sqlite_omit_load_extension`、`-trimpath`、`-s -w`。
- 首轮构建未包含后来修复，仅作为预热缓存；最终源码已重新运行完整验证，未沿用首轮结论。
- 构建日志与产物保存在本地 `dist/istoreos-24.10.6-aarch64/`，设备临时目录
  `/tmp/vohive-arm64-build/` 保留构建缓存以便后续 USB 问题修正；不会重复创建构建。

### 交付物

- `vohive-sms-forward_0.1.0-1_aarch64_generic.ipk`（3,563,154 字节）。
- IPK SHA-256：`b5ff527e9d42d3935636e14ebefb078eec10d0822bc5aa5ecca30e56c7496799`。
- 二进制 SHA-256：`b3c3f48d0578d870493454019d82ff26057196e66b92fe5227f8fea36345e8cd`。
- 另有 `SHA256SUMS`、`verification.json`、`native-build.log` 和无模组 HTTP 快照。
- 实际采用 ARM64/musl 容器原生构建与项目打包器；并非声称 SDK recipe 已执行。

## iStoreOS 主机安装及无模组验收：通过

1. 主机动态加载器成功解析 `libusb-1.0.so.0`、musl 和 `libgcc_s.so.1`；
   Alpine 记录的 libc SONAME 正确映射到目标已有 musl。二进制 `-h` 在主机直接运行成功。
2. `opkg --noaction install` 依赖检查后正常安装，无 force 参数、无运行库覆盖。
   已安装版本 0.1.0-1 / aarch64_generic。首次安装 `enabled=0`，没有自动启动业务。
3. 为验收启用本项目服务。procd 实际命令包含 `-sms-only=true` 和
   `-archive-delete=false`，仅绑定 `127.0.0.1:7676`；服务已保持运行以等待模组。
4. 正常启动、重启、停止后监听消失、再次启动均通过。日志证实 PID 依次为
   12261、12414、12486。临时修改未知 SIM 标签后跨重启保留，随后已恢复原标签；
   没有创建或发送测试短信。
5. 网页两个入口、状态、SIM、短信列表、通知状态均 HTTP 200。
   `/api/traffic`、`/api/metered` GET/POST 返回 404 `sms_only`；
   移动数据开/关写入均 403。通知跨源写入 403，无效 Bark/Telegram 配置写入 400，
   未生成凭据文件。
6. HTTP 快照确认服务端注入短信专用模式、短信及通知表单保留；无模组时
   `guard_state=pending` 且 `enabled=null`，没有误报为数据已关闭。
7. 配置及数据目录权限分别 0600/0700。`/etc/config/network` 与
   `/etc/config/firewall` 的前后 SHA-256 一致。

## 2026-10-17 真实模组接入：部分通过

- 用户插入模组后，系统实际枚举 `2c7c:0125`，型号查询为 Baiwang QDC507，
  固件 QDC507GLEFM21。与默认 `2ca3:4006` 不同，初始未连接原因已定位。
- 经 `/dev/ttyUSB2` 的 AT、ATI、只读自动拨号查询确认 interface 2 为可用 AT 口。
  已仅修改目标本项目 UCI 为 `vid=2c7c`、`pid=0125`、`at_interface=2`，
  修改前配置保存在目标 `/tmp/vohive-config.before-usb`；未修改通用默认值。
- 服务接入后 `connected=true`，SIM READY，CHN-CT / LTE，采样 RSSI 19（61%）。
  查询 `qcautoconnect=0`，`/api/data` 为
  `{"enabled":false,"guard_state":"off","sms_only":true}`，本次未触发模组重启。
- 真实读取模组已有短信并归档 5 条，其中日志显示包含两段长短信；不在验收文档记录
  电话号码、ICCID 或正文。此项证明既有消息读取，尚不等价于新到短信即时接收验收。
- 真实停止服务后，AT interface 2 的驱动从 usbfs 归还 option；重新启动后认领为 usbfs，
  SIM 恢复 READY、数据关闭核验通过，5 条归档逐项与重启前一致，没有重复归档。
  QMI interface 4 仍由 qmi_wwan 管理，未被本服务解绑。
- Bark / Telegram 仍未配置。未发送任何出站短信，未操作 Mac 旧通知配置。
- 本轮验证的是服务重启与真实 USB 接口释放/重认领，尚未执行物理拔插测试。

## 剩余真实硬件/渠道验收

- 已明确提醒用户把带 SIM 的模组插到 Radxa E20C/iStoreOS USB 口。
- 模组、AT 接口、SIM 与移动数据关闭已实测；待物理 USB 断连/重插恢复。
- 已读取并归档模组原有短信；待新到入站短信即时接收与 Bark/Telegram 实际送达。
- 待用户确认号码与操作后的真实出站短信及 Telegram 预览/确认/取消流程。

## 2026-10-17 通知配置迁移：通过服务端测试

- 用户明确同意迁移 Mac 原 Bark/TG 配置并停用旧服务；本次只迁移通知配置，
  Mac 历史 SQLite 归档保留原位，未覆盖 iStoreOS 已有归档。
- 原 LaunchAgent 与通知配置已备份在 Mac 的
  `~/Library/Application Support/VoHiveMac/migration-backup-<时间戳>/`（目录 0700，备份文件 0600）。
- `com.yanbc.vohive-mac` 已 launchctl disable 并 bootout。卸载短暂异步完成后，
  已复核服务不再注册、disabled 状态生效、本地 7676 监听消失。
- 原通知配置通过 SSH 标准输入传输，目标原子安装为
  `/etc/vohive-sms-forward/data/notify.json`，root:root、0600；未在命令参数、日志、Git 中放入密钥。
- iStoreOS 重启加载后 Bark 与 Telegram 均 configured/active；移动数据仍核验为 off。
- 通过应用 `/api/notifications/test` 分别测试 Bark 和 Telegram，均 HTTP 200 / `{"ok":true}`。
  这证明渠道接口接受测试通知；手机端实际显示仍需用户确认。
- 待用户在指定 Telegram 私聊发送 `/help` 验证轮询指令；未代用户发送真实短信。


目标 IPK 构建、安装与无模组运行验证已通过；USB 实测和真实渠道送达尚未完成，
活动目标继续保留，未将软件测试通过替代端到端验收。

## 2026-10-17 新到短信与第二个 Mac 实例排查

- 用户发送测试短信后，iStoreOS 归档由 5 条增加至 6 条，新消息时间 13:09:00，
  实际归档时间 13:09:08；仅记录元数据，不记录正文和号码。
- 该消息早于通知配置迁移启用，数据库 `notification_deliveries` 无记录，符合历史
  短信不补发设计；因此这条消息证明真实入站/归档，但不证明短信自动转发。
- 发现 Telegram 持续重试。只读 `getWebhookInfo` 确认无 Webhook；暂停目标服务后
  单独请求长轮询仍收到 HTTP 409，证实存在另一轮询客户端，而非 USB 收信故障。
- Mac 复核发现另一个 `com.yanbc.vohive-mac-session` 实例（开发目录二进制），
  与先前已停用的 `com.yanbc.vohive-mac` 不同。按用户停用旧服务的授权，已将该
  session 服务 disable/bootout，并核验本地 7676 监听消失。
- 初次迁移后端口释放的检查只覆盖当时状态；第二个服务随后启动，不能据初次检查
  断言不存在后续冲突。后续需以 TG 指令回复和新短信渠道投递状态验证最终结果。
- 停用第二个 Mac 服务后，单实例 Telegram 25 秒长轮询实测成功（HTTP 成功、
  `ok=true`、耗时 26.5 秒、无更新），随后已恢复 iStoreOS 短信服务；
  双渠道 active，数据 guard off。之前 409 的复现条件已消除。

## Telegram 实际指令回复验收：帮助流程通过

- 只读目标数据库确认 1 条 Telegram 指令记录为 `completed`、`reply_sent=1`，
  回复包含固定帮助命令 `/send`、`/confirm`、`/cancel`；发送完成时间戳 1792213968。
- 这证明目标已接收到指定聊天指令并成功向 Telegram API 发送帮助回复；
  未代替用户执行 `/send` 或 `/confirm`，手机端显示仍由用户核对。
- 截至此次检查，入站仍 6 条、出站 0 条、短信通知投递队列为空；
  待通知启用后的新入站短信验证自动双渠道转发。

## 新短信自动双渠道转发：服务端验收通过

- 后续只读复核发现入站归档由 6 条增加为 7 条，出站仍为 0。
- `notification_deliveries` 有且仅有新消息的 Bark、Telegram 两条记录，
  两者均为 `done`；这证明新归档短信实际自动触发两个渠道且服务端接受投递。
- `/api/data` 同时保持 `enabled=false`、`guard_state=off`、`sms_only=true`。
- 尚待手机端显示确认、用户自行预览/确认后的真实出站短信，以及物理拔插恢复。

## Telegram 真实出站短信：通过

- 用户确认“1完成”（TG 真实发送并确认手机收到）。
- 目标只读核验：2 条出站归档，时间分别为 2026-10-17 13:14:10、13:15:02；
  Telegram 有 2 条预览记录和 2 条 `sent` 确认记录，全部 `reply_sent=1`，当前草稿数 0。
- 未记录收件号码和正文；操作由用户在 TG 完成，未由自动验收代为发送。
- 同时核验数据 guard 仍为 off。剩余物理 USB 拔插恢复及此前通知手机端显示确认。

## 2026-10-17 物理 USB 拔插恢复：通过

- 用户确认已重插，USB 路径仍为 4-1，设备号由 2 变为 3，证实发生真实重新枚举。
- 未重启服务：初次查询时 AT 驱动为 option，随后现有服务自动重新认领为 usbfs；
  模组恢复 connected、SIM READY、CHN-CT/LTE，采样信号 77%。
- 重插后数据保护曾短暂显示 error（不误报 off）；在观察窗口内现有 30 秒复核任务
  自行恢复 `enabled=false` / `guard_state=off` / `sms_only=true`，没有人工重启掩盖恢复。
- QMI interface 保持 qmi_wwan，短信归档仍入站 7 条、出站 2 条；Bark/TG 原投递
  均 done，无归档丢失。两渠道仍 active，服务 running。
- 硬件接入、真实收发、Telegram 预览/确认及物理重插已取得证据；
  最后待用户确认手机端 Bark/TG 短信通知实际显示，再做最终完成审计。

## 最终验收结论（2026-10-17）

用户已确认 Bark 和 Telegram 手机端均收到新短信转发。结合此前真实发送接收确认，
本目标的代码迁移、软件测试、目标构建安装、真实收发/双渠道通知/TG 操控与拔插恢复完成。
上文“待验收”内容为按时间保留的历史阶段记录，以本节及后续实测结论为准。

最终只读审计再次确认：
- 已部署二进制 SHA-256 与最终构建产物一致。
- 服务启用自启动；模组 connected、SIM READY，短信专用 guard off。
- 两个通知渠道 active，真实消息双渠道投递均 done。
- 两条真实出站归档和对应 Telegram sent/reply_sent 记录存在。
- SQLite quick_check 为 ok，物理重插后入站 7 条、出站 2 条保留。
- 最终源码 ARM64 race 测试、vet、打包测试及 release 构建日志已复核；
  未把尚未执行的 SDK recipe 或尚未运行的新增 GitHub CI 声称为通过。
- Mac 两个旧轮询服务均已停用并禁用，原配置有私有备份。

交付范围为已测试的 Radxa E20C / iStoreOS 24.10.6 / QDC507 短信专用方案；
其他固件、架构、USB 标识和模块仍需按部署指南核验。代码修改保留在工作区，未自动提交或推送。

## 2026-10-17 心跳版 0.2.0-1：iStoreOS 发送端完成

本轮新增模块只读自检、HTTPS 正常心跳、配置/状态网页。ECS 接收和 Bark 超时报警
按用户要求移至独立仓库，本次不将它们混入主项目，不宣称 ECS 已上线。

- 独立审查指出并补测修复：归档数据库写失败传播、检查完成时归档新鲜度重核验、
  重试截止于当前周期；复审无重要遗留问题。
- macOS 全套 Go race、普通 vet 通过；ARM64 最终源码全套 race、vet -race、
  3 项 Python 打包测试通过；新增 TLS 集成、公开签名向量、配置权限/密钥保留、
  网页运行和重配取消测试。原生 vet 用 race 模式复用 CGO，非声称普通 native vet。
- 设备原有临时构建缓存随重启丢失，已复用现有容器层重建缓存；早期预热轮被主动停止，
  不将其未完成 vet/release 视作成功。完整最终轮 exit 0 后才部署。
- 已安装版本 0.2.0-1，二进制 SHA256
  `14b42dad1b265a38fce85d33d840064136736a7cba204940fdc43c43fa7792be`。
- IPK（3,610,743 字节）SHA256
  `636efd0d3b63c01267f1f3deeef116aeb9eb74d21e5ee77e9e1d7765477dd6bd`。
- 升级前停止服务并完成一致 SQLite/二进制/UCI/通知配置备份，目标位置
  `/etc/vohive-sms-forward/upgrade-backups/heartbeat-20261017-135416/`，私有目录/文件。
- opkg 正常升级；用户定制 UCI 保留，默认新配置另存 `vohive-sms-forward-opkg`，
  这是配置保留提示，不是覆盖用户设备标识；实际仍为 2c7c:0125 / AT interface 2。
- 真实自检七项均 true：AT、SIM、注册、数据关闭、短信归档、数据库及时限。
  入站7/出站2保留、notify.json hash不变、network/firewall hash不变、Bark/TG仍active。
- 真实目标新API：跨源写入403、无效设置400、上报禁用时发送503、手动自检200且正常；
  网络功能仍404/403。保存默认禁用配置后文件root/0600，重启保持600秒/禁用和自检正常。
- 在目标主机运行 opt-in TLS 测试：读取正在运行服务的真实健康结果，签名发到临时回环
  HTTPS 接收器并获得确认；将报告设异常后被门禁阻断。测试不修改系统 CA、生产接收地址，
  不发短信，不对真实 ECS 发请求。
- 自启动保持启用。交付位于 `dist/istoreos-0.2.0-heartbeat-aarch64/`，旧版本产物保留。

当前上报明确为关闭，等待 ECS 独立部署后在网页配置实际 HTTPS 地址及共享密钥。
10分钟发送调度已用可控时钟边界测试，ECS真实20分钟超时报警不在本轮完成范围。
