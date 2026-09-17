# 飞连短信事件转发通用短信网关 — 交付文档

| 项目 | 内容 |
|---|---|
| 交付文档版本 | v1.0 |
| 软件版本 | v1.0.0 |
| 交付日期 | 2026-09-14 |
| 适用对象 | 客户侧飞连/IT 运维、实施人员、验收人员、二次开发人员 |
| 运行环境 | Ubuntu 20.04+（x86_64 主目标，arm64 附带）；单二进制 + systemd，headless 无图形界面 |
| 需求规格 | 飞连短信事件转发通用短信网关（功能需求 FR 与验收标准 AC-1~AC-17，摘要见 [项目概述](./00-项目概述.md)） |
| 密级建议 | 内部公开（文档不含任何真实凭证；生产密钥由部署现场生成，不随交付物分发） |

---

## 1. 交付文档总索引

| 序号 | 文档 | 说明 | 读者 |
|---|---|---|---|
| 00 | [项目概述](./00-项目概述.md) | 背景、目标、范围、术语、干系人 | 全部 |
| 01 | [系统架构设计文档](./01-系统架构设计.md) | 总体架构、分层、数据流、关键设计决策、安全与可靠性设计 | 架构/开发/运维 |
| 02 | [功能模块说明文档](./02-功能模块说明文档.md) | 各模块职责、配置模型、字段映射引擎、签名、幂等、回执、WebUI | 开发/实施 |
| 03 | [API 接口文档](./03-API接口文档.md) | 飞连 Webhook、厂商回执、管理 API（/api）全部端点与报文样例 | 开发/联调/验收 |
| 04 | [数据库设计文档](./04-数据库设计文档.md) | SQLite 表结构、字段、索引、迁移、密钥存储与备份 | 开发/运维/DBA |
| 05 | [环境部署说明文档](./05-环境部署说明文档.md) | 软硬件要求、构建、安装、systemd、配置项、升级与卸载、联调清单 | 运维/实施 |
| 06 | [用户操作手册](./06-用户操作手册.md) | WebUI 四视图逐项操作指引（系统设置/通道/绑定/记录） | 业务运维 |
| 07 | [测试报告](./07-测试报告.md) | 单元测试、竞态检测、覆盖率、静态检查、交叉编译、全链路冒烟结果与原始记录 | 验收/质量 |

---

## 2. 交付物清单

### 2.1 源代码（Go，module：`feilian-sms`，Go 1.26.3）

| 路径 | 说明 |
|---|---|
| `cmd/sms-gateway/main.go` | 主服务进程入口（引导→装配→信号驱动优雅退出） |
| `cmd/mockvendor/` | 可配置厂商桩（联调/冒烟用，非生产组件） |
| `internal/bootstrap/` | 启动引导配置（YAML + 环境变量）、数据密钥解析 |
| `internal/feilian/` | 飞连 Webhook 入站协议：信封解析、challenge、AES-256-CBC 兼容、9 种 sms_type |
| `internal/channel/` | 通用 HTTP 短信通道运行时：配置、变量、HTTP 客户端、示例厂商预置 |
| `internal/channel/mapping/` | 字段映射引擎（点分路径组装嵌套 JSON） |
| `internal/channel/sign/` | 签名策略：none / sha1_salt / hmac_sha256 |
| `internal/store/` | SQLite 持久化、配置快照热加载、AES-256-GCM 信封加密、发送状态机 |
| `internal/service/` | 编排层：转发幂等、回执、测试发送、设置运行时 |
| `internal/httpapi/` | HTTP 装配：Webhook、回执、/health、管理 API |
| `internal/logging/` | Zap 日志构造与统一出口脱敏 core |
| `internal/webui/` | 内嵌静态资源处理器 |
| `web/` | 前端资源（原生 HTML/JS/CSS，`go:embed` 内嵌，零 npm/CDN） |
| `go.mod` / `go.sum` | 依赖清单与校验和 |

规模：Go 生产源码 49 个文件 / 5,368 行；Go 测试 38 个文件 / 6,107 行；前端 12 个文件 / 2,033 行。全部 Go 源文件单文件不超过 500 行（最大 `internal/httpapi/channels_api.go` 466 行）。

### 2.2 配置与部署文件

| 路径 | 说明 |
|---|---|
| `deploy/config.example.yaml` | 引导配置样例（仅启动级项；业务配置全部在 WebUI/SQLite） |
| `deploy/sms-gateway.service` | systemd 服务单元（最小权限加固、journald、EnvironmentFile） |
| `deploy/install.sh` | 一键幂等安装脚本（建用户/目录、生成密钥、装二进制与 unit、enable --now） |
| `deploy/smoke/run.sh` | 全链路冒烟脚本（10 步，全新临时目录自验） |
| `Makefile` | vet / test / race / build-local / build-linux-amd64 / build-linux-arm64 / smoke |
| `.gitignore` | 已排除 `*.db*`、`data.key`、`bin/` 等敏感与产物项 |

### 2.3 协议样例与测试夹具

| 路径 | 说明 |
|---|---|
| `testdata/feilian/url_verification.json` | 飞连 URL 验证握手样例 |
| `testdata/feilian/event_sms_code.json` | 飞连登录验证码事件样例 |
| `testdata/feilian/event_sms_guest_wifi.json` | 飞连访客 Wi-Fi 事件样例（未绑定降级演示） |
| `testdata/provider/response_success.json` | 示例厂商同步成功响应样例 |
| `testdata/provider/response_business_error.json` | 示例厂商业务失败响应样例 |
| `testdata/provider/receipt_delivered.json` | 示例厂商送达回执样例 |
| `testdata/provider/receipt_failed.json` | 示例厂商送达失败回执样例 |
| `testdata/GenSignVectors.java` | 示例厂商签名 JDK 双端向量生成参考程序 |

### 2.4 可执行产物（现场构建，或由交付方提供）

| 产物 | 目标 | 形态 |
|---|---|---|
| `bin/sms-gateway-linux-amd64` | Ubuntu x86_64 | 静态链接 ELF，CGO-free |
| `bin/sms-gateway-linux-arm64` | Ubuntu arm64 | 静态链接 ELF，CGO-free |
| `bin/sms-gateway` | 本机（构建机） | 本地架构可执行文件 |
| `bin/mockvendor-*` | 联调用厂商桩 | 与主程序同架构 |

> `bin/` 已在 `.gitignore` 中，不随源码入库；按 [环境部署说明文档](./05-环境部署说明文档.md) 第 3 节现场执行 `make build-linux-amd64` 即可复现，产物可经 `file` 验证为 statically linked ELF。

### 2.5 文档

即本 `docs/` 目录下 00~07 共 8 份文档与本索引。

---

## 3. 快速开始（5 分钟）

```bash
# 1) 编译 linux/amd64 单二进制（CGO-free）
make build-linux-amd64

# 2) 安装到 Ubuntu（幂等，首装自动生成 32 字节随机数据密钥）
sudo deploy/install.sh --bin bin/sms-gateway-linux-amd64

# 3) PC 浏览器打开（监听地址/端口见 /etc/sms-gateway/config.yaml，仅限受信网络访问）
#    http://<Ubuntu主机IP>:8080/

# 现场自检（全新临时目录，10 步全链路，不触碰生产库）
make smoke
```

完整步骤、前置网络条件与联调动作见 [环境部署说明文档](./05-环境部署说明文档.md)；界面操作见 [用户操作手册](./06-用户操作手册.md)。

---

## 4. 需求符合性总览

| 需求域 | 交付状态 | 主要证据 |
|---|---|---|
| 飞连 URL 验证 / token 校验 / 事件解析 / 加密兼容 | 已实现 | 单元测试、smoke 第 6 步 |
| 通用 HTTP 通道 + 表单化字段映射（零代码接新厂商） | 已实现 | 映射引擎表驱动测试、AC-8 端到端 |
| HTTP JSON 示例预置（报文/签名/响应/回执） | 已实现，凭证待向厂商申请后填入 | JDK 双端签名向量逐字节一致 |
| 三种签名策略 none/sha1_salt/hmac_sha256 | 已实现 | sign 包测试 + 固化向量 |
| 场景绑定与参数下标重排 | 已实现 | service 包测试 |
| 幂等去重 + 恒 3 秒内 200 + 失败只留痕 | 已实现 | smoke 第 8 步、AC-5/6/7 |
| 厂商异步回执回写 | 已实现 | smoke 第 10 步、AC-10 |
| 飞连参数 DB 化、WebUI 配置、热生效不重启 | 已实现 | AC-9 |
| WebUI 四视图（go:embed，内网浏览器直连） | 已实现 | AC-14 |
| 密钥 AES-GCM 密文存储、掩码、日志脱敏 | 已实现 | AC-9、AC-16，T14 |
| Zap JSON 全路径结构化日志 | 已实现 | T14、[测试报告](./07-测试报告.md) |
| headless 部署（systemd/install.sh/无外部依赖） | 已实现 | AC-15、T13 |
| 高可用集群 / 上行短信 / 飞连加密推送生产启用 | 不在本期范围 | 见 [项目概述](./00-项目概述.md) 范围说明 |

详细验收项逐条对照见 [测试报告](./07-测试报告.md) 第 6 节「验收标准对照」。

---

## 5. 联调前需客户/实施方确认的开放事项

以下事项不影响软件交付与本机自测，但真实上线前必须落实（详见部署文档第 7 节）：

1. 内网主机出网到 `sms.example.com:1443` 的防火墙/代理策略；
2. 示例厂商公网异步回执如何进入内网（反向映射/反代），回执 URL 在示例厂商申请时填报；
3. 示例厂商凭证 `appCode / appSecret / orgCode / templateCode` 的申请与短信模板审核进度；
4. 示例厂商 `code` 模板的参数顺序（与飞连参数顺序不一致时用绑定的「参数下标序列」修正）；
5. 监听端口（建议 8080）与管理面网络访问控制（安全组/主机防火墙/堡垒机/反代 ACL）；
6. 在飞连后台创建事件订阅、勾选短信通知事件、获取 Verification Token 的时间点。

---

## 6. 第三方组件与许可证

| 组件 | 版本 | 用途 | 许可证 |
|---|---|---|---|
| go.uber.org/zap | v1.28.0 | 结构化日志 | MIT |
| gopkg.in/yaml.v3 | v3.0.1 | 引导配置解析 | Apache-2.0 |
| modernc.org/sqlite | v1.58.0 | 纯 Go SQLite 驱动（CGO-free） | BSD-3-Clause |
| github.com/google/uuid | v1.6.0 | UUID 生成 | BSD-3-Clause |

前端为原生 HTML/ES Module/CSS，无 npm 依赖、无外部 CDN，离线可用。
