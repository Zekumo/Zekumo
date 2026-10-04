# 启动本机开发依赖(Postgres + Redis),运行在 WSL Ubuntu 内的 Docker 里。
# 没装 Docker Desktop 也能用;首次运行前需要在 WSL 里装好 docker(已装)。
#
# 注意:WSL 虚拟机在最后一个终端退出后会自动关闭,容器会跟着停,
# 所以这里挂了一个常驻 sleep 进程保活。

Start-Process -WindowStyle Hidden wsl -ArgumentList '-u','root','-d','Ubuntu','--','sleep','infinity'
Start-Sleep 2

wsl -u root -d Ubuntu -- sh -c @'
sysctl -w net.ipv4.ip_forward=1 >/dev/null
service docker start >/dev/null 2>&1
sleep 2
if ! docker inspect minicloud-pg >/dev/null 2>&1; then
  docker run -d --name minicloud-pg --restart unless-stopped --network host \
    -e POSTGRES_USER=minicloud -e POSTGRES_PASSWORD=minicloud -e POSTGRES_DB=minicloud \
    postgres:16-alpine
else
  docker start minicloud-pg
fi
if ! docker inspect minicloud-redis >/dev/null 2>&1; then
  docker run -d --name minicloud-redis --restart unless-stopped --network host redis:7-alpine
else
  docker start minicloud-redis
fi
sleep 2
docker ps --format '{{.Names}} {{.Status}}'
'@

$pg = Test-NetConnection -ComputerName localhost -Port 5432 -WarningAction SilentlyContinue
$rd = Test-NetConnection -ComputerName localhost -Port 6379 -WarningAction SilentlyContinue
Write-Host "Postgres(5432): $($pg.TcpTestSucceeded)  Redis(6379): $($rd.TcpTestSucceeded)"
