param(
    [string]$OutputDirectory = (Join-Path $PSScriptRoot 'dist')
)
$ErrorActionPreference = 'Stop'
$taskPreviousLocation = Get-Location
$taskPreviousGOOS = $env:GOOS
$taskPreviousGOARCH = $env:GOARCH
$taskPreviousCGO = $env:CGO_ENABLED
try {
    Set-Location -LiteralPath $PSScriptRoot
    $taskOutput = [IO.Path]::GetFullPath($OutputDirectory)
    New-Item -ItemType Directory -Path $taskOutput -Force | Out-Null
    $env:CGO_ENABLED = '0'
    $taskTargets = @(
        @{ OS = 'windows'; Arch = 'amd64' },
        @{ OS = 'linux'; Arch = 'amd64' },
        @{ OS = 'linux'; Arch = 'arm64' }
    )
    $taskManifest = @()
    foreach ($taskTarget in $taskTargets) {
        $env:GOOS = $taskTarget.OS
        $env:GOARCH = $taskTarget.Arch
        $taskPlatform = "$($taskTarget.OS)-$($taskTarget.Arch)"
        $taskPlatformDir = Join-Path $taskOutput $taskPlatform
        New-Item -ItemType Directory -Path $taskPlatformDir -Force | Out-Null
        foreach ($taskCommand in @('weixin', 'relay', 'worker', 'maintenance', 'retry')) {
            $taskSuffix = if ($taskTarget.OS -eq 'windows') { '.exe' } else { '' }
            $taskBinary = Join-Path $taskPlatformDir ($taskCommand + $taskSuffix)
            & go build -mod=vendor -trimpath -buildvcs=false -ldflags '-s -w' -o $taskBinary "./cmd/$taskCommand"
            if ($LASTEXITCODE -ne 0) { throw "Build failed: $taskPlatform/$taskCommand" }
            $taskManifest += [ordered]@{
                file = "$taskPlatform/$taskCommand$taskSuffix"
                bytes = (Get-Item -LiteralPath $taskBinary).Length
                sha256 = (Get-FileHash -LiteralPath $taskBinary -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        }
        foreach ($taskDocument in @('README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md')) {
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot $taskDocument) -Destination $taskPlatformDir
        }
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'licenses/tencent-MIT.txt') -Destination $taskPlatformDir
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'vendor/github.com/skip2/go-qrcode/LICENSE') -Destination (Join-Path $taskPlatformDir 'go-qrcode-MIT.txt')
        Compress-Archive -Path (Join-Path $taskPlatformDir '*') -DestinationPath (Join-Path $taskOutput "wechat-go-assistant-$taskPlatform.zip") -Force
    }
    [IO.File]::WriteAllText((Join-Path $taskOutput 'manifest.json'), (ConvertTo-Json -InputObject $taskManifest -Depth 4), [Text.UTF8Encoding]::new($false))
    Write-Output "Built 15 binaries and 3 platform archives in $taskOutput"
} finally {
    $env:GOOS = $taskPreviousGOOS
    $env:GOARCH = $taskPreviousGOARCH
    $env:CGO_ENABLED = $taskPreviousCGO
    Set-Location -LiteralPath $taskPreviousLocation.Path
}
