# 短信专用模式与 Bark／Telegram 配置

iStoreOS 使用固定短信专用服务，先按 [iStoreOS 部署指南](openwrt.md) 安装，
通过 SSH 隧道访问网页后，从本文第 2 节配置 Bark／Telegram。本文第 1 节的
USB 模式切换是原 macOS 专用说明，iStoreOS 不要求切换 QMI 或创建模组网络接口。

本方案不需要虚拟机。模组仅保持蜂窝注册和短信能力，蜂窝数据保持关闭；
Bark 与 Telegram 的 HTTPS 连接由 Mac 自己的 Wi‑Fi／有线网络发起。

## 1. 先备份与核验

停止 vohive-mac 服务，备份 `vohive.db`（包括 WAL／SHM）、现有二进制和
LaunchAgent plist。AT 接口同一时刻只应由一个进程使用。用匹配实际设备的
`BAIWANG_VID`、`BAIWANG_PID` 和必要时的 `BAIWANG_IFACE` 执行：

```sh
./vohive-mac at 'AT+QCFG="qcautoconnect"' 'AT+QCFG="usbnet"' 'AT+CPIN?' 'AT+COPS?'
```

确认 `qcautoconnect` 为 `0`。如为 `1`，先设置为 `0` 并重启模组：

```sh
./vohive-mac at 'AT+QCFG="qcautoconnect",0' 'AT+CFUN=1,1'
```

需要从 ECM 切到 QMI 时，再执行以下持久化模式切换，模组会重启并重新枚举：

```sh
./vohive-mac at 'AT+QCFG="usbnet",0' 'AT+CFUN=1,1'
```

等待设备重新出现，复查上述只读 AT 状态；`AT+CPIN?` 应为 `READY`，
`AT+COPS?` 应显示已注册，`qcautoconnect` 仍为 `0`。Mac 默认路由应是 Wi‑Fi／
有线网，不应出现由此模组提供的活动 ECM 网络接口。

## 2. 本机网页填写通知配置

先通过 BotFather 创建 Bot，在 Telegram 与它的**私人聊天**中发送 `/start`，取得该
私人聊天的数字 `chat_id`。Bot 不应设置 Webhook，否则 `getUpdates` 会遇到 409 冲突。
Bark 服务地址填写 HTTPS 根地址（不含 `/push`），device key **只填纯 key**，
不要粘贴完整推送 URL 或 `key/正文` 路径。

启动服务后打开 `http://127.0.0.1:7676/`，在“短信通知设置”中分别填写 Bark
或 Telegram 卡片，并点击各自的“保存并立即启用”。只配置其中一个渠道也可独立运行。
配置原子写入 `<dataDir>/notify.json`，权限 `0600`；再次保存只需填写要修改的那张卡，
不会要求重填另一渠道。页面不会回显 Bot Token／Bark key，也不把它们放进浏览器存储、
URL、日志或 SQLite。不要把凭据粘贴到聊天或提交进 Git。使用 `sudo` 启动时，文件
所有者为 `SUDO_UID` 对应的登录用户；真实 root 启动则归 root。

可选高级用法：`-notify-config /absolute/private/path/notify.json` 覆盖默认文件位置；
文件仍由网页保存，或自行提供精确 `0600` 的单渠道／双渠道 JSON。旧版完整四项文件
继续可用；若仅某一渠道字段无效，另一渠道仍运行并在网页分别显示状态。文件权限或
JSON 结构无效时，网页短信功能照常可用，并显示配置错误以便修复。

## 3. 启动与验收

```sh
./vohive-mac -addr 127.0.0.1:7676 -data ./data \
  -archive-delete=false -sms-only
```

先查看 `GET http://127.0.0.1:7676/api/data`，只在 `guard_state` 为 `off`
时视为已核验；`pending`／`error` 表示需要排查，**不是**数据已关闭。
检查 `/api/status` 与本地短信列表，确认蜂窝注册和收件能力。网页保存后无需重启，
按已配置渠道分别点击“测试 Bark”和“测试 Telegram”并在设备／私人聊天核对送达；测试只推送固定
测试通知，不调用模组发送短信。配置生效后新归档的短信只进入已启用渠道的
投递队列；历史短信不会补发。单一路径保存、测试或投递失败不影响另一条，网络恢复后自动重试。
更换通知目标时，旧目标尚未投递的任务以及 Telegram 旧草稿／回复／偏移会清理，
不会把旧短信或命令发到新目标。

Telegram 仅接受指定 `chat_id`、类型为 `private` 且发件人 ID 相同的命令：

```text
/send +123456789 内容
/confirm
/cancel
/help
```

`/send` 返回号码、正文、预计分段和 2 分钟有效期；`/confirm` 才调用模组。
如果发送结果不明确，回复会要求人工核对，不会自动重发短信。
真实端到端发送验收由本人在私人聊天中发出 `/send` 并亲自 `/confirm`。
不要用他人的号码或真实短信作为自动化测试目标。

停止服务时保留 SQLite 和通知配置的备份。若切回 ECM 上网模式，先确认
不再运行 `-sms-only`，然后按 [模块安装手册](dongle-setup.md) 的 ECM 步骤操作。
