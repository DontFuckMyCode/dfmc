#Requires -Version 5.1

param(
    [ValidateSet("build", "build-cgo", "all")]
    [string]$Action = "all"
)

function Get-Version {
    try {
        $tag = git describe --tags --always --dirty 2>$null
        if ($tag) { return $tag }
    } catch {}
    return "dev"
}

function Build-Go {
    param([bool]$EnableCgo)

    $cgoVal = if ($EnableCgo) { "1" } else { "0" }
    $outDir = "bin"
    $outExe = "dfmc.exe"

    if (-not (Test-Path $outDir)) {
        New-Item -ItemType Directory -Path $outDir | Out-Null
    }

    $cgoLabel = if ($EnableCgo) { "ON" } else { "OFF" }
    Write-Host "==> Building dfmc (CGO=$cgoLabel)..." -ForegroundColor Cyan

    $env:CGO_ENABLED = $cgoVal
    if ($env:GOFLAGS) { Remove-Item Env:\GOFLAGS -ErrorAction SilentlyContinue }

    $version = Get-Version
    $ldflags = "-s -w -X main.version=$version"

    go build -ldflags "$ldflags" -o "$outDir\$outExe" ./cmd/dfmc

    Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue

    if ($LASTEXITCODE -eq 0) {
        $size = (Get-Item "$outDir\$outExe").Length / 1MB
        Write-Host "Done. Output: $outDir\$outExe ({0:N2} MB)" -f $size -ForegroundColor Green
    } else {
        Write-Host "Build failed with exit code $LASTEXITCODE" -ForegroundColor Red
        exit $LASTEXITCODE
    }
}

switch ($Action) {
    "build"      { Build-Go -EnableCgo $false }
    "build-cgo" { Build-Go -EnableCgo $true }
    "all"       { Build-Go -EnableCgo $false; Build-Go -EnableCgo $true }
}