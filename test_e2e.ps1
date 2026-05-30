$protocols = @("vless-reality", "xhttp")
$nodeId = "vps-test"

foreach ($proto in $protocols) {
    Write-Host "`n=== Testing Standalone: $proto ==="
    
    # 0. Create dummy user
    $userBody = "id=qa_$proto&name=qa_$proto&protocols=$proto"
    curl.exe -s -X POST -d "$userBody" "http://localhost:8090/ui/users" > $null

    # 1. Update Inbound via UI form POST
    Write-Host "Saving inbound..."
    $body = "inbound_index=0&proto=$proto&port=8443&for_users_0=qa_$proto"
    curl.exe -s -X POST -d "$body" "http://localhost:8090/ui/nodes/$nodeId/inbounds" > $null

    # 2. Apply Node
    Write-Host "Applying Node..."
    $applyRes = curl.exe -s -X POST "http://localhost:8090/ui/nodes/$nodeId/apply"
    if ($applyRes -match "class=`"alert alert-success`"") {
        Write-Host "[PASS] Apply Node succeeded"
    } elseif ($applyRes -match "class=`"alert alert-danger`"") {
        Write-Host "[FAIL] Apply Node failed!"
        Write-Host $applyRes
        continue
    }

    # 4. Fetch Client Config (Assuming node1 or vps-test, how does the UI get standalone configs?)
    # The UI gets configs via GET /ui/users/qa_$proto/config?chain=&node=$nodeId
    $html = curl.exe -s "http://localhost:8090/ui/users/qa_$proto/config?node=$nodeId"
    
    if ($html -match '(?s)<textarea[^>]*>(.*?)</textarea>') {
        $configText = $matches[1] -replace '&#34;', '"' -replace '&lt;', '<' -replace '&gt;', '>' -replace '&amp;', '&'
        
        # Build client JSON based on URI
        $json = ""
        if ($configText -match "vless://([^@]*)@([^:]+):(\d+)\?type=tcp&security=reality&pbk=([^&]+)&sni=([^&]+)&sid=([^&]+)") {
            $uuid = $matches[1]
            $ip = $matches[2]
            $port = $matches[3]
            $pbk = $matches[4]
            $sni = $matches[5]
            $sid = $matches[6]
            
            $json = @"
{
  "log": {"level": "info"},
  "outbounds": [
    {
      "type": "vless", "tag": "proxy", "server": "$ip", "server_port": $port, "uuid": "$uuid", "flow": "xtls-rprx-vision",
      "tls": {"enabled": true, "server_name": "$sni", "utls": {"enabled": true, "fingerprint": "chrome"}, "reality": {"enabled": true, "public_key": "$pbk", "short_id": "$sid"}}
    }
  ],
  "inbounds": [{"type": "mixed", "listen": "127.0.0.1", "listen_port": 1080}]
}
"@
        } elseif ($configText -match "vless://([^@]*)@([^:]+):(\d+)\?type=xhttp&security=none&host=([^&]+)&path=%2Fapi") {
            $uuid = $matches[1]
            $ip = $matches[2]
            $port = $matches[3]
            $hostName = $matches[4]
            
            $json = @"
{
  "log": {"level": "info"},
  "outbounds": [
    {
      "type": "vless", "tag": "proxy", "server": "$ip", "server_port": $port, "uuid": "$uuid",
      "transport": {"type": "http", "host": ["$hostName"], "path": "/api", "method": "POST", "headers": {"Content-Type": ["application/json"]}}
    }
  ],
  "inbounds": [{"type": "mixed", "listen": "127.0.0.1", "listen_port": 1080}]
}
"@
        } else {
            Write-Host "[FAIL] Config text is not a recognized URI: $configText"
            continue
        }
        
        [System.IO.File]::WriteAllText("$PWD\client.json", $json)
        Write-Host "[PASS] Generated client.json from URI"
    } else {
        Write-Host "[FAIL] Could not extract config from HTML response"
        continue
    }

    # 5. Start Sing-Box
    Write-Host "Starting Sing-box..."
    $sb = Start-Process -FilePath ".\sing-box.exe" -ArgumentList "run -c client.json" -PassThru -NoNewWindow
    
    Start-Sleep -Seconds 3

    # 6. Test proxy
    Write-Host "Testing SOCKS5 Proxy..."
    $proxyTest = curl.exe -s -x "socks5://127.0.0.1:1080" "https://api.ipify.org"
    if ($proxyTest -match "\d+\.\d+\.\d+\.\d+") {
        Write-Host "[PASS] Traffic works! Proxy IP: $proxyTest"
    } else {
        Write-Host "[FAIL] Traffic test failed!"
    }

    # 7. Kill Sing-Box
    Stop-Process -Id $sb.Id -Force -ErrorAction SilentlyContinue
}
