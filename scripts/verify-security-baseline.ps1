[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$failures = [System.Collections.Generic.List[string]]::new()

function Read-WorkspaceText {
    param([Parameter(Mandatory)][string[]]$Paths)

    $content = foreach ($path in $Paths) {
        if (Test-Path -LiteralPath $path) {
            Get-Content -LiteralPath $path -Raw
        }
    }
    return ($content -join "`n")
}

function Assert-Absent {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Text,
        [Parameter(Mandatory)][string]$Pattern
    )

    if ($Text -match $Pattern) {
        $failures.Add("$Name：检测到禁止模式 /$Pattern/")
    }
}

function Assert-Present {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Text,
        [Parameter(Mandatory)][string]$Pattern
    )

    if ($Text -notmatch $Pattern) {
        $failures.Add("$Name：缺少必需模式 /$Pattern/")
    }
}

$backendSources = Get-ChildItem -LiteralPath (Join-Path $root "backend") -Recurse -File -Filter "*.go" |
    Where-Object { $_.Name -notlike "*_test.go" } |
    ForEach-Object FullName
$localDevCommandPath = [IO.Path]::GetFullPath((Join-Path $root "backend\cmd\fullpro-server\main.go"))
$productionBackendSources = $backendSources | Where-Object { [IO.Path]::GetFullPath($_) -ne $localDevCommandPath }
$backendText = Read-WorkspaceText -Paths $productionBackendSources
$localDevCommandText = Get-Content -LiteralPath $localDevCommandPath -Raw

$adminSourceRoot = Join-Path $root "backend\admin-ui\src"
$adminSources = if (Test-Path -LiteralPath $adminSourceRoot) {
    Get-ChildItem -LiteralPath $adminSourceRoot -Recurse -File -Include "*.ts", "*.tsx", "*.js", "*.jsx" |
        ForEach-Object FullName
} else {
    Get-ChildItem -LiteralPath (Join-Path $root "backend\internal\server\web\admin") -File -Include "*.js" |
        ForEach-Object FullName
}
$adminText = Read-WorkspaceText -Paths $adminSources

$manifestPath = Join-Path $root "extension\public\manifest.json"
$manifestText = Get-Content -LiteralPath $manifestPath -Raw
$manifest = $manifestText | ConvertFrom-Json

Assert-Absent -Name "生产后端不得内置固定管理员口令" -Text $backendText -Pattern '(?i)fixedAdminPassword|ensureFixedAdmin|2231'
Assert-Present -Name "本地 dev 命令必须显式标注固定测试口令" -Text $localDevCommandText -Pattern 'const\s+localDevelopmentPassword\s*=\s*"2231"'
Assert-Present -Name "本地 dev 命令必须显式开启弱口令例外" -Text $localDevCommandText -Pattern 'AllowWeakPassword:\s*true'
Assert-Present -Name "本地 dev 命令必须保持开发模式隔离" -Text $localDevCommandText -Pattern 'DevelopmentMode:\s*true'
Assert-Absent -Name "CORS 不得放行任意扩展 ID" -Text $backendText -Pattern 'HasPrefix\s*\(\s*origin\s*,\s*"chrome-extension://"'
Assert-Present -Name "后端必须提供版本化同步端点" -Text $backendText -Pattern '/api/v1/sync/profile'
Assert-Present -Name "后端必须提供同步历史恢复端点" -Text $backendText -Pattern '/api/v1/sync/profile/versions'
Assert-Present -Name "后端必须支持一次性安装码" -Text $backendText -Pattern 'FULLPRO_INSTALL_CODE|InstallCode'
Assert-Present -Name "后端必须发送 CSP" -Text $backendText -Pattern 'Content-Security-Policy'
Assert-Present -Name "后端必须发送 nosniff" -Text $backendText -Pattern 'X-Content-Type-Options'
Assert-Present -Name "同步写入必须执行版本前置条件" -Text $backendText -Pattern 'BaseVersion|baseVersion'
Assert-Present -Name "跨端 profile hash 必须使用显式 canonical JSON" -Text $backendText -Pattern 'writeExtensionCanonicalJSON'
Assert-Present -Name "旧版注册写入口必须强制升级" -Text $backendText -Pattern 'POST /api/auth/register"\s*,\s*a\.handleProtocolUpgradeRequired'
Assert-Present -Name "旧版配置恢复写入口必须强制升级" -Text $backendText -Pattern 'POST /api/profile/versions/\{id\}/restore"\s*,\s*a\.handleProtocolUpgradeRequired'

Assert-Absent -Name "后台源码不得使用 innerHTML" -Text $adminText -Pattern '(?i)\.innerHTML\s*='
$requiredHosts = @($manifest.host_permissions)
if ($requiredHosts -contains "http://*/*") {
    $failures.Add("扩展不得把所有 HTTP 主机列为必需权限")
}
if ($requiredHosts -contains "https://*/*") {
    $failures.Add("扩展不得把所有 HTTPS 主机列为必需权限；用户后端应按来源申请可选权限")
}

if ($failures.Count -gt 0) {
    Write-Error ("安全基线检查失败：`n- " + ($failures -join "`n- "))
}

Write-Output "安全基线检查通过（$($productionBackendSources.Count) 个生产后端源文件、1 个受限本地 dev 命令源文件，$($adminSources.Count) 个后台源文件）。"
