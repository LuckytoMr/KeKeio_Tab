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
$serverCommandPath = [IO.Path]::GetFullPath((Join-Path $root "backend\cmd\fullpro-server\main.go"))
$productionBackendSources = $backendSources | Where-Object { [IO.Path]::GetFullPath($_) -ne $serverCommandPath }
$backendText = Read-WorkspaceText -Paths $productionBackendSources
$serverCommandText = Get-Content -LiteralPath $serverCommandPath -Raw

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
$directDockerText = Get-Content -LiteralPath (Join-Path $root "docker命令.txt") -Raw
$directTunnelEnvExample = Get-Content -LiteralPath (Join-Path $routerDeployRoot "cloudflared.env.example") -Raw
$publishWorkflowText = Get-Content -LiteralPath (Join-Path $root ".github\workflows\publish.yml") -Raw

Assert-Absent -Name "生产后端不得内置固定管理员口令" -Text $backendText -Pattern '(?i)fixedAdminPassword|ensureFixedAdmin|2231'
Assert-Present -Name "本地 dev 命令必须显式标注固定测试口令" -Text $serverCommandText -Pattern 'const\s+localDevelopmentPassword\s*=\s*"2231"'
Assert-Present -Name "本地 dev 命令必须显式开启弱口令例外" -Text $serverCommandText -Pattern 'AllowWeakPassword:\s*true'
Assert-Present -Name "本地 dev 命令必须保持开发模式隔离" -Text $serverCommandText -Pattern 'DevelopmentMode:\s*true'
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
Assert-Present -Name "一键安装器必须从只读命名卷传递 Tunnel Token" -Text $routerInstallerText -Pattern '\$\{TOKEN_VOLUME\}:/run/secrets:ro'
Assert-Present -Name "一键安装器必须让 cloudflared 使用默认 bridge" -Text $routerInstallerText -Pattern '--network\s+bridge'
Assert-Present -Name "一键安装器必须通过 Caddy 固定地址收窄可信代理" -Text $routerInstallerText -Pattern 'FULLPRO_TRUSTED_PROXIES=\$\{CADDY_IP\}/32'
Assert-Present -Name "一键安装器必须绑定真实 LAN 管理地址" -Text $routerInstallerText -Pattern '\$\{LAN_IP\}:8443:443/tcp'
Assert-Present -Name "一键安装器必须使用 Docker 数据命名卷" -Text $routerInstallerText -Pattern '\$\{DATA_VOLUME\}:/data'
Assert-Present -Name "一键安装器必须使用 Docker 备份命名卷" -Text $routerInstallerText -Pattern '\$\{BACKUP_VOLUME\}:/backups'
Assert-Present -Name "一键安装器必须通过命名卷提供 Caddyfile" -Text $routerInstallerText -Pattern '\$\{CADDYFILE_VOLUME\}:/etc/caddy:ro'

Assert-Absent -Name "直接模式示例不得包含 JWT 形态 Tunnel Token" -Text ($directDockerText + "`n" + $directTunnelEnvExample) -Pattern 'eyJ[A-Za-z0-9_-]{80,}'
Assert-Present -Name "直接模式必须从本地 env 文件读取 Tunnel Token" -Text $directDockerText -Pattern '--env-file\s+cloudflared\.env'
Assert-Present -Name "直接模式必须收窄 loopback 可信代理" -Text $directDockerText -Pattern 'FULLPRO_TRUSTED_PROXIES=127\.0\.0\.1/32'
Assert-Present -Name "直接模式必须固定管理 LAN" -Text $directDockerText -Pattern 'FULLPRO_ADMIN_ALLOWED_CIDRS=127\.0\.0\.1/32,::1/128,192\.168\.50\.0/24'
Assert-Present -Name "直接模式只能在固定 LAN IP 发布后端" -Text $directDockerText -Pattern '192\.168\.50\.1:9009:9009/tcp'
Assert-Present -Name "直接模式必须显式开启 LAN HTTP 管理" -Text $directDockerText -Pattern 'FULLPRO_ALLOW_INSECURE_ADMIN_HTTP=true'
Assert-Present -Name "直接模式必须使用应用数据命名卷" -Text $directDockerText -Pattern 'kekeio-tab-data:/data'
Assert-Present -Name "直接模式必须共享经过约束的应用网络命名空间" -Text $directDockerText -Pattern '--network\s+container:kekeio-tab'
Assert-Present -Name "生产入口必须读取 LAN HTTP 显式开关" -Text $serverCommandText -Pattern 'envBool\("FULLPRO_ALLOW_INSECURE_ADMIN_HTTP",\s*false\)'
Assert-Present -Name "生产入口必须把 LAN HTTP 显式开关传入服务配置" -Text $serverCommandText -Pattern 'AllowInsecureAdminHTTP:\s+allowInsecureAdminHTTP'
Assert-Absent -Name "CI 不得重新消耗 Actions Artifact 配额" -Text $publishWorkflowText -Pattern 'actions/upload-artifact'
Assert-Present -Name "main 构建必须覆盖滚动 Release" -Text $publishWorkflowText -Pattern 'release_tag="main-latest"'
Assert-Present -Name "CI 必须在 Actions 摘要提供下载链接" -Text $publishWorkflowText -Pattern 'GITHUB_STEP_SUMMARY'

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
