[CmdletBinding()]
param(
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

function Invoke-Checked {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][scriptblock]$Command
    )

    Write-Output "`n== $Label =="
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Label 失败，退出码 $LASTEXITCODE"
    }
}

function Invoke-InDirectory {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][scriptblock]$Command
    )

    Push-Location -LiteralPath $Path
    try {
        & $Command
    } finally {
        Pop-Location
    }
}

$pnpm = if (Get-Command pnpm -ErrorAction SilentlyContinue) {
    { pnpm @args }
} elseif (Get-Command corepack -ErrorAction SilentlyContinue) {
    { corepack pnpm @args }
} else {
    throw "未找到 pnpm 或 corepack。"
}

$backend = Join-Path $root "backend"
Invoke-Checked "Go tests" { Invoke-InDirectory $backend { go test ./... } }
Invoke-Checked "Go vet" { Invoke-InDirectory $backend { go vet ./... } }

$admin = Join-Path $backend "admin-ui"
if (Test-Path -LiteralPath (Join-Path $admin "package.json")) {
    Invoke-Checked "Admin tests" { Invoke-InDirectory $admin { & $pnpm test } }
    if (-not $SkipBuild) {
        Invoke-Checked "Admin production build" { Invoke-InDirectory $admin { & $pnpm build } }
    }
}

$extension = Join-Path $root "extension"
Invoke-Checked "Extension tests" { Invoke-InDirectory $extension { & $pnpm test } }
Invoke-Checked "Extension typecheck" { Invoke-InDirectory $extension { & $pnpm typecheck } }
if (-not $SkipBuild) {
    Invoke-Checked "Extension production build" { Invoke-InDirectory $extension { & $pnpm build } }
}

Invoke-Checked "Cross-module security baseline" { & (Join-Path $PSScriptRoot "verify-security-baseline.ps1") }
Invoke-Checked "Product presentation baseline" { & (Join-Path $PSScriptRoot "verify-product-presentation.ps1") }
Write-Output "`n全部验证通过。"
