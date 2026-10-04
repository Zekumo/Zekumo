#!/usr/bin/env bash
# Zekumo 首次部署引导(在服务器上执行一次)。
#
# 职责:生成 /opt/zekumo/.env,随机生成 JWT_SECRET / POSTGRES_PASSWORD /
#       ADMIN_PASSWORD 等凭据,并收集对外域名 BASE_URL。
#       密钥只在服务器上生成,不经过本机、不进任何压缩包。
#
# 幂等:已存在的 .env 只补缺失项,已填的值绝不覆盖。重复执行安全。
#
# 用法(上传 docker-compose.yml、lock-origin.sh、deploy.sh、本脚本之后):
#   cd /opt/zekumo && chmod +x bootstrap.sh && ./bootstrap.sh
#
# 非交互(自动化/CI):
#   BASE_URL=https://panel.mn1.top ./bootstrap.sh
#
# 生成的密码会打印在结尾,复制到自己密码管理器里,之后随时可从 .env 查
#   grep ADMIN_PASSWORD /opt/zekumo/.env

set -euo pipefail

APP_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="$APP_DIR/.env"
BASE_URL="${BASE_URL:-}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
JWT_SECRET="${JWT_SECRET:-}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-}"

command -v openssl >/dev/null 2>&1 || { echo "服务器缺少 openssl:apt-get install -y openssl" >&2; exit 1; }

current() { # key → 当前 .env 里该变量的值(无则空)
  grep -E "^$1=" "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- || true
}

set_env() { # key value → 删除旧键(如有)再写入,保证不重复
  local key="$1" val="$2"
  if [ -f "$ENV_FILE" ]; then
    sed -i "/^${key}=/d" "$ENV_FILE"
  else
    : > "$ENV_FILE"
  fi
  printf '%s=%s\n' "$key" "$val" >> "$ENV_FILE"
}

# 某个必须项:已有非空值就保留,否则写入默认值
req() { # key default(cur 为空时用)
  local key="$1" def="$2" cur
  cur="$(current "$key")"
  [ -n "$cur" ] || set_env "$key" "$def"
}

echo "部署目录: $APP_DIR"

# ---- 基础项 ---------------------------------------------------------------
req ZEKUMO_ENV "production"
req ADMIN_USERNAME "$ADMIN_USERNAME"

# ---- 随机凭据(openssl 生成;同名环境变量传入则优先生效,不覆盖已有值) -------------
if [ -z "$(current JWT_SECRET)" ]; then
  set_env JWT_SECRET "${JWT_SECRET:-$(openssl rand -hex 32)}"
fi
if [ -z "$(current POSTGRES_PASSWORD)" ]; then
  set_env POSTGRES_PASSWORD "${POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
fi
if [ -z "$(current ADMIN_PASSWORD)" ]; then
  set_env ADMIN_PASSWORD "${ADMIN_PASSWORD:-$(openssl rand -base64 18 | tr -d '/+=')}"
fi

# ---- 依赖连接串 ------------------------------------------------------------
pgp="$(current POSTGRES_PASSWORD)"
req DATABASE_URL "postgres://zekumo:${pgp}@postgres:5432/zekumo"
req REDIS_ADDR "redis:6379"

# ---- 对外域名(生产必填,填 localhost 客户端下载不到更新产物) -------------------
ensure_base_url() {
  local cur
  cur="$(current BASE_URL)"
  if [ -n "$cur" ] && [ "$cur" != "http://localhost:8080" ]; then return; fi
  if [ -z "$BASE_URL" ]; then
    if [ -t 0 ]; then
      printf '对外域名(生产必填,含 https://,例如 https://panel.mn1.top): '
      read -r BASE_URL
    else
      echo "非交互运行必须设置 BASE_URL(如 BASE_URL=https://panel.mn1.top ./bootstrap.sh)" >&2
      exit 1
    fi
  fi
  set_env BASE_URL "$BASE_URL"
}
ensure_base_url

# ---- 其余默认项(缺才补) ------------------------------------------------------
req TRUST_PROXY "true"
req STORAGE_DRIVER "local"
req LOCAL_DATA_DIR "/data"
req MAX_ARTIFACT_SIZE "2147483648"
req STATS_TZ "Asia/Shanghai"
req LOG_RETENTION_DAYS "14"
req LOG_HTTP_ALL "false"
req TOKEN_TTL "168h"
req VERSION "1.0.0"
req FUNC_HTTP_ALLOW_PRIVATE "false"
req ZEKUMO_ADDR ":8080"

chmod 600 "$ENV_FILE"

echo
echo "生成完成:$ENV_FILE"
echo "──── 凭据(只显示这一次,记好) ────"
echo "  控制台入口 : $(current BASE_URL)/admin/"
echo "  用户名     : $(current ADMIN_USERNAME)"
echo "  密码       : $(current ADMIN_PASSWORD)"
echo "  随时可再查 : grep -E 'ADMIN_(USERNAME|PASSWORD)' $ENV_FILE"
echo
echo "下一步:屏幕输出里记住 BASE_URL 要和 Cloudflare 上的域名一致,"
echo "       然后: ./lock-origin.sh && ./deploy.sh"