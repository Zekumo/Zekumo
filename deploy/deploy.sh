#!/usr/bin/env bash
# MiniCloud 自动部署脚本(在服务器上执行)。
#
# 流程:备份 → 拉取最新源码 → 构建镜像 → 重启服务 → 健康检查 → 失败自动回滚。
# 要求:服务器装有 docker compose v2 和 curl;脚本与 docker-compose.yml、.env 同目录。
#
# 用法:
#   ./deploy.sh                       # 部署默认目录 /opt/minicloud
#   ./deploy.sh /srv/minicloud        # 部署指定目录
#   VERSION=1.1.0 ./deploy.sh         # 指定版本号(默认取时间戳,便于回滚区分)
#   SKIP_BACKUP=1 ./deploy.sh         # 跳过部署前备份
#
# 源码更新方式:src/ 是 git 仓库则自动 git pull --ff-only;
# 否则检测 /opt/minicloud/ 下是否有新上传的 minicloud-src-<版本>.tar.gz
# (本机用 deploy/package.ps1 生成),有就自动解压进 src/ 再构建。
#
# 每次部署生成一个新镜像 tag(minicloud:<版本>),保留当前与上一个,
# 其余旧镜像自动清理。回滚是重新拉起上一个 tag,不碰数据卷。

set -euo pipefail

APP_DIR="${1:-/opt/minicloud}"
COMPOSE_FILE="$APP_DIR/docker-compose.yml"
LOG_FILE="${DEPLOY_LOG:-/var/log/minicloud-deploy.log}"
ENV_FILE="$APP_DIR/.env"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/readyz}"
READY_TIMEOUT="${READY_TIMEOUT:-180}"   # 秒

log() { echo "[$(date '+%F %T')] $*" | tee -a "$LOG_FILE"; }
fail() { log "错误: $*"; exit 1; }

cd "$APP_DIR"

# ---- 前置检查 ------------------------------------------------------------
[ -f "$COMPOSE_FILE" ] || fail "找不到 $COMPOSE_FILE,确认目录结构是否正确"
[ -f "$ENV_FILE" ]    || fail "找不到 $ENV_FILE(生产配置),拒绝空配置部署"
# 源码来源:src/ 目录存在,或上传了 minicloud-src-*.tar.gz(首次部署还没有 src/,
# 等第 2 步解压时创建)。两者都没有才拒绝。
if [ ! -d "$APP_DIR/src" ]; then
  ls -1 "$APP_DIR"/minicloud-src-*.tar.gz >/dev/null 2>&1 \
    || fail "找不到 $APP_DIR/src 源码目录,也没有源码压缩包可解压:先上传 minicloud-src-<版本>.tar.gz"
fi
command -v docker >/dev/null 2>&1 || fail "服务器未安装 docker"
docker compose version >/dev/null 2>&1 || fail "docker compose v2 不可用"

# .env 必填项校验:首次部署先跑 ./bootstrap.sh 生成随机凭据,
# 缺任何一项都直接中止 —— 宁可失败,也不要带默认弱口令跑起来。
check_env() {
  local v
  v="$(grep -E "^$1=" "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- || true)"
  [ -n "$v" ] || fail ".env 缺少 $1,先执行: ./bootstrap.sh"
}
for k in JWT_SECRET POSTGRES_PASSWORD ADMIN_PASSWORD BASE_URL; do check_env "$k"; done

# ---- 版本号 ---------------------------------------------------------------
OLD_VERSION="$(grep -E '^VERSION=' "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2 || true)"
OLD_VERSION="${OLD_VERSION:-1.0.0}"
# 未显式指定版本时,若存在 minicloud-src-<版本>.tar.gz 则从包名取版本,否则取时间戳
NEW_VERSION="${VERSION:-}"
if [ -z "$NEW_VERSION" ]; then
  LATEST_TAR="$(ls -1 "$APP_DIR"/minicloud-src-*.tar.gz 2>/dev/null | sort -V | tail -1 || true)"
  if [ -n "$LATEST_TAR" ]; then
    NEW_VERSION="$(basename "$LATEST_TAR" | sed -E 's/^minicloud-src-(.+)\.tar\.gz$/\1/')"
  else
    NEW_VERSION="$(date +%Y%m%d%H%M%S)"
  fi
fi
log "部署目录: $APP_DIR"
log "旧版本: $OLD_VERSION → 新版本: $NEW_VERSION"

# ---- 1. 部署前备份(可选,失败不阻塞部署) -----------------------------------
if [ "${SKIP_BACKUP:-0}" != "1" ] && [ -x "$APP_DIR/backup.sh" ]; then
  if COMPOSE_FILE="$(basename "$COMPOSE_FILE")" "$APP_DIR/backup.sh" >> "$LOG_FILE" 2>&1; then
    log "部署前备份完成"
  else
    log "警告: 部署前备份失败(继续部署,备份任务本应按 crontab 每日执行)"
  fi
fi

# ---- 2. 更新源码 -----------------------------------------------------------
# 优先方式:git 仓库则 git pull;否则看有没有新上传的源码压缩包,
# 有就解压进 src/(解压覆盖,不删除多余文件 —— 删除旧文件会造成构建缓存失效)。
STAMP_FILE="$APP_DIR/src/.source-stamp"
NEWEST_TAR="$(ls -1 "$APP_DIR"/minicloud-src-*.tar.gz 2>/dev/null | sort -V | tail -1 || true)"

if [ -d "$APP_DIR/src/.git" ]; then
  log "git pull --ff-only 拉取最新源码…"
  ( cd "$APP_DIR/src" && git pull --ff-only ) >> "$LOG_FILE" 2>&1 || fail "git pull 失败,中止部署"
elif [ -n "$NEWEST_TAR" ] && [ "$(basename "$NEWEST_TAR")" != "$(cat "$STAMP_FILE" 2>/dev/null || true)" ]; then
  log "解压新源码包 $(basename "$NEWEST_TAR") → src/ …"
  mkdir -p "$APP_DIR/src"
  tar -xzf "$NEWEST_TAR" -C "$APP_DIR/src" >> "$LOG_FILE" 2>&1 || fail "解压 $NEWEST_TAR 失败"
  printf '%s' "$(basename "$NEWEST_TAR")" > "$STAMP_FILE"
else
  log "没有新的源码包,直接使用 src/ 现有代码构建"
fi

# ---- 3. 构建新镜像 ----------------------------------------------------------
log "构建镜像 minicloud:$NEW_VERSION(首次构建需要几分钟)…"
VERSION="$NEW_VERSION" docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" build server \
  >> "$LOG_FILE" 2>&1 || fail "构建失败,服务保持原版本运行"

# ---- 4. 重启服务 ------------------------------------------------------------
log "拉起 minicloud:$NEW_VERSION…"
VERSION="$NEW_VERSION" docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d \
  >> "$LOG_FILE" 2>&1 || fail "启动失败,服务保持原版本运行"

# ---- 5. 健康检查 ------------------------------------------------------------
wait_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if body="$(curl -fsS --max-time 3 "$HEALTH_URL" 2>/dev/null)" \
       && [ -n "$body" ] && printf '%s' "$body" | grep -q '"ready":true'; then
      log "健康检查通过: $body"
      return 0
    fi
    sleep 3
  done
  return 1
}

if wait_ready; then
  :
else
  log "警告: 新版本 $NEW_VERSION 健康检查超时,自动回滚到 $OLD_VERSION…"
  VERSION="$OLD_VERSION" docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d \
    >> "$LOG_FILE" 2>&1 || fail "回滚也失败了,请手动检查: docker compose ps"
  if wait_ready; then
    fail "新版本不健康,已回滚到 $OLD_VERSION 且回滚后健康检查通过"
  else
    fail "新版本不健康,已回滚到 $OLD_VERSION,但回滚后健康检查仍失败,请手动排查"
  fi
fi

# ---- 6. 清理旧镜像(保留当前与上一个) ---------------------------------------
log "清理旧镜像…"
docker images "minicloud:*" --format '{{.Repository}}:{{.Tag}}' \
  | grep -vE "minicloud:(${NEW_VERSION}|${OLD_VERSION})" \
  | xargs -r docker rmi -f >> "$LOG_FILE" 2>&1 || log "警告: 旧镜像清理不完整"

# ---- 7. 完成 ---------------------------------------------------------------
log "部署完成: $OLD_VERSION → $NEW_VERSION"
log "状态:"
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps | tee -a "$LOG_FILE" || true
