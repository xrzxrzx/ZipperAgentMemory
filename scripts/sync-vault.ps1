# sync-vault.ps1 — 密码薄一键同步（方案 A）
# 用法：改完 D:\个人信息\密码薄.xlsx 后运行本脚本（或双击）
# 流程：转 CSV → 上传服务器 → 服务器自动提交+推送 Gitee
# 前置：minichat-server SSH 密钥认证；Python3 + openpyxl
$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$csv = "D:\Works\ZipperAgentMemory\memory\structured\password-vault.csv"

Write-Host "=== 1. 解析 xlsx 并生成 CSV ==="
$pyOut = & python "$scriptDir\convert-vault.py"
if ($LASTEXITCODE -ne 0) { throw "xlsx 解析失败" }
Write-Host $pyOut

Write-Host "=== 2. 上传服务器 ==="
scp -o BatchMode=yes $csv minichat-server:/opt/zipper-agent-memory/memory/structured/password-vault.csv
if ($LASTEXITCODE -ne 0) { throw "scp 上传失败" }

Write-Host "=== 3. 服务器提交 + 推送 Gitee ==="
ssh -o BatchMode=yes minichat-server "cd /opt/zipper-agent-memory && ./zipper-agent-memoryd git-commit -root memory 2>&1 | tail -1 && cd memory && git push gitee master 2>&1 | tail -1"

Write-Host ""
Write-Host "OK - vault synced"
