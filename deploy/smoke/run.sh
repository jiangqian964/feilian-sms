#!/usr/bin/env bash
#
# T13 全链路冒烟：全新临时目录起 mockvendor + sms-gateway，curl 串联
#   健康检查 → 系统设置 → 预置建通道 → 补常量/密钥 → 场景绑定 →
#   url_verification 握手 → 短信事件转发 → 幂等重放 → 未绑定场景留痕 →
#   厂商回执回写 → 厂商业务失败分支 → records API 数据校验。
#
# 依赖：bash 3.2+、curl、python3（Ubuntu server / macOS 均自带）。
# 端口可用环境变量覆盖：GW_PORT（默认 18090）、MOCK_PORT（默认 19091）。
# 任一步断言失败立即退出非 0，并打印网关日志尾部。
set -euo pipefail

GW_PORT="${GW_PORT:-18090}"
MOCK_PORT="${MOCK_PORT:-19091}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GW_BIN="${REPO_DIR}/bin/sms-gateway"
MOCK_BIN="${REPO_DIR}/bin/mockvendor"
TD="${REPO_DIR}/testdata"

GW="http://127.0.0.1:${GW_PORT}"
MOCK="http://127.0.0.1:${MOCK_PORT}"
WEBHOOK="/feilian/sms/events"
TOKEN="smoke-token-001"

for tool in curl python3; do
	command -v "${tool}" >/dev/null 2>&1 || {
		echo "缺少依赖: ${tool}" >&2
		exit 2
	}
done
[ -x "${GW_BIN}" ] || {
	echo "找不到 ${GW_BIN}，请先执行 make build-local" >&2
	exit 2
}
[ -x "${MOCK_BIN}" ] || {
	echo "找不到 ${MOCK_BIN}，请先执行 make build-local" >&2
	exit 2
}

W="$(mktemp -d "${TMPDIR:-/tmp}/smsgw-smoke.XXXXXX")"
PIDS=()
cleanup() {
	for p in "${PIDS[@]:-}"; do
		kill -TERM "${p}" >/dev/null 2>&1 || true
	done
}
trap cleanup EXIT

step() { printf '\n==== %s ====\n' "$1"; }
fail() {
	echo "断言失败: $1" >&2
	echo "---- 网关日志尾部 ----" >&2
	tail -30 "${W}/gw.log" >&2 || true
	exit 1
}
assert_eq() { [ "$1" = "$2" ] || fail "期望 [$2] 实际 [$1]（$3）"; }
assert_contains() { case "$2" in *"$1"*) ;; *) fail "响应缺少 [$1]（$3）: $2" ;; esac; }

# jget 从 stdin JSON 按键路径取值；对象/数组输出紧凑 JSON，标量原样输出。
jget() {
	python3 -c '
import sys, json
d = json.load(sys.stdin)
for k in sys.argv[1:]:
    d = d[int(k)] if isinstance(d, list) else d[k]
if isinstance(d, (dict, list)):
    print(json.dumps(d, ensure_ascii=False, separators=(",", ":")))
elif d is None:
    print("")
else:
    print(d)
' "$@"
}

# api <METHOD> <path> [json-body]：响应写入 $W/last_resp 并打印 HTTP 码。
api() {
	local method="$1" path="$2" body="${3:-}"
	if [ -n "${body}" ]; then
		curl -s -o "${W}/last_resp" -w '%{http_code}' \
			-X "${method}" "${GW}${path}" -H 'Content-Type: application/json' -d "${body}"
	else
		curl -s -o "${W}/last_resp" -w '%{http_code}' \
			-X "${method}" "${GW}${path}"
	fi
}
apif() { # 方法 路径 请求体文件
	curl -s -o "${W}/last_resp" -w '%{http_code}' \
		-X "$1" "${GW}$2" -H 'Content-Type: application/json' --data-binary "@$3"
}
resp() { cat "${W}/last_resp"; }
record_total() { curl -s "${GW}/api/records?limit=200" | jget total; }

echo "临时目录: ${W}"

# ---- 启动两个进程 ----
"${MOCK_BIN}" -listen "127.0.0.1:${MOCK_PORT}" -dump-dir "${W}/dump" \
	>"${W}/mock.log" 2>&1 &
PIDS+=("$!")
SMSGW_SERVER_LISTEN="127.0.0.1:${GW_PORT}" \
	SMSGW_SQLITE_PATH="${W}/sms.db" \
	SMSGW_LOG_FORMAT="${SMSGW_LOG_FORMAT:-console}" \
	"${GW_BIN}" >"${W}/gw.log" 2>&1 &
PIDS+=("$!")

ready=0
for _ in $(seq 1 50); do
	if [ "$(curl -s -o /dev/null -w '%{http_code}' "${GW}/health" || true)" = "200" ]; then
		ready=1
		break
	fi
	sleep 0.1
done
[ "${ready}" = "1" ] || fail "网关未在 5 秒内就绪"

step "1/10 健康检查"
assert_eq "$(curl -s "${GW}/health")" '{"status":"ok"}' "health 响应"
echo "✓ /health"

step "2/10 系统设置"
code="$(api PUT /api/settings "{
  \"verification_token\": \"${TOKEN}\",
  \"webhook_path\": \"${WEBHOOK}\",
  \"public_base_url\": \"${GW}\",
  \"downstream_timeout_ms\": 2000,
  \"stale_pending_ms\": 120000
}")"
assert_eq "${code}" "200" "settings PUT"
echo "✓ 设置已保存"

step "3/10 内置预置建通道"
code="$(api POST /api/channels/from-preset \
	'{"preset":"http_json_v1","name":"示例厂商-冒烟"}')"
assert_eq "${code}" "201" "from-preset"
CID="$(resp | jget id)"
[ -n "${CID}" ] || fail "未取得通道 ID"
echo "✓ 通道 ${CID}"

step "4/10 补基址/appCode/appSecret（保存前试渲染）"
PUT_BODY="$(resp | jget config | MOCK="${MOCK}" python3 -c '
import sys, json, os
cfg = json.load(sys.stdin)
cfg["request"]["base_url"] = os.environ["MOCK"]
cfg["constants"]["appCode"] = {"value": "DEMO-SMOKE", "secret": False}
print(json.dumps({"name": "示例厂商-冒烟", "config": cfg,
                  "secrets": {"appSecret": "smoke-secret-1"}}, ensure_ascii=False))
')"
code="$(api PUT "/api/channels/${CID}" "${PUT_BODY}")"
assert_eq "${code}" "200" "PUT 通道（试渲染应通过）"
assert_eq "$(resp | jget config request method)" "POST" "预置方法"
echo "✓ 凭证与基址已保存"

step "5/10 场景绑定 code → 通道"
code="$(api PUT /api/bindings "{
  \"bindings\": [{
    \"sms_type\": \"code\", \"channel_id\": \"${CID}\",
    \"template_code\": \"SMS_TEST\", \"param_index\": [0, 1], \"enabled\": true
  }]
}")"
assert_eq "${code}" "200" "bindings PUT"
echo "✓ 绑定生效"

step "6/10 url_verification 握手（JSON 原样回显 challenge）"
code="$(apif POST "${WEBHOOK}" "${TD}/feilian/url_verification.json")"
assert_eq "${code}" "200" "challenge HTTP"
assert_eq "$(resp | jget challenge)" "smoke-challenge-0001" "challenge 必须秒级以 JSON 对象原样回显"
echo "✓ challenge 以 JSON 原样返回"

step "7/10 短信事件转发（mockvendor 实收完整厂商报文）"
code="$(apif POST "${WEBHOOK}" "${TD}/feilian/event_sms_code.json")"
assert_eq "${code}" "200" "事件 HTTP"
assert_eq "$(resp)" '{"code":0,"message":"success"}' "事件恒 200 响应"
sleep 0.2
assert_contains '"appSmsId":"smoke-code-0001"' "$(cat "${W}/dump/last.json")" "厂商收到 appSmsId"
grep -Eq '"sign":"[0-9a-f]{40}"' "${W}/dump/last.json" || fail "厂商报文缺少 40 位小写 hex 签名"
echo "✓ 报文已下发并含合法签名"

code="$(api GET /api/records/smoke-code-0001)"
assert_eq "${code}" "200" "单条记录"
assert_eq "$(resp | jget status)" "success" "主状态"
assert_eq "$(resp | jget mobile_masked)" "13****56" "手机号掩码"
assert_eq "$(record_total)" "1" "首条事件后记录总数"
echo "✓ 记录 status=success，手机号 13****56"

step "8/10 幂等重放（同一 event_id 不重复下发）"
code="$(apif POST "${WEBHOOK}" "${TD}/feilian/event_sms_code.json")"
assert_eq "${code}" "200" "重放 HTTP"
sleep 0.2
assert_eq "$(record_total)" "1" "重放不得产生新记录"
assert_eq "$(grep -c '"component":"mockvendor"' "${W}/mock.log" || true)" "1" "厂商只应收到 1 次请求"
echo "✓ 幂等闸门生效"

step "9/10 未绑定场景恒 200 且留痕 failed/unbound"
code="$(apif POST "${WEBHOOK}" "${TD}/feilian/event_sms_guest_wifi.json")"
assert_eq "${code}" "200" "guest_wifi HTTP"
sleep 0.2
code="$(api GET /api/records/smoke-guest-0001)"
assert_eq "$(resp | jget status)" "failed" "未绑定主状态"
assert_eq "$(resp | jget error_kind)" "unbound" "未绑定错误分类"
echo "✓ 未绑定场景安全留痕"

step "10/10 厂商回执回写 + 业务失败分支"
code="$(apif POST "/receipts/${CID}" "${TD}/provider/receipt_delivered.json")"
assert_eq "${code}" "200" "回执 HTTP"
assert_eq "$(resp)" '{"status":0,"message":"success"}' "回执响应必须精确字节"
code="$(api GET /api/records/smoke-code-0001)"
assert_eq "$(resp | jget delivery_status)" "delivered" "回执归一化状态"
assert_eq "$(resp | jget seq_no)" "1" "回执序号"
echo "✓ 回执 delivered 已回写"

curl -s -X POST "${MOCK}/__control" -H 'Content-Type: application/json' \
	-d '{"mode":"business_error","fail_code":50001,"fail_message":"模拟业务失败"}' >/dev/null
code="$(api POST "/api/channels/${CID}/test" \
	'{"sms_type":"code","template_code":"SMS_TEST","country_code":"+86","mobile_number":"13800000056","params":["123456","5"]}')"
assert_eq "${code}" "200" "测试发送 HTTP"
assert_eq "$(resp | jget success)" "False" "业务失败 success=false"
assert_eq "$(resp | jget error_kind)" "vendor" "业务失败归类 vendor"
curl -s -X POST "${MOCK}/__control" -H 'Content-Type: application/json' \
	-d '{"mode":"success"}' >/dev/null
echo "✓ 厂商业务失败分支归类正确"

printf '\n======== 全链路冒烟通过（全部步骤退出码 0）========\n'
