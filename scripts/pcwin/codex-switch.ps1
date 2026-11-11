# MindFS helper: switch the local Codex runtime config between shared profiles.
#
#   codex-switch            show current profile / runtime state
#   codex-switch default    switch to the shared "default" profile (official subscription)
#   codex-switch llmux      switch to the shared "llmux" profile
#
# The switch is executed by the running MindFS service over its local API, so it
# reuses the same validation, rollback and agent-restart logic as the web UI.
# Keep this file pure ASCII: Windows PowerShell reads scripts with the ANSI code
# page, and non-ASCII bytes can corrupt line endings.
param(
    [Parameter(Position = 0)][string]$Action = "status",
    [string]$Addr = ""
)
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$MindfsExe = Join-Path $ScriptDir "mindfs.exe"
if (-not (Test-Path $MindfsExe)) {
    $found = Get-Command mindfs.exe -ErrorAction SilentlyContinue
    if ($found) { $MindfsExe = $found.Source }
}
if (-not $MindfsExe) {
    Write-Error "mindfs.exe not found: put codex-switch next to it or add its bin directory to PATH"
    exit 2
}

function Resolve-ServiceAddr {
    if ($Addr) { return $Addr }
    $configPath = Join-Path $env:APPDATA "mindfs\config.json"
    if (Test-Path $configPath) {
        try {
            $config = Get-Content $configPath -Raw | ConvertFrom-Json
            if ($config.addr) {
                $value = ([string]$config.addr) -replace '^https?://', ''
                $value = $value -replace '^0\.0\.0\.0:', '127.0.0.1:'
                $value = $value -replace '^\[::\]:', '127.0.0.1:'
                return $value
            }
        } catch { }
    }
    return "127.0.0.1:7332"
}

function Get-ServiceBase {
    return "http://$(Resolve-ServiceAddr)"
}

function Show-RuntimeState {
    $markerPath = Join-Path $env:APPDATA "mindfs\agents-config-sync\active-profile"
    if (Test-Path $markerPath) {
        Write-Host ("active profile: " + (Get-Content $markerPath -Raw).Trim())
    } else {
        Write-Host "active profile: (unset)"
    }
    $runtimePath = Join-Path $env:USERPROFILE ".codex\config.toml"
    if (Test-Path $runtimePath) {
        $provider = Select-String -Path $runtimePath -Pattern '^model_provider\s*=' | Select-Object -First 1
        if ($provider) {
            Write-Host ("runtime provider: " + $provider.Line.Trim())
        } else {
            Write-Host "runtime provider: (none: official subscription)"
        }
    } else {
        Write-Host "runtime provider: (config.toml missing)"
    }
    $base = Get-ServiceBase
    foreach ($prefix in @("/mindfs", "")) {
        try {
            $sync = Invoke-RestMethod -Method Get -Uri "$base$prefix/api/agent-config/sync" -TimeoutSec 10
            Write-Host ("last sync state: " + $sync.state + " - " + $sync.message)
            if ($sync.last_attempt_at) { Write-Host ("last sync attempt: " + $sync.last_attempt_at) }
            break
        } catch { }
    }
    if ([Environment]::GetEnvironmentVariable("LLMUX_API_KEY", "User")) {
        Write-Host "LLMUX_API_KEY in user environment: present"
    } else {
        Write-Host "LLMUX_API_KEY in user environment: absent (only MindFS-managed agents get it)"
    }
}

switch ($Action.ToLowerInvariant()) {
    "status" { Show-RuntimeState; exit 0 }
    "default" { $target = "shared:codex-default" }
    "official" { $target = "shared:codex-default" }
    "llmux" { $target = "shared:codex-llmux" }
    default {
        Write-Host "usage: codex-switch [status|default|llmux]"
        exit 2
    }
}

$serviceAddr = Resolve-ServiceAddr
$body = @{ agent = "codex"; id = $target; confirm_overwrite = $false } | ConvertTo-Json
$response = $null
foreach ($prefix in @("/mindfs", "")) {
    $endpoint = "http://$serviceAddr$prefix/api/agent-config/switch"
    try {
        $response = Invoke-RestMethod -Method Post -Uri $endpoint -ContentType "application/json" -Body $body -TimeoutSec 30
        break
    } catch {
        if ($prefix -eq "") {
            Write-Error "switch request failed: $($_.Exception.Message)"
            exit 1
        }
    }
}
if ($null -eq $response) {
    Write-Error "switch request failed: no response"
    exit 1
}
if ($response.needs_confirm) {
    Write-Error "server asked for confirmation; switch from the MindFS web UI instead"
    exit 1
}
Write-Host "switched to $target"
if ($target -eq "shared:codex-llmux" -and -not [Environment]::GetEnvironmentVariable("LLMUX_API_KEY", "User")) {
    Write-Host "note: the Codex desktop app needs LLMUX_API_KEY in its own environment."
    Write-Host "      MindFS injects it only into agent processes it starts."
}
Show-RuntimeState
