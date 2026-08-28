#Requires -Version 5.1
<#
.SYNOPSIS
  Готовит sql2csv на чистой Windows-машине: Go (если нет), модули, sql2csv.exe.
  Повторный запуск идемпотентен: уже установленный Go не переустанавливается,
  окружение не ломается.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -LiteralPath $ProjectRoot

function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-Host "==> $Message"
}

function Write-Fail {
    param([string]$Message)
    Write-Host ""
    Write-Host "error: $Message" -ForegroundColor Red
}

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = "$machine;$user"
    $extra = @(
        'C:\Program Files\Go\bin',
        (Join-Path $env:LOCALAPPDATA 'Programs\Go\bin')
    )
    foreach ($dir in $extra) {
        $exe = Join-Path $dir 'go.exe'
        if ((Test-Path -LiteralPath $exe) -and ($env:Path -notlike "*$dir*")) {
            $env:Path = "$dir;$env:Path"
        }
    }
}

function Test-GoAvailable {
    Refresh-Path
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if (-not $cmd) {
        return $false
    }
    & go version
    return $LASTEXITCODE -eq 0
}

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal $id
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Install-GoWithWinget {
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if (-not $winget) {
        Write-Host "winget не найден."
        return $false
    }
    Write-Step "Установка Go через winget (GoLang.Go)"
    & winget install --id GoLang.Go -e --accept-package-agreements --accept-source-agreements
    Write-Host "winget завершился с кодом $LASTEXITCODE"
    return (Test-GoAvailable)
}

function Get-GoMsiUrl {
    $arch = 'amd64'
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
        $arch = 'arm64'
    }
    Write-Host "Запрос списка дистрибутивов https://go.dev/dl/?mode=json (windows-$arch installer)..."
    $releases = Invoke-RestMethod -Uri 'https://go.dev/dl/?mode=json' -UseBasicParsing
    foreach ($rel in $releases) {
        foreach ($file in $rel.files) {
            if ($file.os -eq 'windows' -and $file.arch -eq $arch -and $file.kind -eq 'installer') {
                return "https://go.dev/dl/$($file.filename)"
            }
        }
    }
    return $null
}

function Install-GoWithMsi {
    $url = Get-GoMsiUrl
    if (-not $url) {
        Write-Host "Не удалось найти Windows-installer на go.dev."
        return $false
    }
    if (-not (Test-Admin)) {
        Write-Host "Для запуска MSI нужны права администратора."
        return $false
    }
    $msi = Join-Path $env:TEMP 'sql2csv-go-setup.msi'
    Write-Step "Скачивание $url"
    Invoke-WebRequest -Uri $url -OutFile $msi -UseBasicParsing
    Write-Step "Запуск MSI (не тихий: /passive)"
    $p = Start-Process -FilePath 'msiexec.exe' -ArgumentList @('/i', $msi, '/passive', '/norestart') -Wait -PassThru
    Write-Host "msiexec завершился с кодом $($p.ExitCode)"
    if ($p.ExitCode -ne 0 -and $p.ExitCode -ne 3010) {
        return $false
    }
    return (Test-GoAvailable)
}

if (-not (Test-Path -LiteralPath (Join-Path $ProjectRoot 'go.mod'))) {
    Write-Fail "в $ProjectRoot нет go.mod — запустите setup.ps1 из каталога проекта."
    exit 1
}

Write-Step "Проверка Go"
if (Test-GoAvailable) {
    Write-Host "Go уже есть, установку пропускаем."
} else {
    Write-Host "Go не найден, пытаемся установить."
    $ok = Install-GoWithWinget
    if (-not $ok) {
        $ok = Install-GoWithMsi
    }
    if (-not $ok) {
        Write-Fail @"
не удалось установить Go автоматически.
Скачайте toolchain с https://go.dev/dl/ (Windows installer), установите, откройте новую консоль и снова запустите:
  powershell -ExecutionPolicy Bypass -File .\setup.ps1
"@
        exit 1
    }
}

Write-Step "go mod download"
& go mod download
if ($LASTEXITCODE -ne 0) {
    Write-Fail "go mod download завершился с кодом $LASTEXITCODE"
    exit 1
}

Write-Step "go build -o sql2csv.exe ."
& go build -o sql2csv.exe .
if ($LASTEXITCODE -ne 0) {
    Write-Fail "go build завершился с кодом $LASTEXITCODE"
    exit 1
}

$exe = Join-Path $ProjectRoot 'sql2csv.exe'
if (-not (Test-Path -LiteralPath $exe)) {
    Write-Fail "сборка прошла, но $exe не появился"
    exit 1
}

Write-Host ""
Write-Host "Готово: $exe"
Write-Host "Запуск: .\sql2csv.exe   (корень C:\Source\db скрипт не трогает)"
exit 0
