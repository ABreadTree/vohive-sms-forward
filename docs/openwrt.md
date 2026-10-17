# iStoreOS 短信专用版：构建、部署与验收

本项目优先适配 iStoreOS，模组只负责收发短信，不作为上网卡。
Bark、Telegram 推送及 Telegram 长轮询使用 iStoreOS 原有的互联网连接。
Linux 使用原始 USB AT 通道，不要求模组具有可用的 ECM/QMI 网络接口。

## 短信专用范围

- 保留：SIM/信号状态、PIN 管理、短信收发、长短信重组、SQLite 归档。
- 保留：Bark / Telegram 独立配置、投递失败重试、待投递消息重启恢复。
- Telegram 指定私聊提供 `/send 手机号 内容`、`/confirm`、`/cancel`、`/help`；
  `/send` 只预览，两分钟内 `/confirm` 才发送。Bark 是推送渠道，不接受控制命令。
- iStoreOS 服务固定带 `-sms-only=true`，不提供 UCI 上网模式开关。
  旧配置中的 `sms_only=0` 和 `net_interface` 均不再生效。
- **不启动**网络接口发现/流量采样、低数据模式协调器或 ECM/DHCP watchdog。
  网页隐藏 WAN IP、流量图、数据开关和低数据模式，且不轮询流量/低数据 API。
- `/api/data` 只读展示 `guard_state`；所有手动写入返回 403，避免从网页/API
  重启模组。`/api/traffic`、`/api/metered` 在短信专用模式返回 404 `sms_only`。
- 不配置拨号、WAN、路由、DNS 或防火墙，不改变 iStoreOS 原有网络出口。
  不自动切换 USB composition，也不加载/卸载全局串口驱动。
- 只认领指定的 USB AT 接口。Linux 在取得 USB 配置后启用 libusb 自动解绑，
  释放接口时重新附着其驱动，避免解绑其他 USB 网络接口。
- 仅沿用已有百旺/Quectel 风格 AT 命令，特别是 `qcautoconnect` 数据关闭核验。
  VID/PID 可配置并不代表任意模组已兼容。暂未增加串口后端或 LuCI 页面。

macOS 历史完整模式代码保留，但不进入 iStoreOS 短信服务的网络运行路径。
本项目不依赖 VoCat，也不引入其 VoWiFi、eSIM、QMI 拨号等子系统。

主机回归、短信专用启动/API/网页运行测试和 procd 边界测试覆盖软件逻辑。
已完成 Radxa E20C / iStoreOS 24.10.6 的原生 ARM64/musl IPK 构建、安装、
procd 启停与无模组接口验证，产物位于 `dist/istoreos-24.10.6-aarch64/`。
**QDC507 实机收发、Bark/TG 手机端送达、TG 预览确认及 USB 拔插恢复已验收，SDK recipe 尚未实际执行**；
`.github/workflows/test.yml` 定义了 Linux CI，新增文件不代表 CI 已运行通过。

## 先确认你的设备

### 已通过 SSH 确认的首要目标（2026-10-16）

| 项目 | 结果 |
|---|---|
| 硬件 | Radxa E20C（`radxa,e20c`） |
| 系统 | iStoreOS 24.10.6，修订 `2026041710` |
| 内核 | 6.6.127 |
| 固件 target/subtarget | `rockchip/armv8` |
| CPU / 软件包架构 | `aarch64` / `aarch64_generic` |
| 包管理器 | `opkg`，目标安装包为 `.ipk` |

此次只读检查时，USB 列表仅有控制器/集线器，未检测到短信模组，亦无
`ttyUSB`/`ttyACM` 节点。进一步用不带精确包名的列表查询确认：运行库已登记为
`libusb-1.0-0 1.0.27-r1`（实际包名带 ABI 后缀 `-0`），并存在
`/usr/lib/libusb-1.0.so.0.4.0` 及其链接；先前精确查询 `libusb-1.0` 没有匹配到此包。
设备使用 musl（`/lib/ld-musl-aarch64.so.1`），另接受 `aarch64_cortex-a53` 架构的软件包。
构建使用匹配上述固件的 SDK，首要输出 `aarch64_generic` 的 ARM64/musl `.ipk`；
安装时再核对软件包依赖元数据与现有固件文件，避免盲目覆盖运行库。
2026-10-17 已接入并验收 QDC507（`2c7c:0125`，AT interface 2）；目标 UCI 已按实机调整，
通用默认标识不替代设备核对。详见 [验收记录](istoreos-verification.md)。

设备已有 ARM64 Docker，可在隔离的 ARM64/musl Go 容器中原生编译、运行 Linux
测试后打包。此路径不等同于 SDK 构建，但生成物仍需通过目标设备动态链接、
opkg 安装及服务启停验收。项目提供不联网、不安装软件的打包器：

```sh
python3 -m unittest discover -s openwrt -p 'test_*.py' -v
python3 openwrt/build_ipk.py --binary /absolute/path/to/arm64-musl/vohive-sms-forward --output dist
```

打包器验证 ELF64/AArch64 与 musl 解释器，使用 OpenWrt gzip/tar IPK 格式，
固定 root 文件属主和敏感目录权限，并生成 SHA-256。不会把 libusb 静态链接或
覆盖到包内；依赖目标固件的 `libusb-1.0-0`。首次安装不自动启动短信业务。

### 其他设备或固件升级后重新核对

在 iStoreOS SSH 终端运行，分享结果时可删去序列号和 MAC 地址：

```sh
ubus call system board
uname -m
cat /etc/openwrt_release
ls -l /dev/ttyUSB* /dev/ttyACM* 2>/dev/null
command -v opkg
command -v apk
```

iStoreOS 不同版本的软件包格式、CPU 架构、musl 和内核模块版本可能不同，
以实际输出选择同一版本及 target/subtarget 的 SDK。不要把 x86_64 当作默认，
也不要使用另一个固件的 kmod 包。本项目原始 USB AT 通道本身不要求
`kmod-usb-serial-option`，也不要求安装 QMI 拨号工具或给模组建立网络接口。

## 用匹配的 SDK 构建本地源代码

在 Linux 构建机准备与目标 iStoreOS 匹配的 SDK，以及相应版本的 packages feed。
Go 主机工具链至少为 `go.mod` 声明的 1.24.4；SDK 内 Go 版本过旧时，先更新
该 SDK 可兼容的 golang host 工具链，而不是删除或下调项目的 Go 版本声明。

以下路径均替换为实际绝对路径：

```sh
export SDK=/absolute/path/to/matching-sdk
export SOURCE=/absolute/path/to/vohive-sms-forward
cd "$SDK"
./scripts/feeds update packages
./scripts/feeds install golang libusb-1.0 ca-bundle
mkdir -p package/vohive-sms-forward
cp -R "$SOURCE/openwrt/vohive-sms-forward/." package/vohive-sms-forward/
make menuconfig
# 在 Network 选择 vohive-sms-forward；SDK 架构必须与目标设备一致。
make package/vohive-sms-forward/compile V=s VOHIVE_SOURCE_DIR="$SOURCE"
find bin -type f \( -name '*vohive-sms-forward*.ipk' -o -name '*vohive-sms-forward*.apk' \)
```

Makefile 将指定本地源码复制到 SDK 构建目录，包括尚未提交的适配改动，
不从 GitHub 下载旧版本替代。源码变化后先执行
`make package/vohive-sms-forward/clean`，再重新 compile。
`VOHIVE_SOURCE_DIR` 路径请避免空格（OpenWrt make 构建系统通用限制）。

保留 `CGO_ENABLED=1`：gousb 依赖 libusb，go-sqlite3 需要 C 编译器。
仅设置 `GOOS=linux GOARCH=...` 或关闭 CGO 并不是可用的完整交叉编译方式。
SDK 使用目标 musl 工具链，并为目标固件生成软件包；libusb 动态链接，
SQLite 使用驱动内置实现，并关闭运行时加载 SQLite 扩展。

## 安装与启动

先安装生成的软件包及其固件源内依赖；根据设备实际包管理器使用
`opkg install /tmp/vohive-sms-forward_....ipk` 或
`apk add /tmp/vohive-sms-forward-....apk`。包名中的版本和架构以实际输出为准。
包默认不运行应用（UCI `enabled=0`），不会在安装时向模组发送 AT 命令。

**短信专用服务会主动关闭兼容模组的数据，并可能重启模组。**
此服务用于独立短信模组，而不是承载 WAN 的模组。它保持蜂窝注册以收发短信，
不等于开启飞行模式。`pending`/`error` 表示尚未核验关闭，网页会明确显示；
Telegram `/confirm` 会再次核验，未核验关闭时不发送。

```sh
uci set vohive-sms-forward.main.vid='2ca3'
uci set vohive-sms-forward.main.pid='4006'
uci set vohive-sms-forward.main.at_interface='3'
uci set vohive-sms-forward.main.archive_delete='0'
uci set vohive-sms-forward.main.enabled='1'
uci commit vohive-sms-forward
/etc/init.d/vohive-sms-forward enable
/etc/init.d/vohive-sms-forward start
logread -e vohive
```

procd 负责后台运行、日志、有限次数重启和正常停止。修改 UCI 后执行
`/etc/init.d/vohive-sms-forward restart`。服务以 root 运行访问 USB；通知配置
由应用原子写入 `0600` 文件，不放入 UCI，也不会通过进程参数传递 Token。

初次默认 `archive_delete=0`，保留硬件短信方便核对。确认归档和转发可靠后，可设置
`archive_delete=1` 并重启服务，防止长期运行时模组/SIM 的有限短信槽位被占满。

默认数据目录 `/etc/vohive-sms-forward/data`（新建时权限 `0700`）。SQLite
数据库、通知配置及待投递状态应放在可靠持久存储，避免 `/tmp` 重启丢失。
长期运行推荐使用已挂载的数据盘上的专用目录，以减少内部闪存写入；启动服务前
先确认该盘已挂载，防止文件写进未挂载的同名目录：

```sh
uci set vohive-sms-forward.main.data_dir='/mnt/YOUR_DISK/vohive-sms-forward'
uci commit vohive-sms-forward
```

自定义数据盘目录需自行纳入备份；软件包声明保留 `/etc/config/vohive-sms-forward`
和 `/etc/vohive-sms-forward/`，不保证外部挂载目录被固件备份工具收录。
迁移已有数据库时先停止原程序，将完整数据目录复制到新位置，再启动新服务。
不要在 SQLite 运行时只复制主 `.db` 而遗漏 WAL；通知配置复制后检查为 root 所有、
`0600`。不要同时运行本服务和 VoCat、ModemManager、其他 AT 轮询程序占用同一端口。

## 网页与验收

服务仍仅监听 `127.0.0.1:7676`。从电脑建立 SSH 隧道（HOST 为 iStoreOS 地址）：

```sh
ssh -N -L 7676:127.0.0.1:7676 root@HOST
```

浏览器访问 `http://127.0.0.1:7676/`，不要换成 `localhost`，因为通知设置
沿用现有的 Host/Origin 校验。首版不开放 LAN 无鉴权访问，不创建端口转发。

验收顺序：

1. 查看日志与 `/api/status`，确认目标模组和 SIM；不以网页启动代表 USB 已连接。
2. 验证 `/api/data` 的 `sms_only` 和 `guard_state`，短信专用模式须为 `off`。
3. 从其他设备给 SIM 发一条短信，确认归档完整。初次验收保留
   `archive_delete=0`，核对后再自行启用硬件归档后删除。
4. 配置 Bark/Telegram，先用网页渠道测试，再确认新短信转发与服务重启后的状态。
5. 在 Telegram 指定私聊发送 `/send 手机号 内容`，核对预览后自行 `/confirm`；
   或 `/cancel` 放弃。真实短信可能产生费用，自动化测试不发送真实短信。
6. 验证 USB 拔插、正常停止和重启恢复；无 ECM/QMI 网络接口时仍应能收发短信。
7. 检查网页只有短信/通知/SIM 相关功能，没有流量图和联网开关；原有 WAN/LAN 保持不变。

回滚：先 `stop` 和 `disable` 服务，备份数据再卸载软件包。短信专用核验写入
模组 NV 的设置不因卸载应用自动恢复；需要恢复蜂窝数据时按原模组手册操作。

## 参考项目

- [VoCat](https://github.com/MengMengCode/VoCat)：参考其 Linux 串口/USB 发现及
  自包含服务的集成思路，不引入其 VoWiFi/eSIM 子系统或源码。
- [luci-app-vocat](https://github.com/JGG-SH/luci-app-vocat)：参考 UCI、procd
  与核心程序分离的管理方式；本项目首版仅提供核心包，不声称已提供 LuCI 页面。
- [iStoreOS](https://github.com/istoreos/istoreos)：目标固件、版本与硬件信息。
- [OpenWrt procd](https://openwrt.org/docs/guide-developer/procd-init-scripts)
  和 [golang-package.mk](https://github.com/openwrt/packages/blob/openwrt-24.10/lang/golang/golang-package.mk)：
  服务管理与 CGO SDK 构建接口。

本次适配独立实现，保留本仓库及原项目 MIT 版权声明。

## 模组自检与心跳上报

新增每分钟只读自检及可配置的 HTTPS 正常心跳，默认上报关闭、周期10分钟。
仅健康时上报，支持独立密钥签名、重试和网页状态；ECS接收与Bark超时报警另建仓库。
配置见 [心跳指南](heartbeat-setup.md)，对接见 [协议](heartbeat-protocol.md)。
