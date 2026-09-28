# Download official Wintun 0.14.1 and stage the amd64 DLL for go:embed.
# Zip and DLL digests match internal/wintunbin/pin.go.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$zipSha = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
$dllSha = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
$cache = Join-Path $root ".cache"
New-Item -ItemType Directory -Force -Path $cache | Out-Null
$zip = Join-Path $cache "wintun-0.14.1.zip"
Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile $zip
$got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
if ($got -ne $zipSha) { throw "wintun zip sha256 $got" }
$dest = Join-Path $cache "wintun"
if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
Expand-Archive -Path $zip -DestinationPath $dest -Force
$dll = Join-Path $dest "wintun\bin\amd64\wintun.dll"
$gotDll = (Get-FileHash -Algorithm SHA256 $dll).Hash.ToLower()
if ($gotDll -ne $dllSha) { throw "wintun.dll sha256 $gotDll" }
Copy-Item $dll (Join-Path $root "internal\wintunbin\wintun.dll") -Force
Write-Output "staged internal/wintunbin/wintun.dll"
