# VoHive 正常心跳协议 v1（跨仓库契约）

本仓库实现发送端。ECS 接收、超时判定和 Bark 报警在独立仓库实现；此文不表示 ECS 已上线。

## 请求

`POST https://接收域名/v1/heartbeat`，Content-Type: application/json。
只使用 HTTPS，验证系统 CA，不跟随重定向。正文不超过 4096 字节，无查询参数。

```json
{"version":1,"device_id":"router-1","name":"router","interval_seconds":600,"checked_at":"2026-10-17T00:00:00Z","healthy":true,"checks":{"archive":true,"at":true,"data_off":true,"database":true,"registration":true,"sim":true,"timely":true}}
```

- device_id：1—64 个 ASCII 字母数字、下划线或连字符，首字符必须字母或数字，预注册。
- name：非空展示名称，最多 160 UTF-8 字节、无控制字符，不含 SIM/手机身份信息。
- interval_seconds：整数，60—86400，默认 600，必须和接收端注册周期一致。
- checked_at：本轮检查完成时间，RFC3339（可含小数和时区偏移）。
- healthy：必须为 true；checks 中以上七项必须全部为 true。
- archive：最后一次成功同步不超过 150 秒，且没有当前同步错误（包括归档写入失败）。
- 不上传手机号、ICCID、IMEI、短信正文、Bark key 或 Telegram Token。

## 鉴权头

| HTTP 头 | 内容 |
|---|---|
| X-VoHive-Device | 与正文相同的设备 ID |
| X-VoHive-Timestamp | 签发时 Unix 秒时间戳的十进制字符串 |
| X-VoHive-Nonce | 16 个随机字节的小写 hex（32 字符） |
| X-VoHive-Signature | HMAC-SHA256 结果的小写 hex（64 字符） |

每设备共享密钥是 32 个随机字节，配置中以 64 个 hex 字符保存；**HMAC 使用 hex 解码后的原始字节**。
签名输入为下列六项用单个 LF (`\n`) 连接，末尾没有换行：

```text
POST
/v1/heartbeat
<device_id>
<timestamp>
<nonce>
<SHA256(raw_request_body) 的小写 hex>
```

JSON 不重编码：验签必须用实际收到的原始正文。HMAC 常量时间比较。
服务器拒绝未知设备、时间偏差超过 120 秒、无效签名、无效正文及异常检查结果。
服务器需核对 checked_at 与签发时间相差不超过 150 秒，且不晚于签发秒边界后 2 秒。

## 应答与去重

成功（包括幂等重复）为 HTTP 200，完整 JSON 仅包含：

```json
{"ok":true}
```

其他 HTTP 状态、重定向、错误/未知字段、尾随 JSON、超过 4 KiB 响应均视为发送失败。
服务器应先持久化再确认。认证/格式错误不得刷新最后正常接收时间。

同一周期最多初始请求加 3 次重试，退避 5/15/30 秒，受本周期截止时间、请求 10 秒超时、
正常状态及原始签发时间 120 秒有效期共同限制。每次重试前重新检查健康，复用原始正文、
时间戳、nonce；若条件失效则停止，不排队补发历史心跳。
服务器保存 nonce 至少 5 分钟，重复合法请求返回相同成功结果，**不更新最后正常接收时间**。
此规则避免丢失成功响应后的重试人为延长在线时间。

## 签名测试向量（公开测试值，不是部署密钥）

原始正文：请求示例单行 JSON，不含尾随换行。

```text
key hex: 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
timestamp: 1792195200
nonce: 000102030405060708090a0b0c0d0e0f
body SHA256: 2d24ac1f448d2891ec06547d5dde19fad47bd4aea2f04c3526b38e530c65d33e
signature: 2f912cd50efa01093d15890497175019376a4df0e3de1f3fa538878c7604af9b
```

对应回归测试 `TestPublishedSigningVector`。该时间戳只用于算法测试，在线请求会因过期被拒绝。

## ECS 独立仓库应实现的监控规则

服务器以实际接收时间计时，不信任设备请求改变服务器监控周期。设备启用即开始计时，
无首包也报警。默认最后正常时间后 1200 秒超时，每 5 秒检测，失败 Bark 通知重试，
同故障去重，恢复通知一次；持久化状态、事件与nonce，重启不重置超时宽限。
详细状态机及 Alpine/OpenRC 要求见 [设计](superpowers/specs/2026-10-17-heartbeat-monitor-design.md)。
