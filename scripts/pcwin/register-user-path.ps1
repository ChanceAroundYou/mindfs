# Add the MindFS bin directory to the current user's PATH (idempotent).
# Run through stdin so quoting never crosses the ssh / cmd boundary:
#   ssh pc 'powershell -NoProfile -Command -' < scripts/pcwin/register-user-path.ps1
$ErrorActionPreference = "Stop"
$BinDir = Join-Path $env:LOCALAPPDATA "Programs\mindfs\bin"
$current = [Environment]::GetEnvironmentVariable("Path", "User")
if ($null -eq $current) { $current = "" }
$segments = @($current -split ';' | Where-Object { $_ -and $_.Trim() -ne "" })
$target = $BinDir.TrimEnd('\')
$present = $segments | Where-Object { $_.TrimEnd('\') -ieq $target }
if ($present) {
    Write-Output "already present: $BinDir"
    exit 0
}
$next = (@($segments) + $BinDir) -join ';'
[Environment]::SetEnvironmentVariable("Path", $next, "User")
Write-Output "added to user PATH: $BinDir"
