# build-windows.ps1 — Build SwiftDrop for Windows
# Run from the swiftdrop-windows directory.
#
# Prerequisites:
#   - Go 1.22+ (with CGO disabled for pure-Go builds, or MSVC for CGO)
#   - WebView2 runtime (pre-installed on Windows 10 21H2+ and Windows 11)

$ErrorActionPreference = "Stop"
$Version = git describe --tags --always 2>$null
if (-not $Version) { $Version = "dev" }

Write-Host "Building SwiftDrop for Windows ($Version)..."

# -H windowsgui prevents the console window from appearing for the GUI app.
$env:CGO_ENABLED = "0"
go build -ldflags "-s -w -H windowsgui -X main.version=$Version" -o SwiftDrop.exe .

if ($LASTEXITCODE -eq 0) {
    $size = (Get-Item SwiftDrop.exe).Length / 1MB
    Write-Host "Built SwiftDrop.exe ($([math]::Round($size, 1)) MB)"
} else {
    Write-Error "Build failed"
    exit 1
}
