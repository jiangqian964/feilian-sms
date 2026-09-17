# feilian-sms · 飞连短信事件转发通用短信网关

把飞连（Feilian）事件订阅推送的短信通知事件，转发到**任意基于 HTTP/JSON 的短信厂商接口**的独立网关服务。核心是一套**厂商无关的通用通道引擎**（地址、鉴权、报文字段映射、签名、响应判定、回执全部表单化配置、热生效、零代码接入新厂商），并内置一个开箱即用的「HTTP JSON 示例预置」。

- 单二进制、纯 Go、CGO-free，内嵌 SQLite 与 WebUI（`go:embed`，无 npm/CDN 依赖）
- Linux（amd64/arm64）+ systemd headless 部署；密钥 AES-256-GCM 密文存储、日志强制脱敏
- 幂等去重、恒 3 秒内回 200、异步回执回写、场景绑定与参数下标重排

---

## 目录结构

```
cmd/sms-gateway/      主进程入口（引导 → 装配 → 信号驱动优雅退出）
cmd/mockvendor/       本地厂商桩（联调/冒烟用，非生产组件）
internal/
  bootstrap/          引导配置（YAML + 环境变量）、数据密钥解析
  feilian/            飞连 Webhook 入站协议：信封、challenge、加密兼容、9 种 sms_type
  channel/            通用 HTTP 短信通道运行时：配置、变量、HTTP 客户端、示例预置
    mapping/          字段映射引擎（点分路径组装嵌套 JSON）
    sign/             签名策略：none / sha1_salt / hmac_sha256
  store/              SQLite、配置快照热加载、AES-GCM 密钥信封、发送状态机
  service/            编排层：转发幂等、回执、测试发送、设置运行时
  httpapi/            Webhook、回执、/health、管理 API（/api）
  webui/  logging/    内嵌静态资源处理器；Zap 日志与统一出口脱敏
web/                  前端（原生 HTML/ES Module/CSS）
deploy/               config 样例、install.sh、systemd unit、全链路冒烟脚本
testdata/             飞连/厂商协议样例与跨语言签名向量参考程序
docs/                 00~07 全套交付文档（架构/API/数据库/部署/手册/测试报告）
```

---

## 快速开始

需要 Go 1.26.3+（以 [go.mod](./go.mod) 为准）。

```bash
# 本地编译（产物在 bin/）
make build-local

# 单元测试 / 竞态检测
make test
make race

# 全链路冒烟：临时目录起 mockvendor + 网关，curl 串联 10 步后自动清理
make smoke

# Linux 交叉编译（CGO-free，产物为静态链接 ELF）
make build-linux-amd64     # x86_64
make build-linux-arm64     # arm64
```

Linux 上一键安装（建用户/目录、生成 32 字节随机数据密钥、安装二进制与 systemd unit 并 `enable --now`）：

```bash
sudo deploy/install.sh --bin bin/sms-gateway-linux-amd64
```

启动参数为 `--config <引导配置 YAML>`（留空则纯环境变量启动）；引导配置仅含监听、SQLite 路径、日志、数据密钥等启动项，样例见 [config.example.yaml](./deploy/config.example.yaml)。飞连接入参数、通道、字段映射、签名、场景绑定等**业务配置全部在 WebUI 中完成并持久化到 SQLite，改后热生效、无需重启**。浏览器访问 `http://<主机IP>:8080/`（默认端口见引导配置；管理面不做来源 IP 限制，请通过安全组/防火墙等网络边界控制访问，切勿裸露公网）。

---

## 文档

完整交付文档索引见 [docs/README.md](./docs/README.md)：

| 文档 | 内容 |
|---|---|
| [00 项目概述](./docs/00-项目概述.md) | 背景、目标、范围、术语 |
| [01 系统架构设计](./docs/01-系统架构设计.md) | 分层、数据流、关键设计、安全与可靠性 |
| [02 功能模块说明](./docs/02-功能模块说明文档.md) | 配置模型、字段映射引擎、签名、幂等、回执、WebUI |
| [03 API 接口文档](./docs/03-API接口文档.md) | 飞连 Webhook、厂商回执、管理 API（/api） |
| [04 数据库设计](./docs/04-数据库设计文档.md) | 表结构、索引、迁移、密钥存储与备份 |
| [05 环境部署说明](./docs/05-环境部署说明文档.md) | 构建、安装、systemd、联调、升级卸载 |
| [06 用户操作手册](./docs/06-用户操作手册.md) | WebUI 四视图逐项操作指引 |
| [07 测试报告](./docs/07-测试报告.md) | 单测、竞态、覆盖率、静态检查、交叉编译、冒烟结果 |

---

## 占位与脱敏声明

本仓库为公开发布版本，**不含任何真实客户信息、真实厂商端点或真实凭证**，以下均为可安全公开的占位内容，对接真实环境时必须替换：

- 厂商基址 `https://sms.example.com:1443` 与请求路径 `/sms/send` 均为**虚构占位**；
- 预置/测试/冒烟中的 `demo-*`、`smoke-*`、`DEMOAPP`、`smoke-secret-*` 等 AppCode/AppSecret 与签名盐均为**演示值**；
- 手机号夹具（如 `13800000000`、`13800001111`、`13800000056`）为**合成测试号码**，仅用于脱敏/解析测试；
- 「示例厂商」为虚构对接方；业务错误码（如 `50001`）仅为示例值，实际状态码与文案以对接厂商规范为准；
- SHA-1 加盐签名策略（`sha1_salt`）的 Go/JDK 双端一致性由 [testdata/GenSignVectors.java](./testdata/GenSignVectors.java) 生成的公开向量互验，输入均为上述演示值。

上线前需要自行落实：真实厂商基址与出网策略、`appCode/appSecret/orgCode/templateCode` 等凭证与短信模板审核、公网异步回执进入内网的方案、Verification Token 与管理面网络访问控制（安全组/防火墙/堡垒机/反代 ACL）。

## 安全

- 通道密钥以 AES-256-GCM 信封加密落库，界面只回掩码，数据主密钥经环境变量注入或首启随机生成（`data.key` 已在 `.gitignore`，禁止入库）；
- 手机号、参数、secret/token/sign 在日志出口统一掩码；响应默认 `Cache-Control: no-store`；
- systemd unit 采用最小权限加固（无 capability、`Protect*`、`PrivateTmp` 等）。

## 第三方组件

| 组件 | 用途 | 许可证 |
|---|---|---|
| go.uber.org/zap | 结构化日志 | MIT |
| gopkg.in/yaml.v3 | 引导配置解析 | Apache-2.0 |
| modernc.org/sqlite | 纯 Go SQLite 驱动（CGO-free） | BSD-3-Clause |
| github.com/google/uuid | UUID 生成 | BSD-3-Clause |

前端为原生 HTML/ES Module/CSS，无 npm 依赖、无外部 CDN，可完全离线运行。

## 许可与免责

本项目基于 [MIT License](./LICENSE) 开源（Copyright (c) 2026 jiangqian964）。本项目为通用对接参考实现，按“现状”提供，不提供任何明示或默示担保；使用者需自行评估其在自身环境中的安全性与合规性。完整条款见根目录 [LICENSE](./LICENSE)。
