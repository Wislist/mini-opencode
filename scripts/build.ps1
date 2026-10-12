[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Output = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$appSource = Get-Content -Raw (Join-Path $repoRoot "internal/app/app.go")
$versionMatch = [regex]::Match($appSource, '(?m)^var version = "([^"]+)"\r?$')
if (-not $versionMatch.Success) {
    throw "build.ps1: could not read the base version from internal/app/app.go"
}
$baseVersion = ($versionMatch.Groups[1].Value -split '-', 2)[0]
$gitCommand = Get-Command git -ErrorAction SilentlyContinue

function Invoke-GitText {
    param([string[]]$Arguments)

    if ($null -eq $gitCommand) {
        return $null
    }

    $result = & $gitCommand.Source -C $repoRoot @Arguments 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }
    return (($result | Out-String).Trim())
}

if ([string]::IsNullOrWhiteSpace($Version)) {
    $gitDir = Invoke-GitText -Arguments @("rev-parse", "--git-dir")
    if ([string]::IsNullOrWhiteSpace($gitDir)) {
        $Version = $baseVersion
    }
    else {
        $sha = Invoke-GitText -Arguments @("rev-parse", "--short=7", "HEAD")
        if ([string]::IsNullOrWhiteSpace($sha)) {
            $sha = "unknown"
        }

        & $gitCommand.Source -C $repoRoot diff --quiet 2>$null
        $workingTreeDirty = $LASTEXITCODE -ne 0
        & $gitCommand.Source -C $repoRoot diff --cached --quiet 2>$null
        $indexDirty = $LASTEXITCODE -ne 0
        $dirty = if ($workingTreeDirty -or $indexDirty) { "-dirty" } else { "" }

        $tag = Invoke-GitText -Arguments @("describe", "--tags", "--abbrev=0")
        $tagVersion = if ($tag -and $tag.StartsWith("v")) { $tag.Substring(1) } else { $tag }
        if ($tagVersion -eq $baseVersion) {
            $commitsText = Invoke-GitText -Arguments @("rev-list", "--count", "$tag..HEAD")
            $commits = if ($commitsText) { [int]$commitsText } else { 0 }
            if ($commits -eq 0 -and $dirty -eq "") {
                $Version = $baseVersion
            }
            elseif ($commits -eq 0) {
                $Version = "$baseVersion+$sha$dirty"
            }
            else {
                $Version = "$baseVersion-dev.$commits.g$sha$dirty"
            }
        }
        else {
            $Version = "$baseVersion-dev.g$sha$dirty"
        }
    }
}

if ([string]::IsNullOrWhiteSpace($Output)) {
    $Output = Join-Path $repoRoot "bin/mini-opencode.exe"
}
$outputDir = Split-Path -Parent $Output
if ($outputDir) {
    New-Item -ItemType Directory -Force $outputDir | Out-Null
}

Push-Location $repoRoot
try {
    & go build -ldflags "-X main.version=$Version" -o $Output ./cmd/mini-opencode
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

Write-Host "built $Output $Version"
