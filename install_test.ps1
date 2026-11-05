$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$temp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
$release = Join-Path $temp 'release'
$localAppData = Join-Path $temp 'localappdata'
New-Item -ItemType Directory -Path $release, $localAppData | Out-Null
$archive = Join-Path $release 'booxd_9.9.9_windows_amd64.zip'
$exe = Join-Path $release 'booxd.exe'
$source = Join-Path $temp 'main.go'
@'
package main
import "fmt"
func main() { fmt.Println("booxd 9.9.9 (windows/amd64)") }
'@ | Set-Content -LiteralPath $source -Encoding ascii
Push-Location $root
try {
    & go build -o $exe $source
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the installer test executable.' }
} finally {
    Pop-Location
}
Compress-Archive -LiteralPath $exe -DestinationPath $archive
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
"$hash  booxd_9.9.9_windows_amd64.zip" | Set-Content -LiteralPath (Join-Path $release 'SHA256SUMS') -Encoding ascii

$oldBase = $env:BOOXD_BASE_URL
$oldVersion = $env:BOOXD_VERSION
$oldBin = $env:BOOXD_BIN_DIR
$oldLocalAppData = $env:LOCALAPPDATA
$oldUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
try {
    $env:BOOXD_BASE_URL = $release
    $env:BOOXD_VERSION = '9.9.9'
    $env:BOOXD_BIN_DIR = ''
    $env:LOCALAPPDATA = $localAppData
    & (Join-Path $root 'install.ps1')
    & (Join-Path $root 'install.ps1')

    $installed = Join-Path $localAppData 'Programs\booxd\booxd.exe'
    if (-not (Test-Path -LiteralPath $installed -PathType Leaf)) {
        throw 'Installer did not create booxd.exe.'
    }
    $output = & $installed
    if ($LASTEXITCODE -ne 0 -or $output -ne 'booxd 9.9.9 (windows/amd64)') {
        throw "Installed executable returned unexpected output: $output"
    }
    $entries = @([Environment]::GetEnvironmentVariable('Path', 'User') -split ';' | Where-Object { $_ })
    $count = @($entries | Where-Object { [string]::Equals($_, (Split-Path -Parent $installed), [StringComparison]::OrdinalIgnoreCase) }).Count
    if ($count -ne 1) {
        throw "User PATH contains $count entries for the install directory; expected one."
    }
    Write-Output 'Windows installer checks passed.'
} finally {
    [Environment]::SetEnvironmentVariable('Path', $oldUserPath, 'User')
    $env:BOOXD_BASE_URL = $oldBase
    $env:BOOXD_VERSION = $oldVersion
    $env:BOOXD_BIN_DIR = $oldBin
    $env:LOCALAPPDATA = $oldLocalAppData
    Remove-Item -LiteralPath $temp -Recurse -Force
}
