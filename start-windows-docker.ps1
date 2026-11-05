param(
    [switch]$Build,
    [string]$LanIP = "",
    [string]$VectorIP = ""
)

$ErrorActionPreference = "Stop"

# 1. Detect the physical LAN IPv4 address (ignoring WSL, Docker, and VPN virtual adapters)
if (-not $LanIP) {
    $candidates = Get-NetIPAddress -AddressFamily IPv4 | Where-Object {
        $_.IPAddress -notlike "127.*" -and
        $_.IPAddress -notlike "169.254.*" -and
        $_.InterfaceAlias -notlike "vEthernet*" -and
        $_.InterfaceAlias -notlike "*VPN*" -and
        $_.InterfaceAlias -notlike "*Loopback*"
    }
    $preferred = $candidates | Where-Object { $_.IPAddress -like "192.168.*" -or $_.IPAddress -like "10.*" } | Select-Object -First 1
    if ($preferred) {
        $LanIP = $preferred.IPAddress
    } elseif ($candidates) {
        $LanIP = ($candidates | Select-Object -First 1).IPAddress
    } else {
        Write-Error "Could not automatically detect your LAN IPv4 address. Pass it explicitly with: .\start-windows-docker.ps1 -LanIP 192.168.1.x"
    }
}

Write-Host "==> Detected Windows Host LAN IP: $LanIP" -ForegroundColor Cyan
$env:HOST_IP = $LanIP

# 2. Discover Vector's real LAN IP via mDNS (_ankivector._tcp) if not explicitly provided
if (-not $VectorIP -and (Get-Command dns-sd.exe -ErrorAction SilentlyContinue)) {
    Write-Host "==> Scanning LAN for Vector via mDNS (_ankivector._tcp)..." -ForegroundColor Cyan
    $job = Start-Job { dns-sd.exe -B _ankivector._tcp local }
    Start-Sleep -Seconds 2
    $browseOut = Receive-Job $job | Out-String
    Stop-Job $job -ErrorAction SilentlyContinue
    Remove-Job $job -Force -ErrorAction SilentlyContinue
    if ($browseOut -match "(Vector-[A-Za-z0-9]+)") {
        $vecHost = "$($Matches[1]).local"
        try {
            $resolved = [System.Net.Dns]::GetHostAddresses($vecHost) | Where-Object { $_.AddressFamily -eq 'InterNetwork' } | Select-Object -First 1
            if ($resolved) {
                $VectorIP = $resolved.IPAddressToString
                Write-Host "==> Discovered $vecHost at $VectorIP" -ForegroundColor Green
            }
        } catch {}
    }
}

if ($VectorIP) {
    $env:VECTOR_IP = $VectorIP
}

# 3. Start (and optionally rebuild) the wire-pod Docker container
if ($Build) {
    Write-Host "==> Building and starting wire-pod container..." -ForegroundColor Cyan
    docker compose up -d --build
} else {
    Write-Host "==> Starting wire-pod container..." -ForegroundColor Cyan
    docker compose up -d
}

# 4. If VectorIP was found and botSdkInfo.json has a Docker 172.x.0.1 gateway IP, update it and restart wire-pod
if ($VectorIP) {
    $botInfo = docker exec wire-pod sh -c "cat /data/chipper/jdocs/botSdkInfo.json 2>/dev/null"
    if ($botInfo -match "172\.\d+\.0\.1") {
        Write-Host "==> Updating Vector IP in botSdkInfo.json from Docker bridge IP to $VectorIP..." -ForegroundColor Yellow
        docker exec wire-pod sed -i "s/172\.[0-9]*\.0\.1/$VectorIP/g" /data/chipper/jdocs/botSdkInfo.json
        docker restart wire-pod | Out-Null
    }
}

# 5. Ensure Windows Bonjour mDNS proxy is advertising escapepod.local -> $LanIP
$existingDnsSd = Get-CimInstance Win32_Process -Filter "Name = 'dns-sd.exe'" -ErrorAction SilentlyContinue |
    Where-Object { $_.CommandLine -like "*escapepod*" }

if ($existingDnsSd) {
    Write-Host "==> Stopping old dns-sd.exe mDNS proxy process(es)..." -ForegroundColor Yellow
    $existingDnsSd | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
}

if (Get-Command dns-sd.exe -ErrorAction SilentlyContinue) {
    Write-Host "==> Starting Windows mDNS proxy for escapepod.local -> $LanIP..." -ForegroundColor Cyan
    Start-Process -FilePath "dns-sd.exe" `
        -ArgumentList "-P escapepod _app-proto._tcp local 8084 escapepod.local $LanIP txtv=0 lo=1 la=2" `
        -WindowStyle Hidden
    Start-Sleep -Seconds 2
    Write-Host "==> Verifying escapepod.local resolution..." -ForegroundColor Cyan
    ping.exe -4 -n 1 escapepod.local
} else {
    Write-Warning "dns-sd.exe (Apple Bonjour Service) was not found in PATH. Install Bonjour Print Services for Windows so Vector can resolve escapepod.local."
}

# 6. Warn if a VPN or Public firewall profile is blocking LAN traffic
$vpnAdapters = Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -like "*VPN*" }
if ($vpnAdapters) {
    Write-Host ""
    Write-Warning "An active VPN adapter ($($vpnAdapters.InterfaceAlias -join ', ')) was detected!"
    Write-Host "If Bot Settings fails to connect to Vector ($VectorIP:443), your VPN's Kill-Switch/Firewall is blocking outbound LAN traffic to 192.168.1.x. Disconnect the VPN or enable 'Allow LAN Traffic' in your VPN settings." -ForegroundColor Yellow
}

$publicProfiles = Get-NetConnectionProfile | Where-Object {
    $_.NetworkCategory -eq "Public" -and $_.InterfaceAlias -notlike "*VPN*" -and $_.InterfaceAlias -notlike "vEthernet*"
}
if ($publicProfiles) {
    Write-Host ""
    Write-Warning "Your LAN adapter ($($publicProfiles.InterfaceAlias -join ', ')) is set to 'Public' in Windows."
    Write-Host "If Vector cannot connect during authentication, run PowerShell as Administrator and execute:" -ForegroundColor Yellow
    Write-Host '  New-NetFirewallRule -DisplayName "Wire-Pod LAN" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 80,443,8080,8084 -Profile Any' -ForegroundColor White
    Write-Host "  Set-NetConnectionProfile -InterfaceAlias `"$($publicProfiles[0].InterfaceAlias)`" -NetworkCategory Private" -ForegroundColor White
}

Write-Host ""
Write-Host "==> Wire-Pod is ready! Web UI: http://localhost:8080 (or http://${LanIP}:8080)" -ForegroundColor Green
