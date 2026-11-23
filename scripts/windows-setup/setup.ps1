# MindFS edge controller installer (Windows).
#
# Invoked by the self-extracting setup stub from its staging directory. Every
# path is passed in from the stub (internal/deploy/setup.go::SetupPaths) so this
# script never has to re-derive them; the scheduled task XML is likewise built
# and tested on the Go side (deploy.BuildSetupTaskXML) and only registered here.
#
# This script is embedded in the setup exe and MUST stay pure ASCII. Windows
# decodes .ps1 with the local code page (GBK on Chinese systems); a UTF-8 CJK
# comment swallows the next newline, silently merging the following statement
# into the comment. internal/deploy/install_script_test.go pins this.
#
# Order: stop -> copy -> register task -> PATH -> start -> health. Stopping
# first is what makes the copy succeed: the running service holds its own exe
# open and schtasks /create refuses a task that is still running.

param(
    [Parameter(Mandatory = $true)][string]$StageDir,
    [Parameter(Mandatory = $true)][string]$Prefix,
    [Parameter(Mandatory = $true)][string]$BinDir,
    [Parameter(Mandatory = $true)][string]$Binary,
    [Parameter(Mandatory = $true)][string]$ShareDir,
    [Parameter(Mandatory = $true)][string]$Agents,
    [Parameter(Mandatory = $true)][string]$Web,
    [Parameter(Mandatory = $true)][string]$TaskXml,
    [string]$TaskName = 'mindfs',
    [string]$Addr = '0.0.0.0:7332'
)

$ErrorActionPreference = 'Stop'

function Stop-RunningInstance([string]$Binary) {
    # First install: nothing to stop. The stub has already written config.json
    # by now, so use the process list rather than shelling out to the old
    # binary (which may be a version whose -stop flag did not exist yet).
    if (-not (Test-Path $Binary -PathType Leaf)) { return }
    $name = [System.IO.Path]::GetFileNameWithoutExtension($Binary)
    $running = @(Get-Process -Name $name -ErrorAction SilentlyContinue)
    if ($running.Count -eq 0) { return }
    Write-Host "Stopping running instance ($($running.Count) process(es))..."
    try { & $Binary -stop | Out-Null } catch { Write-Host "  mindfs -stop failed: $($_.Exception.Message)" }
    for ($i = 0; $i -lt 150; $i++) {
        if (-not (Get-Process -Name $name -ErrorAction SilentlyContinue)) { return }
        Start-Sleep -Milliseconds 200
    }
    # Still alive after 30 s: it is wedged. Kill it rather than fail the
    # upgrade half-way through, leaving a new binary and an old process.
    Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 500
}

function Install-Payload([string]$Stage, [string]$Prefix, [string]$Binary, [string]$Agents, [string]$Web) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Binary) | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Agents) | Out-Null

    $srcBin = Join-Path $Stage 'mindfs.exe'
    if (-not (Test-Path $srcBin -PathType Leaf)) { throw 'payload is missing mindfs.exe' }
    Copy-Item -Force $srcBin $Binary
    Write-Host "  Binary -> $Binary"

    $srcAgents = Join-Path $Stage 'agents.json'
    if (Test-Path $srcAgents -PathType Leaf) {
        Copy-Item -Force $srcAgents $Agents
        Write-Host "  Agents -> $Agents"
    }

    $srcWeb = Join-Path $Stage 'web'
    if (-not (Test-Path $srcWeb -PathType Container)) { throw 'payload is missing web\' }
    if (Test-Path $Web) { Remove-Item -Recurse -Force $Web }
    Copy-Item -Recurse $srcWeb $Web
    Write-Host "  Static -> $Web"

    # A binary with no page is a half install: the service comes up, GET / is a
    # 404, and nothing in the logs says why. Verify the anchors really landed.
    foreach ($anchor in @('index.html', 'favicon.svg')) {
        $p = Join-Path $Web $anchor
        if (-not (Test-Path $p -PathType Leaf)) { throw "web\$anchor did not land at $p" }
    }
    Write-Host "  Prefix -> $Prefix"
}

function Register-ScheduledTask([string]$TaskName) {
    if (-not (Test-Path $TaskXml -PathType Leaf)) { throw "task XML is missing: $TaskXml" }
    # schtasks /create /xml only accepts UTF-16LE. Writing UTF-8 reports "the
    # task XML is not valid", which says nothing about the encoding. The stub
    # emits UTF-16LE; never re-encode it here.
    & schtasks.exe /create /tn $TaskName /xml $TaskXml /f | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "schtasks /create failed with exit code $LASTEXITCODE" }
    Write-Host "  Task   -> \$TaskName"
}

function Add-UserPath([string]$Dir) {
    $want = $Dir.TrimEnd('\', '/')
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $current) { $current = '' }
    $segments = @($current -split ';' | Where-Object { $_ -and $_.Trim() })
    foreach ($seg in $segments) {
        # Compare case-insensitively and ignore trailing separators: Windows
        # paths are case-insensitive and "C:\x" == "C:\x\".
        if ($seg.TrimEnd('\', '/') -ieq $want) {
            Write-Host '  PATH   -> already present'
            return
        }
    }
    $next = (@($segments) + $Dir) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $next, 'User')
    $env:Path = "$Dir;$env:Path"
    Write-Host "  PATH   -> added $Dir"
}

function Start-AndWait([string]$TaskName, [string]$Addr) {
    & schtasks.exe /run /tn $TaskName | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "schtasks /run failed with exit code $LASTEXITCODE" }
    # A wildcard listen address is not dialable; probe loopback instead.
    $hostPart = '127.0.0.1'
    $portPart = $Addr
    $colon = $Addr.LastIndexOf(':')
    if ($colon -ge 0) { $portPart = $Addr.Substring($colon + 1) }
    $url = "http://${hostPart}:${portPart}/mindfs/health"
    for ($i = 0; $i -lt 100; $i++) {
        Start-Sleep -Milliseconds 300
        try {
            $resp = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 -Uri $url
            if ($resp.StatusCode -eq 200) {
                Write-Host "  Health -> $url is up"
                return
            }
        } catch { }
    }
    # Not fatal: the task is registered and will start on next boot/logon even
    # if the first launch lost a race. Say so instead of claiming success.
    Write-Warning "service did not answer $url within 30 s; check: schtasks /query /tn $TaskName"
}

Write-Host "MindFS edge controller -> $Prefix"

Stop-RunningInstance $Binary
Install-Payload $StageDir $Prefix $Binary $Agents $Web
Register-ScheduledTask $TaskName
Add-UserPath $BinDir
Start-AndWait $TaskName $Addr

Write-Host ''
Write-Host 'Done. Open http://127.0.0.1:7332/mindfs/ and pair with your primary.'
