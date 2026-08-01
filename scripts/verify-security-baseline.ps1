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
$routerDeployRoot = Join-Path $root "backend\deploy\router"
$tunnelComposeText = Read-WorkspaceText -Paths @(
    (Join-Path $routerDeployRoot "compose.tunnel.yaml"),
    (Join-Path $routerDeployRoot "compose.tunnel-simpledocker.yaml")
)
$tunnelCaddyText = Get-Content -LiteralPath (Join-Path $routerDeployRoot "Caddyfile.tunnel") -Raw
$tunnelPublicCaddyText = ($tunnelCaddyText -split '# 管理入口只监听', 2)[0]
$routerInstallerText = Get-Content -LiteralPath (Join-Path $routerDeployRoot "install.sh") -Raw

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
Assert-Absent -Name "Tunnel 不得共享其他容器网络命名空间" -Text $tunnelComposeText -Pattern 'network_mode:\s*container:'
Assert-Absent -Name "Tunnel Compose 不得保存明文 Token" -Text $tunnelComposeText -Pattern 'eyJ[A-Za-z0-9_-]{80,}'
Assert-Absent -Name "Tunnel Compose 不得通过普通环境变量注入 Token" -Text $tunnelComposeText -Pattern '(?m)^\s*(TUNNEL_TOKEN|CLOUDFLARE_TUNNEL_TOKEN)\s*:'
Assert-Present -Name "Tunnel 必须从只读文件读取 Token" -Text $tunnelComposeText -Pattern '--token-file'
Assert-Present -Name "Tunnel 必须配置随机源站 Host 鉴权值" -Text $tunnelComposeText -Pattern 'KEKEIO_TUNNEL_ORIGIN_HOST'
Assert-Present -Name "Tunnel 必须使用独立的 Caddy 公网监听器" -Text $tunnelCaddyText -Pattern '(?m)^:8081\s*\{'
Assert-Present -Name "Tunnel 公网监听器必须校验随机源站 Host" -Text $tunnelPublicCaddyText -Pattern 'host\s+\{\$KEKEIO_TUNNEL_ORIGIN_HOST\}'
Assert-Present -Name "Tunnel 公网监听器必须有默认拒绝" -Text $tunnelPublicCaddyText -Pattern 'respond\s+404'
Assert-Absent -Name "Tunnel 公网监听器不得包含管理路由" -Text $tunnelPublicCaddyText -Pattern '/admin|/install|/api/admin/'
Assert-Absent -Name "一键安装器不得共享容器网络命名空间" -Text $routerInstallerText -Pattern '(?i)--network\s+container:'
Assert-Absent -Name "一键安装器不得通过环境变量传递 Tunnel Token" -Text $routerInstallerText -Pattern '(?i)(-e|--env)\s+[^\r\n]*(TUNNEL_TOKEN|CLOUDFLARE_TUNNEL_TOKEN)'
Assert-Absent -Name "一键安装器不得挂载 Docker socket" -Text $routerInstallerText -Pattern '/var/run/docker\.sock'
Assert-Absent -Name "一键安装器不得在线拉取浮动镜像" -Text $routerInstallerText -Pattern '(?i)docker\s+pull|cloudflared:latest'
Assert-Present -Name "一键安装器必须从离线包加载镜像" -Text $routerInstallerText -Pattern 'docker\s+load\s+-i'
Assert-Present -Name "一键安装器必须从只读文件传递 Tunnel Token" -Text $routerInstallerText -Pattern 'cloudflare-tunnel-token:ro'
Assert-Present -Name "一键安装器必须让 cloudflared 使用默认 bridge" -Text $routerInstallerText -Pattern '--network\s+bridge'
Assert-Present -Name "一键安装器必须通过 Caddy 固定地址收窄可信代理" -Text $routerInstallerText -Pattern 'FULLPRO_TRUSTED_PROXIES=\$\{CADDY_IP\}/32'
Assert-Present -Name "一键安装器必须绑定真实 LAN 管理地址" -Text $routerInstallerText -Pattern '\$\{LAN_IP\}:8443:443/tcp'

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
