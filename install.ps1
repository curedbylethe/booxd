$ErrorActionPreference = 'Stop'

$version = $env:BOOXD_VERSION
if (-not $version) {
    $version = (Invoke-RestMethod 'https://api.github.com/repos/curedbylethe/booxd/releases/latest').tag_name
}
if (-not $version) {
    throw 'Could not determine the booxd release version.'
}
$assetVersion = $version -replace '^v', ''
$tag = "v$assetVersion"
$asset = "booxd_${assetVersion}_windows_amd64.zip"
$base = $env:BOOXD_BASE_URL
if (-not $base) {
    $base = "https://github.com/curedbylethe/booxd/releases/download/$tag"
}

$destination = $env:BOOXD_BIN_DIR
$overrideDestination = [bool]$destination
if (-not $destination) {
    $destination = Join-Path $env:LOCALAPPDATA 'Programs\booxd'
}
New-Item -ItemType Directory -Force -Path $destination | Out-Null
$temp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null

try {
    $archive = Join-Path $temp $asset
    $checksums = Join-Path $temp 'SHA256SUMS'
    if ($base -match '^https://') {
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $archive
        Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $checksums
    } else {
        Copy-Item -LiteralPath (Join-Path $base $asset) -Destination $archive
        Copy-Item -LiteralPath (Join-Path $base 'SHA256SUMS') -Destination $checksums
    }

    $checksumLine = Get-Content -LiteralPath $checksums | Where-Object {
        $fields = $_.Trim() -split '\s+'
        $fields.Count -ge 2 -and $fields[1] -eq $asset
    } | Select-Object -First 1
    if (-not $checksumLine) {
        throw "SHA256SUMS does not list $asset."
    }
    $expectedHash = ($checksumLine.Trim() -split '\s+')[0].ToLowerInvariant()
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
    if ($actualHash -ne $expectedHash) {
        throw "SHA256 mismatch for $asset."
    }

    $unpacked = Join-Path $temp 'unpacked'
    Expand-Archive -LiteralPath $archive -DestinationPath $unpacked
    $executable = Join-Path $unpacked 'booxd.exe'
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
        throw "$asset does not contain booxd.exe."
    }
    $installed = Join-Path $destination 'booxd.exe'
    Copy-Item -LiteralPath $executable -Destination $installed -Force
    Unblock-File -LiteralPath $installed

    $pathChanged = $false
    if (-not $overrideDestination) {
        $userPath = (Get-Item 'HKCU:\Environment').GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $entries = @($userPath -split ';' | Where-Object { $_ })
        if (-not ($entries | Where-Object { [string]::Equals($_, $destination, [StringComparison]::OrdinalIgnoreCase) })) {
            $newPath = (@($entries) + $destination) -join ';'
            Set-ItemProperty -Path 'HKCU:\Environment' -Name Path -Value $newPath -Type ExpandString
            $pathChanged = $true
        }
        if (-not (($env:Path -split ';') | Where-Object { [string]::Equals($_, $destination, [StringComparison]::OrdinalIgnoreCase) })) {
            $env:Path = "$destination;$env:Path"
        }
    }

    Write-Output "booxd $version installed to $installed"
    if ($overrideDestination) {
        Write-Output 'PATH unchanged because BOOXD_BIN_DIR is set.'
    } elseif ($pathChanged) {
        Write-Output "Added $destination to the user PATH. Open a new terminal to use booxd."
    } else {
        Write-Output "$destination is already on the user PATH. Open a new terminal to use booxd."
    }
} finally {
    Remove-Item -LiteralPath $temp -Recurse -Force
}
