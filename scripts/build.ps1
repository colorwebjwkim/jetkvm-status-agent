param([string]$Go = 'go')
$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
$oldOS = $env:GOOS
$oldArch = $env:GOARCH
$oldARM = $env:GOARM
$oldCGO = $env:CGO_ENABLED
try {
    & $Go test ./...
    if ($LASTEXITCODE) { throw 'Tests failed' }
    & $Go vet ./...
    if ($LASTEXITCODE) { throw 'Vet failed' }
    New-Item -ItemType Directory -Force dist | Out-Null
    $env:CGO_ENABLED = '0'
    foreach ($target in @(@('windows','amd64',''),@('linux','arm','7'),@('linux','arm64',''))) {
        $env:GOOS = $target[0]; $env:GOARCH = $target[1]; $env:GOARM = $target[2]
        $suffix = if ($target[0] -eq 'windows') { '.exe' } else { '' }
        & $Go build -trimpath -ldflags '-s -w' -o "dist/jetkvm-status-agent-$($target[0])-$($target[1])$suffix" ./cmd/jetkvm-status-agent
        if ($LASTEXITCODE) { throw 'Build failed' }
    }
    Get-FileHash dist/jetkvm-status-agent-* -Algorithm SHA256
} finally {
    $env:GOOS=$oldOS; $env:GOARCH=$oldArch; $env:GOARM=$oldARM; $env:CGO_ENABLED=$oldCGO
    Pop-Location
}
