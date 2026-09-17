#!/usr/bin/env bash
#
# sms-gateway 一键安装（Ubuntu/Debian，headless，无需图形环境）。
#
# 做什么（全部幂等，可重复执行）：
#   1. 创建系统用户/组 sms-gateway（nologin）；
#   2. 建数据目录 /var/lib/sms-gateway（0750）与配置目录 /etc/sms-gateway（0750）；
#   3. 首装生成 32 字节随机数据密钥到 /etc/sms-gateway/sms-gateway.env（0640，不覆盖）；
#   4. 安装引导配置 config.yaml（不覆盖已有配置）；
#   5. 安装二进制到 /usr/local/bin/sms-gateway；
#   6. 安装 systemd unit 并 daemon-reload + enable --now。
#
# 用法：
#   sudo deploy/install.sh                      # 安装/升级并启动
#   sudo deploy/install.sh --bin ./bin/x        # 指定二进制
#   deploy/install.sh --dry-run                 # 只打印动作，不改动系统（可非 root）
set -euo pipefail

SERVICE_USER="sms-gateway"
SERVICE_GROUP="sms-gateway"
BIN_TARGET="/usr/local/bin/sms-gateway"
CONF_DIR="/etc/sms-gateway"
CONF_FILE="${CONF_DIR}/config.yaml"
ENV_FILE="${CONF_DIR}/sms-gateway.env"
DATA_DIR="/var/lib/sms-gateway"
UNIT_TARGET="/etc/systemd/system/sms-gateway.service"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

DRY_RUN=0
BIN_SRC=""

usage() {
	cat <<'EOF'
用法: install.sh [--bin PATH] [--dry-run] [--help]
  --bin PATH   指定待安装二进制（默认取仓库 bin/sms-gateway-linux-amd64 或 bin/sms-gateway）
  --dry-run    只打印将执行的动作，不改动系统
  --help       显示本帮助
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--bin)
		BIN_SRC="${2:-}"
		shift 2
		;;
	--dry-run | -n)
		DRY_RUN=1
		shift
		;;
	--help | -h)
		usage
		exit 0
		;;
	*)
		echo "未知参数: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

run() {
	printf '+ %s\n' "$*"
	if [ "${DRY_RUN}" -eq 0 ]; then
		"$@"
	fi
}

run_sh() {
	printf '+ %s\n' "$1"
	if [ "${DRY_RUN}" -eq 0 ]; then
		bash -c "$1"
	fi
}

# ---- 前置检查 ----
if [ "${DRY_RUN}" -eq 0 ] && [ "$(id -u)" -ne 0 ]; then
	echo "安装需要 root 权限，请用 sudo 执行（或先 --dry-run 预览）。" >&2
	exit 1
fi

if [ -z "${BIN_SRC}" ]; then
	for cand in "${REPO_DIR}/bin/sms-gateway-linux-amd64" "${REPO_DIR}/bin/sms-gateway"; do
		if [ -f "${cand}" ]; then
			BIN_SRC="${cand}"
			break
		fi
	done
fi
if [ ! -f "${BIN_SRC}" ]; then
	echo "找不到待安装二进制：${BIN_SRC:-<空>}" >&2
	echo "请先执行 make build-linux-amd64（或 make build-local），或用 --bin 指定。" >&2
	exit 1
fi

for f in "${SCRIPT_DIR}/config.example.yaml" "${SCRIPT_DIR}/sms-gateway.service"; do
	if [ ! -f "${f}" ]; then
		echo "缺少安装所需文件: ${f}" >&2
		exit 1
	fi
done

# 生成 base64 编码的 32 字节随机密钥（openssl 优先，回退 /dev/urandom）。
gen_data_key() {
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -base64 32
	else
		head -c 32 /dev/urandom | base64
	fi
}

# 旧版本支持 server.admin_cidrs（管理端来源网段白名单），该来源 IP 限制已移除。
# 新版引导配置使用严格字段解析，残留的未知键会导致进程启动失败，而本脚本升级时
# 又保留既有 config.yaml，故在重启前幂等剔除该键（兼容行内与 YAML 块列表两种写法）。
migrate_drop_admin_cidrs() {
	[ -f "${CONF_FILE}" ] || return 0
	grep -q '^[ \t]*admin_cidrs[ \t]*:' "${CONF_FILE}" || return 0
	if [ "${DRY_RUN}" -ne 0 ]; then
		echo "[dry-run] 将从 ${CONF_FILE} 剔除已废弃的 admin_cidrs 键"
		return 0
	fi
	echo "发现已废弃的 server.admin_cidrs，重启前从配置剔除: ${CONF_FILE}"
	local tmp
	tmp="$(mktemp)"
	awk '
function indent_of(ln, p) { p = match(ln, /[^ \t]/); return p ? p - 1 : 0 }
BEGIN { skip = 0; key_indent = -1 }
{
	line = $0
	# 块形式：继续丢弃比键更深缩进的 "- " 列表项，遇到同级/更浅行恢复输出。
	if (skip) {
		if (line ~ /^[ \t]+-[ \t]?/ && indent_of(line) > key_indent) {
			next
		}
		skip = 0
	}
	if (line ~ /^[ \t]*admin_cidrs[ \t]*:/) {
		key_indent = indent_of(line)
		rest = line
		sub(/^[ \t]*admin_cidrs[ \t]*:/, "", rest)
		sub(/^[ \t]+/, "", rest)
		sub(/[ \t]+$/, "", rest)
		if (rest == "" || rest ~ /^#/) {
			skip = 1
		}
		next
	}
	print line
}
' "${CONF_FILE}" >"${tmp}"
	install -m 0640 -o root -g "${SERVICE_GROUP}" "${tmp}" "${CONF_FILE}"
	rm -f "${tmp}"
}

echo "==> 二进制: ${BIN_SRC}"
echo "==> dry-run: ${DRY_RUN}"

# ---- 1. 系统用户/组 ----
if id "${SERVICE_USER}" >/dev/null 2>&1; then
	echo "用户 ${SERVICE_USER} 已存在，跳过创建"
else
	run_sh "useradd --system --no-create-home --home-dir '${DATA_DIR}' --shell /usr/sbin/nologin '${SERVICE_USER}'"
fi

# ---- 2. 目录 ----
run install -d -m 0750 -o root -g "${SERVICE_GROUP}" "${CONF_DIR}"
run install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${DATA_DIR}"

# ---- 3. 数据密钥环境文件（仅首装生成，绝不覆盖）----
if [ -f "${ENV_FILE}" ]; then
	echo "环境文件已存在，保留不动: ${ENV_FILE}"
else
	printf '+ 生成 %s（SMSGW_SECRETS_DATA_KEY=<随机 base64>，仅首装）\n' "${ENV_FILE}"
	if [ "${DRY_RUN}" -eq 0 ]; then
		KEY="$(gen_data_key)"
		TMP_ENV="$(mktemp)"
		cat >"${TMP_ENV}" <<EOF
# sms-gateway 通道密钥主密钥（base64 32 字节）。自动生成，请勿入库；
# 备份数据库时务必单独安全备份本值，丢失后已存通道密钥将无法解密。
SMSGW_SECRETS_DATA_KEY=${KEY}
EOF
		install -m 0640 -o root -g "${SERVICE_GROUP}" "${TMP_ENV}" "${ENV_FILE}"
		rm -f "${TMP_ENV}"
	fi
fi

# ---- 4. 引导配置（不覆盖本地改动）----
if [ -f "${CONF_FILE}" ]; then
	echo "配置文件已存在，保留不动: ${CONF_FILE}"
else
	run install -m 0640 -o root -g "${SERVICE_GROUP}" \
		"${SCRIPT_DIR}/config.example.yaml" "${CONF_FILE}"
fi

# ---- 4.1 升级迁移：剔除已移除的来源 IP 白名单键（严格 YAML 解析下残留会启动失败）----
migrate_drop_admin_cidrs

# ---- 5. 二进制（升级时原子替换）----
run install -m 0755 -o root -g root "${BIN_SRC}" "${BIN_TARGET}"

# ---- 6. systemd unit ----
run install -m 0644 -o root -g root \
	"${SCRIPT_DIR}/sms-gateway.service" "${UNIT_TARGET}"

if command -v systemctl >/dev/null 2>&1; then
	run systemctl daemon-reload
	run systemctl enable sms-gateway
	run systemctl restart sms-gateway
	if [ "${DRY_RUN}" -eq 0 ]; then
		sleep 1
		if systemctl is-active --quiet sms-gateway; then
			echo "==> sms-gateway 已启动（systemctl status sms-gateway 查看）"
		else
			echo "!! 服务未进入 active 状态，请检查: journalctl -u sms-gateway -n 50 --no-pager" >&2
			exit 1
		fi
	fi
else
	echo "!! 当前环境无 systemctl（容器/非 systemd 主机），已完成文件安装，未注册服务。"
fi

echo "==> 完成。WebUI 入口: http://<本机IP>:8080/  （监听端口见 ${CONF_FILE}）"
