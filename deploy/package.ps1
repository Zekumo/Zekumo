# 本机打包脚本(Windows PowerShell,无需额外安装)。
#
# 用途:你没有把源码上传到 git,部署需要把它压缩后传到服务器的
#       /opt/zekumo/,再让部署脚本解压进 src/ 构建镜像。
#
# 用法(在项目根目录 ./deploy 下执行):
#   powershell -ExecutionPolicy Bypass -File deploy\package.ps1
#   .\deploy\package.ps1                          # 版本取时间戳
#   .\deploy\package.ps1 -Version 1.1.0           # 指定版本号
#
# 产物:deploy\dist\zekumo-src-<版本>.tar.gz
#
# 排除项:.git、deploy(含 .env 密钥!)、backups、data、以及 *.exe / *.log。

param(
    [string]$Version = "",
    [string]$Src = "."
)

$ErrorActionPreference = "Stop"

if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
    Write-Error "未找到 tar.exe(Windows 10 1803+ 自带,装在 C:\Windows\system32\ 下)"
}
if (-not $Version) {
    $Version = (Get-Date).ToString("yyyyMMddHHmmss")
}

$root = (Resolve-Path $Src).Path
$outDir = Join-Path $root "deploy\dist"
New-Item -ItemType Directory -Path $outDir -Force | Out-Null
$out = Join-Path $outDir "zekumo-src-$Version.tar.gz"

# bsdtar 的 --exclude 用 fnmatch;`.git` / `deploy` 等显式列全,防止把密钥打进包里。
$excludes = @(
    "./.git",
    "./.git/*",
    "./deploy",
    "./deploy/*",
    "./backups",
    "./backups/*",
    "./data",
    "./data/*",
    "./*.exe",
    "./*.log",
    "./zekumo"
)

Write-Host "打包 $root -> $out ..."
$args = @("-czf", $out)
foreach ($e in $excludes) { $args += "--exclude=$e" }
$args += @("-C", $root, ".")

# CreateNoWindow + RedirectStandardOutput 关掉 tar 的进度输出,出错才能暴露。
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = "tar.exe"
$psi.Arguments = ($args | ForEach-Object { if ($_ -match " ") { '"' + $_ + '"' } else { $_ } }) -join " "
$psi.UseShellExecute = $false
$psi.CreateNoWindow = $true
$psi.RedirectStandardError = $true
$p = [System.Diagnostics.Process]::Start($psi)
$err = $p.StandardError.ReadToEnd()
$p.WaitForExit()
if ($p.ExitCode -ne 0) {
    Write-Error "tar 打包失败(exit $($p.ExitCode)):$err"
}

$sizeMB = [Math]::Round((Get-Item $out).Length / 1MB, 2)
Write-Host "完成:$out ($sizeMB MB)"
Write-Host ""
Write-Host "把它上传到服务器 /opt/zekumo/(和 deploy.sh 同目录),"
Write-Host "然后执行 ./deploy.sh 即可自动解压并部署。"