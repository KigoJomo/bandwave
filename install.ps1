# bandwave installer for Windows.
#   irm https://raw.githubusercontent.com/KigoJomo/bandwave/main/install.ps1 | iex
#
# Puts bandwave in %LOCALAPPDATA%\Programs\bandwave, adds it to your PATH and
# starts the agent at logon. Run from an admin PowerShell to also open the
# firewall for other PCs. Set $env:BANDWAVE_NO_AGENT = 1 to skip the agent.
$ErrorActionPreference = 'Stop'
$repo = 'KigoJomo/bandwave'
$dir = Join-Path $env:LOCALAPPDATA 'Programs\bandwave'
$zip = Join-Path $env:TEMP 'bandwave-windows-amd64.zip'

Write-Host 'Downloading bandwave...'
Invoke-WebRequest -UseBasicParsing -OutFile $zip "https://github.com/$repo/releases/latest/download/bandwave-windows-amd64.zip"

# Stop a running agent so its exe can be replaced.
Get-Process bandwave, bandwave-agent -ErrorAction SilentlyContinue | Stop-Process -Force
New-Item -ItemType Directory -Force $dir | Out-Null
Expand-Archive -Force $zip $dir
Remove-Item $zip

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$dir").TrimStart(';'), 'User')
    $env:Path += ";$dir"
    Write-Host "Added $dir to your PATH (new terminals pick it up)."
}
Write-Host "Installed $dir\bandwave.exe"

if ($env:BANDWAVE_NO_AGENT -ne '1') {
    & "$dir\bandwave.exe" install
}
Write-Host 'Done. Run: bandwave'
