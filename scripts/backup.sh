#!/bin/sh
# Postgres 备份。Redis 里只有可重建的派生数据(排行榜从 scores 表重建、
# 限流计数本就短命),所以只备 Postgres。
#
#   ./scripts/backup.sh                    # 备份到 ./backups
#   BACKUP_DIR=/mnt/backup ./scripts/backup.sh
#   KEEP_DAYS=30 ./scripts/backup.sh
#
# 建议挂进 crontab(每天 3:17,避开整点):
#   17 3 * * * cd /srv/minicloud && ./scripts/backup.sh >> /var/log/minicloud-backup.log 2>&1
#
# 恢复:
#   gunzip -c backups/minicloud-20260726-031700.sql.gz | \
#     docker compose -f docker-compose.prod.yml exec -T postgres psql -U minicloud -d minicloud

set -eu

BACKUP_DIR="${BACKUP_DIR:-./backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$BACKUP_DIR/minicloud-$STAMP.sql.gz"

mkdir -p "$BACKUP_DIR"

# 写临时文件再改名:中途失败不会留下一个看起来完整、实际截断的备份。
TMP="$OUT.partial"
docker compose -f "$COMPOSE_FILE" exec -T postgres \
  pg_dump -U minicloud --clean --if-exists minicloud | gzip -9 > "$TMP"

# pg_dump 通过管道时退出码会被 gzip 吞掉,所以显式验证产物可解压且非空。
if ! gzip -t "$TMP" 2>/dev/null || [ ! -s "$TMP" ]; then
  rm -f "$TMP"
  echo "backup failed: dump is empty or corrupt" >&2
  exit 1
fi
mv "$TMP" "$OUT"
echo "backup ok: $OUT ($(du -h "$OUT" | cut -f1))"

# 过期清理放在验证成功之后:备份失败的那天不应该顺手删掉旧的。
find "$BACKUP_DIR" -name 'minicloud-*.sql.gz' -type f -mtime "+$KEEP_DAYS" -print -delete
