# 造一份可以在控制台里看的演示数据:一个游戏、一个通行证账号、两个玩家,
# 外加存档、排行榜分数、剧情脚本和一个云函数,免得各个标签页都是空的。
#
#   .\scripts\seed-demo.ps1                     # 默认打到 localhost:8090
#   .\scripts\seed-demo.ps1 -BaseUrl http://...

param(
  [string]$BaseUrl  = "http://localhost:8090",
  [string]$AdminUser = "admin",
  [string]$AdminPass = "admin123"
)

$ErrorActionPreference = "Stop"

# Invoke-RestMethod 直接传字符串会按本地代码页编码,中文会变成问号;
# 显式转成 UTF-8 字节再发。
function Api($Method, $Path, $Body, $Token) {
  $headers = @{}
  if ($Token) { $headers["Authorization"] = "Bearer $Token" }
  $args = @{ Method = $Method; Uri = "$BaseUrl$Path"; Headers = $headers }
  if ($null -ne $Body) {
    $args.ContentType = "application/json; charset=utf-8"
    $args.Body = [System.Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Depth 10 -Compress))
  }
  Invoke-RestMethod @args
}

Write-Host "→ 管理员登录" -ForegroundColor Cyan
$admin = (Api POST "/admin/api/login" @{ username = $AdminUser; password = $AdminPass }).token

Write-Host "→ 创建游戏" -ForegroundColor Cyan
# 复用同名游戏,免得每次执行都多出一个重复条目。
$game = (Api GET "/admin/api/games" $null $admin).games | Where-Object { $_.name -eq "星海远征" } | Select-Object -First 1
if (-not $game) { $game = Api POST "/admin/api/games" @{ name = "星海远征" } $admin }

Write-Host "→ 注册通行证账号" -ForegroundColor Cyan
# 脚本要可以反复执行:账号已存在时直接登录,而不是报错退出。
try {
  $account = Api POST "/sso/api/register" @{ username = "tester"; password = "test1234"; nickname = "测试员" }
} catch {
  $account = Api POST "/sso/api/login" @{ username = "tester"; password = "test1234" }
}

Write-Host "→ 玩家登录(游客 / 账密 / 通行证)" -ForegroundColor Cyan
$alice = Api POST "/v1/auth/login" @{
  app_id = $game.app_id; provider = "guest"; device_id = "demo-device-alice"; nickname = "爱丽丝"
}
try {
  $bob = Api POST "/v1/auth/register" @{
    app_id = $game.app_id; username = "bob"; password = "bob12345"; nickname = "鲍勃"
  }
} catch {
  $bob = Api POST "/v1/auth/login" @{
    app_id = $game.app_id; provider = "password"; username = "bob"; password = "bob12345"
  }
}
$ticket = (Api POST "/sso/api/tickets" @{ app_id = $game.app_id } $account.token).ticket
$tester = Api POST "/v1/auth/login" @{ app_id = $game.app_id; provider = "sso"; ticket = $ticket }

Write-Host "→ 存档与排行榜" -ForegroundColor Cyan
Api PUT "/v1/player/data/save1" @{ level = 12; gold = 3480; inventory = @("长剑", "药水", "地图碎片") } $alice.token | Out-Null
Api PUT "/v1/player/data/save1" @{ level = 7;  gold = 920;  inventory = @("木弓") } $bob.token | Out-Null
Api PUT "/v1/player/profile" @{ profile = @{ title = "见习冒险者"; guild = "北境" } } $alice.token | Out-Null

Api POST "/v1/leaderboards/weekly/score" @{ score = 8600 } $alice.token  | Out-Null
Api POST "/v1/leaderboards/weekly/score" @{ score = 5200 } $bob.token    | Out-Null
Api POST "/v1/leaderboards/weekly/score" @{ score = 7100 } $tester.token | Out-Null

Write-Host "→ 剧情脚本" -ForegroundColor Cyan
Api PUT "/admin/api/games/$($game.id)/dialogues/chapter1.intro" @{
  title = "第一章 · 抵达港口"
  content = @(
    @{ speaker = "艾拉"; text = "你终于来了,船队等你很久了。" },
    @{ speaker = "旅人"; text = "路上遇到了点麻烦。" },
    @{ speaker = "艾拉"; text = "先上船,细节路上说。" }
  )
} $admin | Out-Null

Write-Host "→ 云函数(每日签到)" -ForegroundColor Cyan
$signin = @'
const uid = request.player.id;
const today = new Date().toISOString().slice(0, 10);
if (mc.playerdata.get(uid, 'last_signin') === today) ({ ok: false, reason: '今天已签到' })
else {
  mc.playerdata.set(uid, 'last_signin', today);
  const gold = (mc.playerdata.get(uid, 'gold') || 0) + 100;
  mc.playerdata.set(uid, 'gold', gold);
  ({ ok: true, gold: gold })
}
'@
Api PUT "/admin/api/games/$($game.id)/functions/daily-signin" @{
  code = $signin; enabled = $true; public = $false; cron_secs = 0
} $admin | Out-Null

# 让客户端日志页有内容
Api POST "/v1/logs" @{
  level = "error"; event = "crash"; message = "渲染器在第三关初始化失败"
  fields = @{ scene = "level3"; device = "Pixel 8" }
} $alice.token | Out-Null

Write-Host ""
Write-Host "完成。" -ForegroundColor Green
Write-Host "  控制台   $BaseUrl/admin/    $AdminUser / $AdminPass"
Write-Host "  游戏     星海远征"
Write-Host "  App ID   $($game.app_id)"
Write-Host "  通行证   tester / test1234"
Write-Host "  游戏账号 bob / bob12345"
