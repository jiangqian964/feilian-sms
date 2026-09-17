# 03 · API 接口文档

| 文档版本 | v1.0 | 软件版本 | v1.0.0 | 日期 | 2026-09-14 |
|---|---|---|---|---|---|

## 1. 通用约定

- **Base URL**：`http://<主机>:<端口>`（默认端口 8080）。
- **请求/响应编码**：`Content-Type: application/json; charset=utf-8`；飞连握手成功响应为 JSON 对象 `{"challenge":"..."}`；回执响应为通道配置体（默认 JSON）。
- **字符集**：UTF-8；时间字段除特别说明外均为 **Unix 毫秒时间戳（整数）**。
- **请求体上限**：1 MiB（Webhook、回执、管理 API 统一），超限返回 413。
- **缓存**：所有响应带 `Cache-Control: no-store` 与 `X-Content-Type-Options: nosniff`。
- **路由可达性**：Webhook（路径取系统设置，默认 `/feilian/sms/events`）、`/receipts/{channel_id}`、`GET /health`、`/api/*` 与 WebUI 静态资源**均不做来源 IP 限制**，应用层对所有路由一视同仁可达；管理面（`/api`、WebUI）的访问控制必须在网络边界（安全组/主机防火墙/堡垒机/反代 ACL）落实，切勿将管理口裸露公网。
- **鉴权**：应用层无登录；飞连来源由 `header.token`（Verification Token，恒定时间比较）校验；厂商回执在系统设置了回执鉴权 Token 时须携带 `X-Receipt-Token` 头（或 `?token=` 查询参数），恒定时间比较；管理面无应用层鉴权，安全边界为网络分区与边界访问控制。

### 1.1 统一错误响应

```json
{ "code": "bad_request", "message": "webhook 路径必须是以 / 开头、不含空白的非空路径", "field": "webhook_path" }
```

| HTTP 状态 | code | 触发场景 |
|---|---|---|
| 400 | `bad_request` | JSON 非法、字段校验失败、事件报文不可解析、应解密但无 key、回执体损坏 |
| 401 | `unauthorized` | 飞连 Verification Token 不匹配；已启用回执鉴权时回执 Token 缺失或不匹配 |
| 404 | `not_found` | Webhook 路径不存在、通道/记录不存在、回执通道不存在、接口不存在 |
| 409 | `conflict` | 对已停用通道发起测试发送 |
| 413 | `payload_too_large` | 请求体超过 1 MiB |
| 500 | `internal_error` | 服务端内部错误（详情仅入日志，不对外） |

> 对飞连事件，**业务成功与失败均返回 HTTP 200**（飞连仅以状态码决定重推）；401 仅用于 token 不符，400 仅用于报文不可解析/解密前置问题。

### 1.2 端点总表

| 方法 | 路径 | 用途 | 面 |
|---|---|---|---|
| GET | `/health` | 存活探针 | 公网 |
| POST | `/feilian/sms/events`（默认，可热改） | 飞连事件/握手入口 | 公网（token 校验） |
| POST | `/receipts/{channel_id}` | 厂商异步送达回执 | 公网 |
| GET | `/api/health` | 管理面存活探针 | 管理面 |
| GET/PUT | `/api/settings` | 系统设置读/改 | 管理面 |
| GET | `/api/presets` | 内置通道预置目录 | 管理面 |
| GET/POST | `/api/channels` | 通道列表/新建 | 管理面 |
| POST | `/api/channels/from-preset` | 从预置一键新建 | 管理面 |
| GET/PUT/DELETE | `/api/channels/{id}` | 通道详情/更新/删除 | 管理面 |
| POST | `/api/channels/{id}/enable` `/disable` | 启停 | 管理面 |
| POST | `/api/channels/{id}/test` | 测试发送 | 管理面 |
| GET/PUT | `/api/bindings` | 场景绑定读/批量替换 | 管理面 |
| GET | `/api/records` | 发送记录分页筛选 | 管理面 |
| GET | `/api/records/{app_sms_id}` | 发送记录详情 | 管理面 |
| GET | `/` 及静态资源 | WebUI | 管理面 |

---

## 2. 健康检查

### GET /health ／ GET /api/health
返回 200：
```json
{"status":"ok"}
```
响应头含 `Cache-Control: no-store`。前端每 20 秒以 XHR 静默调用 `/api/health`。

---

## 3. 飞连事件 Webhook

路径为系统设置中的 `webhook_path`（默认 `/feilian/sms/events`），修改后下一条请求即按新路径匹配，无需重启。

### 3.1 URL 有效性验证（url_verification）

请求：
```json
{ "challenge": "smoke-challenge-0001", "token": "smoke-token-001", "type": "url_verification" }
```
- token 与系统设置一致：**200**，`Content-Type: application/json; charset=utf-8`，响应体为 JSON 对象，其 `challenge` 字段与请求值逐字符一致：
```json
{ "challenge": "smoke-challenge-0001" }
```
  > 飞连/飞书事件订阅网关按 JSON 解析响应并读取 `challenge` 字段；切勿返回 `text/plain` 裸字符串，否则网关解析不到该字段会判定地址校验失败。
- token 不符：401 `{"code":"unauthorized","message":"Verification Token 不匹配"}`。
- 配置 Encrypt Key 时请求体为加密信封，服务先解密再判定。

### 3.2 短信事件 notify.v1.sms

请求（`testdata/feilian/event_sms_code.json`）：
```json
{
  "schema": "1.0",
  "header": {
    "event_id": "smoke-code-0001",
    "token": "smoke-token-001",
    "create_time": "1700000000000",
    "event_type": "notify.v1.sms",
    "app_id": "cli-smoke-app"
  },
  "data": { "events": [ { "object": {
    "country_code": "+86",
    "mobile_number": "13800000056",
    "mobile": "+8613800000056",
    "sms_type": "code",
    "template": "您的验证码是%s，%s分钟内有效，请勿泄露。",
    "params": ["482916", "5"],
    "expired_time": 1700000300
  } } ] }
}
```

响应（通过 token 后恒 200）：
```json
{"code":0,"message":"success"}
```

处理规则：
- `header.token` 不符 → 401，且不调用下游、不产生发送记录（token 校验先于事件类型分流，非短信事件携带错误 token 同样返回 401）。
- 非 `notify.v1.sms` 事件：通过 token 校验后记录日志并回 200，不做短信体校验、不调用编排层。
- 批量 `data.events[]`：逐条处理，幂等键分别为 `event_id`（单条）或 `event_id-下标`（批量）；任一单条失败不影响其余条目与最终 200。
- 下游成功/失败/未绑定等结果不通过 HTTP 体现，仅写入发送记录与日志。

### 3.3 出站到示例厂商的实际报文（字段映射结果，供联调核对）
```json
{
  "appCode": "DEMO-SMOKE",
  "appSmsId": "smoke-code-0001",
  "mobile": "8613800000056",
  "nonce": "48291",
  "orgCode": "100001",
  "params": ["482916", "5"],
  "sign": "a4d89e4c1019d943b1738ac4613084be231693bf",
  "templateCode": "SMS_TEST",
  "timestamp": 1740385174957
}
```

### 3.4 示例厂商同步响应与业务错误码

成功响应：
```json
{ "status": 0, "message": "success", "data": { "id": "示例厂商侧短信ID", "appSmsId": "smoke-code-0001" } }
```

通道响应判定取 `response.success_path=status`、`success_value=0`（数字 `0` 与字符串 `"0"` 宽松相等均判成功），消息 ID 取 `data.id`。任何 HTTP 2xx 但 `status≠0` 一律归类为 `error_kind=vendor`，并把厂商状态码与文案**原样**写入发送记录（`provider_status`/`provider_message`）。内置示例预置采用的业务错误码词典如下（**仅为示例值，状态码与中文文案以对接厂商的接口规范及实际 `message` 为准**）：

| status | 含义 |
|---|---|
| 0 | 成功 |
| 50001 | 手机号非法 |
| 50002 | 签名失败 |
| 50003 | APP 不存在 |
| 50006 | appCode 未启用 |
| 50007 | templateCode 未启用 |
| 50008 | 敏感词 |
| 50009 | 超频 |
| 50010 | 内容超长 |

> 说明：HTTP 层非 2xx/连接失败/超时分别归类为 `network`/`timeout`，与业务错误 `vendor` 区分。业务失败样例见 `testdata/provider/response_business_error.json`。

---

## 4. 厂商异步回执

### POST /receipts/{channel_id}

请求（示例厂商样例 `testdata/provider/receipt_delivered.json`）：
```json
{ "smsId": "sha1-msg-20260914-0001", "appSmsId": "smoke-code-0001",
  "status": "DELIVRD", "statusMessage": "成功", "seqNo": 1 }
```

成功响应（通道配置体，示例厂商默认精确字节）：
```json
{"status":0,"message":"success"}
```

行为：
- **鉴权优先**：系统设置了「厂商回执鉴权 Token」时，请求必须携带 `X-Receipt-Token: <token>` 请求头（或等价的 `?token=<token>` 查询参数，头优先）；缺失或不匹配一律 **401** `unauthorized`，且先于请求体读取与通道查找（即使通道不存在也返回 401 而非 404，避免侧信道探测）。未配置 Token 时不校验，兼容内网/网络层隔离部署。比较采用恒定时间算法。
- 通道不存在：404 `not_found`（仅在通过鉴权后才可能返回）。
- 回执体不是合法 JSON 或缺少 `appSmsId` 路径：400。
- 字段语义按通道回执配置的路径提取，送达状态取**三态**：`status == DELIVRD`（成功值可配置）回写 `delivery_status=delivered`；命中配置的失败值回写 `delivery_failed`；其他状态值保持 `delivery_status` 为空（表示厂商尚未给终态），仅推进 `seq_no` 地板与 `receipt_at`。
- 写入三重守卫：通道归属不一致（回执 `channel_id` 与记录路由不符）只裁决不落库；`seq_no` 倒退的乱序回执跳过；`delivered` 不被无更大 `seq_no` 的失败回执回退。
- 未知 `appSmsId`：记日志（matched_record=false）但**仍回成功响应**，避免厂商重推。
- 主发送状态不受回执改变；回执来源直连 IP 入日志（不读 `X-Forwarded-For`）。

---

## 5. 系统设置 ／api/settings

### GET /api/settings → 200
```json
{
  "verification_token": "smoke-token-001",
  "encrypt_key_set": false,
  "encrypt_key_masked": "****",
  "receipt_auth_token_set": false,
  "receipt_auth_token_masked": "****",
  "webhook_path": "/feilian/sms/events",
  "webhook_url": "http://127.0.0.1:8080/feilian/sms/events",
  "public_base_url": "http://127.0.0.1:8080",
  "downstream_timeout_ms": 2000,
  "stale_pending_ms": 120000,
  "updated_at": 1740385174000
}
```
说明：Verification Token 为普通接入参数，明文回显；Encrypt Key 与回执鉴权 Token 仅回掩码与是否已设置（未设置时掩码固定为 `****`）；`webhook_url` 为「对外基址 + webhook 路径」拼接结果，供复制到飞连后台。

### PUT /api/settings → 200（回显同 GET）
请求体字段均为可选（缺省=不修改，读改写合并）：
```json
{
  "verification_token": "new-token",
  "encrypt_key": "",
  "clear_encrypt_key": false,
  "receipt_auth_token": "",
  "clear_receipt_auth_token": false,
  "webhook_path": "/feilian/sms/events",
  "public_base_url": "http://10.0.0.10:8080",
  "downstream_timeout_ms": 2000,
  "stale_pending_ms": 120000
}
```

| 字段 | 校验 |
|---|---|
| `webhook_path` | 必须以 `/` 开头、非空、不含空白与 `?`/`#`；不得占用 `/api`、`/api/`、`/receipts`、`/receipts/`、`/health` 保留前缀 |
| `public_base_url` | 空或合法 http(s) URL（须有 host） |
| `downstream_timeout_ms` | 100 ~ 60000 |
| `stale_pending_ms` | 1000 ~ 86400000 |
| `encrypt_key` / `clear_encrypt_key` | 二者不可同时提供；空串 encrypt_key=不修改；`clear_encrypt_key=true` 清空 |
| `receipt_auth_token` / `clear_receipt_auth_token` | 二者不可同时提供；空串 receipt_auth_token=不修改；长度至少 16 个字符且不得含空白；`clear_receipt_auth_token=true` 清空（清空后回执端点不再校验） |

保存成功后下一条请求即生效（token、webhook 路径、超时、回执鉴权均热生效）。

---

## 6. 通道管理 ／api/channels

### 6.1 通道配置对象（config）

顶层结构：`request / constants / body_mappings / sign / response / mobile_policy / receipt`。示例厂商通道完整示例：

```json
{
  "request": {
    "base_url": "https://sms.example.com:1443",
    "url": "${base}/sms/send",
    "method": "POST",
    "content_type": "application/json",
    "headers": { }
  },
  "constants": {
    "appCode":   { "value": "", "secret": false },
    "orgCode":   { "value": "", "secret": false },
    "appSecret": { "value": "", "secret": true }
  },
  "body_mappings": [
    { "target": "appCode",      "source_type": "const",    "source": "appCode",      "value_type": "string" },
    { "target": "appSmsId",     "source_type": "variable", "source": "appSmsId",     "value_type": "string" },
    { "target": "mobile",       "source_type": "variable", "source": "mobile",       "value_type": "string" },
    { "target": "nonce",        "source_type": "variable", "source": "nonce",        "value_type": "string" },
    { "target": "orgCode",      "source_type": "const",    "source": "orgCode",      "value_type": "string" },
    { "target": "params",       "source_type": "variable", "source": "params",       "value_type": "raw" },
    { "target": "sign",         "source_type": "variable", "source": "sign",         "value_type": "string" },
    { "target": "templateCode", "source_type": "variable", "source": "templateCode", "value_type": "string" },
    { "target": "timestamp",    "source_type": "variable", "source": "timestamp",    "value_type": "number" }
  ],
  "sign": {
    "strategy": "sha1_salt",
    "secret_const": "appSecret",
    "segments": [
      { "kind": "literal",  "value": "timestamp=" },
      { "kind": "variable", "value": "timestamp" },
      { "kind": "literal",  "value": "&nonce=" },
      { "kind": "variable", "value": "nonce" },
      { "kind": "literal",  "value": "&signData=" },
      { "kind": "variable", "value": "appSmsId" }
    ]
  },
  "response": { "success_path": "status", "success_value": "0",
                "msg_id_path": "data.id", "message_path": "message" },
  "mobile_policy": "cc_prefix",
  "receipt": {
    "msg_id_path": "smsId", "app_msg_id_path": "appSmsId",
    "status_path": "status", "delivered_value": "DELIVRD",
    "message_path": "statusMessage", "seq_no_path": "seqNo", "success_body": ""
  }
}
```

枚举：
- `method`：POST / GET / PUT（保存时归一化为大写，非法值 400）。
- `source_type`：`variable` / `const` / `literal`；`value_type`：`string` / `number` / `boolean` / `raw`。
- `sign.strategy`：`none` / `sha1_salt` / `hmac_sha256`（hmac 的 `encoding`：hex/base64）；`segments[].kind`：`literal` / `variable`。
- `mobile_policy`：`cc_prefix`（默认）/ `strip_plus` / `raw`。
- 鉴权头占位：`${const:名称}`（明文）、`${constb64:名称}`（base64，用于 Basic）。
- 内置变量：appSmsId/mobile/mobileNumber/countryCode/smsType/templateCode/timestamp/nonce/sign/params/param0..N。

### 6.2 GET /api/channels → 200
```json
{ "channels": [ { "通道对象，见 6.3" } ] }
```

### 6.3 POST /api/channels → 201
请求体（密钥只提交需要写入的项，键名必须是 constants 中声明的 secret）：
```json
{
  "name": "示例厂商-生产",
  "description": "示例短信平台",
  "enabled": true,
  "config": { },
  "secrets": { "appSecret": "实际密钥明文，仅本次提交，存储为密文" }
}
```
响应 201（通道对象）：
```json
{
  "id": "7ddd8b8f-d579-4762-92b5-b521088b6cbd",
  "name": "示例厂商-生产",
  "description": "示例短信平台",
  "enabled": true,
  "config": { },
  "secrets_masked": { "appSecret": { "value": "前2后2掩码", "set": true } },
  "receipt_url": "http://10.0.0.10:8080/receipts/7ddd8b8f-d579-4762-92b5-b521088b6cbd",
  "created_at": 1740385174000,
  "updated_at": 1740385174000
}
```
- 保存前服务端执行试渲染：非法变量、重复 target、值类型错误、签名配置错误等返回 400 并带 `field`（如 `config.body_mappings`、`config.sign`、`config.mobile_policy`）；仅密钥未填可放行。
- 提交的密钥名若未在 constants 声明为 secret：400，`field=secrets`。

### 6.4 POST /api/channels/from-preset → 201
```json
{ "preset": "http_json_v1", "name": "示例厂商-冒烟", "description": "" }
```
预置脚手架允许密钥待填（不经试渲染拦截）；未知 preset 返回 400（`field=preset`）。

### 6.5 GET /api/channels/{id} → 200
返回单个通道对象（结构同 6.3 响应）；不存在 404。

### 6.6 PUT /api/channels/{id} → 200
请求体同新建（全量提交 config）。`secrets` 采用合并语义：未提交/空值=保持原密钥不变，提交非空值=覆盖；保存前以「既有密钥 + 本次提交」合并后试渲染。

### 6.7 DELETE /api/channels/{id} → 204
删除通道（级联删除其密钥；绑定引用通道的场景将在事件处理时归为 channel_not_found）。不存在 404。

### 6.8 POST /api/channels/{id}/enable ｜ /disable → 200
切换启停并回读返回通道对象；不存在 404。

### 6.9 POST /api/channels/{id}/test → 200（测试发送）
请求：
```json
{ "template_code": "SMS_TEST", "sms_type": "code",
  "country_code": "+86", "mobile_number": "1380000056",
  "mobile": "861380000056", "params": ["123456", "5"] }
```
响应（同步结果，始终 200，成败看 `success`）：
```json
{ "app_sms_id": "test-1740385172518-5c5bf14a436e",
  "success": false, "http_code": 200,
  "error_kind": "vendor", "message": "模拟业务失败" }
```
- 必填：`template_code`、`mobile_number`；`sms_type` 默认 code 且必须为 9 种之一。
- 通道不存在 404；通道停用 409。
- 生成 `test-<毫秒>-<随机>` 幂等键、`source=test`，全程留痕；渲染/网络/超时/业务失败均以 `error_kind` 返回。

---

## 7. 场景绑定 ／api/bindings

### GET /api/bindings → 200
```json
{
  "sms_types": ["code","init_password","reset_password","alert","guest_wifi",
                "password_expiration","accout_expire","wifi_info","exchange_mfa"],
  "bindings": [
    { "sms_type": "code", "channel_id": "7ddd8b8f-...",
      "template_code": "SMS_TEST", "param_index": [0, 1],
      "enabled": true, "updated_at": 1740385174000 }
  ]
}
```

### PUT /api/bindings → 200（全量替换，回显同 GET）
```json
{ "bindings": [
  { "sms_type": "code", "channel_id": "7ddd8b8f-...",
    "template_code": "SMS_TEST", "param_index": [0, 1], "enabled": true }
] }
```
校验（任一不过整批拒绝、不写库，400 + `field=bindings`）：sms_type 必须在 9 种内且批内不重复；channel_id 必须存在；template_code 非空；param_index 不得含负数。`param_index` 留空数组/缺省表示保持飞连原序。

---

## 8. 发送记录 ／api/records

### GET /api/records → 200（分页 + 筛选）

Query 参数：

| 参数 | 说明 |
|---|---|
| `status` | `pending` / `success` / `failed` |
| `sms_type` | 9 种场景之一 |
| `channel_id` | 通道 ID |
| `source` | `feilian` / `test` |
| `from` / `to` | created_at 的毫秒时间戳下界/上界（非负整数） |
| `limit` | 正整数，默认 50，最大 200（超出截断为 200） |
| `offset` | 非负整数，默认 0 |

响应：
```json
{
  "records": [ { "发送记录对象，见 8.2" } ],
  "total": 1, "limit": 50, "offset": 0
}
```
空结果 `records` 为 `[]`（非 null）。非法 query 参数返回 400 并带 `field`。

### 8.2 GET /api/records/{app_sms_id} → 200
```json
{
  "app_sms_id": "smoke-code-0001",
  "event_id": "smoke-code-0001",
  "source": "feilian",
  "channel_id": "7ddd8b8f-d579-4762-92b5-b521088b6cbd",
  "sms_type": "code",
  "mobile_masked": "13****56",
  "params_masked": ["48****16", "5"],
  "template_code": "SMS_TEST",
  "status": "success",
  "provider_msg_id": "mock-1789376371345-1",
  "provider_status": "200",
  "provider_message": "success",
  "delivery_status": "delivered",
  "delivery_message": "成功",
  "seq_no": 1,
  "receipt_at": 1789376372000,
  "error_kind": "",
  "latency_ms": 5,
  "attempts": 0,
  "created_at": 1789376371000,
  "updated_at": 1789376372000
}
```
不存在返回 404。字段说明见 [04 · 数据库设计文档](./04-数据库设计文档.md) 第 3.5 节；手机号/参数在库中即脱敏形态。`attempts` 为补发认领次数（首次下发不计，上限 3 次）。

---

## 9. 字段词典：error_kind 与状态枚举

| 枚举 | 取值 |
|---|---|
| 主状态 `status` | `pending` / `success` / `failed`（终态不可逆） |
| 回执状态 `delivery_status` | `delivered` / `delivery_failed`（空表示尚无回执，或厂商状态既非成功值也非失败值） |
| 来源 `source` | `feilian` / `test` |
| 失败分类 `error_kind` | `unbound`（未绑定）、`binding_disabled`（绑定停用）、`channel_disabled`（通道停用）、`channel_not_found`（通道缺失）、`invalid_mobile`（号码无法归一化）、`render`（映射/签名/参数渲染失败）、`vendor`（2xx 业务拒绝）、`network`（连接/DNS/非 2xx）、`timeout`（下游超时）、`resend_exhausted`（传输类失败经最多 3 次补发仍未确认）、`internal`（内部错误） |

---

## 10. 命令行参数

| 参数 | 说明 |
|---|---|
| `--config <path>` | 引导配置 YAML 路径；留空则纯环境变量启动 |

示例：`/usr/local/bin/sms-gateway --config /etc/sms-gateway/config.yaml`

---

**返回**：[交付文档总索引](./README.md)　｜　上一篇：[02 · 功能模块说明文档](./02-功能模块说明文档.md)　｜　下一篇：[04 · 数据库设计文档](./04-数据库设计文档.md)
