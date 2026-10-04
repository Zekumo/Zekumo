#!/bin/sh
# 把源站锁成「只有 Cloudflare 能访问 80/443」。
#
# 为什么必须做:源站 IP 一旦泄露(扫描、历史 DNS、邮件头都可能泄露),
# 攻击者可以绕过 Cloudflare 直连源站 —— WAF、限流、DDoS 防护全部作废。
# 更要命的是,配置里开了 TRUST_PROXY,应用会信任 X-Forwarded-For;
# 源站不锁的话,任何人都能伪造这个头,把按 IP 限流整个绕过去。
#
# 用法(服务器上以 root 执行):
#   sh lock-origin.sh            # 应用规则
#   sh lock-origin.sh --dry-run  # 只打印将要执行的命令
#
# Cloudflare 的 IP 段会变(不常见),官方建议定期刷新。可以每月跑一次:
#   0 4 1 * * /opt/zekumo/lock-origin.sh >> /var/log/mc-lock-origin.log 2>&1

set -eu

DRY=""
[ "${1:-}" = "--dry-run" ] && DRY="echo [dry-run]"

command -v ufw >/dev/null || { echo "未安装 ufw:apt-get install -y ufw" >&2; exit 1; }

echo "拉取 Cloudflare IP 段…"
V4=$(curl -fsS --max-time 20 https://www.cloudflare.com/ips-v4)
V6=$(curl -fsS --max-time 20 https://www.cloudflare.com/ips-v6)

# 拉取失败就中止:宁可不改规则,也不要因为拿到空列表而把自己关在外面,
# 或者放行一个空集合导致 Cloudflare 也进不来。
[ -n "$V4" ] || { echo "Cloudflare IPv4 列表为空,中止" >&2; exit 1; }

# SSH 先放行,顺序很重要 —— 先启用防火墙再放行 SSH 会把自己踢下线。
$DRY ufw allow 22/tcp comment 'ssh'

# 清掉上一次写的 Cloudflare 规则,避免累积成几百条。
ufw status numbered 2>/dev/null | grep -i cloudflare | sed -E 's/^\[ *([0-9]+)\].*/\1/' \
  | sort -rn | while read -r n; do $DRY ufw --force delete "$n"; done

for ip in $V4 $V6; do
  $DRY ufw allow from "$ip" to any port 80 proto tcp comment 'cloudflare'
  $DRY ufw allow from "$ip" to any port 443 proto tcp comment 'cloudflare'
done

$DRY ufw default deny incoming
$DRY ufw default allow outgoing
$DRY ufw --force enable

echo
echo "完成。当前规则:"
$DRY ufw status verbose | head -20
echo
echo "验证:从别的机器 curl http://103.79.185.53/ 应该超时,"
echo "      而 https://panel.mn1.top 正常 —— 那就说明只有 Cloudflare 进得来。"
