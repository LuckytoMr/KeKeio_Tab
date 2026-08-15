[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$failures = [System.Collections.Generic.List[string]]::new()
$textExtensions = [System.Collections.Generic.HashSet[string]]::new(
    [string[]]@(".css", ".go", ".html", ".js", ".json", ".jsx", ".md", ".ps1", ".sh", ".svg", ".ts", ".tsx", ".txt", ".yaml", ".yml"),
    [StringComparer]::OrdinalIgnoreCase
)

function Get-PresentationFiles {
    $targets = @(
        (Join-Path $root ".github\workflows"),
        (Join-Path $root "backend\admin-ui\index.html"),
        (Join-Path $root "backend\admin-ui\src"),
        (Join-Path $root "backend\cmd"),
        (Join-Path $root "backend\deploy"),
        (Join-Path $root "backend\internal\server"),
        (Join-Path $root "backend\README.md"),
        (Join-Path $root "extension\dist"),
        (Join-Path $root "extension\newtab.html"),
        (Join-Path $root "extension\options.html"),
        (Join-Path $root "extension\public"),
        (Join-Path $root "extension\src"),
        (Join-Path $root "extension\README.md"),
        (Join-Path $root "scripts"),
        (Join-Path $root "AGENTS.md"),
        (Join-Path $root "DESIGN.md"),
        (Join-Path $root "README.md")
    )

    foreach ($target in $targets) {
        if (-not (Test-Path -LiteralPath $target)) {
            continue
        }
        $item = Get-Item -LiteralPath $target
        if (-not $item.PSIsContainer) {
            if ($textExtensions.Contains($item.Extension)) {
                $item
            }
            continue
        }
        Get-ChildItem -LiteralPath $item.FullName -Recurse -File |
            Where-Object { $textExtensions.Contains($_.Extension) }
    }
}

$legacySpellings = @(
    ("KeKe" + "IO"),
    ("Keke" + "IO")
)

foreach ($file in Get-PresentationFiles | Sort-Object FullName -Unique) {
    $content = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($spelling in $legacySpellings) {
        if ($content.Contains($spelling, [StringComparison]::Ordinal)) {
            $relativePath = [IO.Path]::GetRelativePath($root, $file.FullName)
            $failures.Add("$relativePath 仍包含旧品牌拼写 $spelling")
        }
    }
}

$extensionAppPath = Join-Path $root "extension\src\newtab\App.tsx"
$extensionAppText = Get-Content -LiteralPath $extensionAppPath -Raw
if ($extensionAppText -match '<code>\s*\{\s*backendAuth\.baseUrl\s*\}\s*</code>') {
    $failures.Add("同步登录账号卡不得展示内部后端地址 backendAuth.baseUrl")
}

$brandMarkPaths = @(
    (Join-Path $root "backend\admin-ui\src\components\shell.tsx"),
    (Join-Path $root "backend\admin-ui\src\pages\auth.tsx"),
    (Join-Path $root "backend\internal\server\web\account\reset.html"),
    (Join-Path $root "backend\internal\server\web\account\verify.html")
)
$brandMarkText = ($brandMarkPaths | ForEach-Object { Get-Content -LiteralPath $_ -Raw }) -join "`n"
if ($brandMarkText -cmatch '>\s*KT\s*<') {
    $failures.Add("用户可见品牌标记不得恢复为旧缩写 KT")
}

$zhLocale = Get-Content -LiteralPath (Join-Path $root "extension\public\_locales\zh_CN\messages.json") -Raw | ConvertFrom-Json
$enLocale = Get-Content -LiteralPath (Join-Path $root "extension\public\_locales\en\messages.json") -Raw | ConvertFrom-Json
if ($zhLocale.appName.message -cne "kekeio" -or $enLocale.appName.message -cne "kekeio") {
    $failures.Add("扩展 Manifest 本地化名称必须固定为小写 kekeio")
}

$readmePath = Join-Path $root "README.md"
$readmeText = Get-Content -LiteralPath $readmePath -Raw
$previewRelativePath = "docs/images/kekeio-tab-preview.webp"
$previewPath = Join-Path $root ($previewRelativePath -replace "/", [IO.Path]::DirectorySeparatorChar)
if (-not $readmeText.Contains($previewRelativePath, [StringComparison]::Ordinal)) {
    $failures.Add("README 必须展示效果图 $previewRelativePath")
}
if (-not (Test-Path -LiteralPath $previewPath -PathType Leaf)) {
    $failures.Add("README 效果图不存在：$previewRelativePath")
} elseif ((Get-Item -LiteralPath $previewPath).Length -le 0) {
    $failures.Add("README 效果图为空：$previewRelativePath")
}

foreach ($publicEntry in @("CONTRIBUTING.md", "SECURITY.md")) {
    if (-not (Test-Path -LiteralPath (Join-Path $root $publicEntry) -PathType Leaf)) {
        $failures.Add("公开维护入口缺失：$publicEntry")
    }
}

if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ }
    exit 1
}

Write-Output "产品展示基线通过。"
