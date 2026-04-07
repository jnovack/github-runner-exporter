# Deployment

The exporter runs as a long-lived sidecar process on the same VM as the GitHub Actions runner. It requires read access to the runner's installation directory.

## Linux — systemd

Create `/etc/systemd/system/github-runner-exporter.service`:

```ini
[Unit]
Description=GitHub Runner Prometheus Exporter
After=network.target

[Service]
Type=simple
User=runner
ExecStart=/usr/local/bin/github-runner-exporter \
  --runner-dir /home/runner/actions-runner \
  --listen-address :9102 \
  --log-level info
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=github-runner-exporter

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now github-runner-exporter
sudo systemctl status github-runner-exporter
```

Verify metrics:

```bash
curl -s http://localhost:9102/metrics | grep github_runner_online
```

## macOS — launchd

Create `~/Library/LaunchAgents/com.jnovack.github-runner-exporter.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.jnovack.github-runner-exporter</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/github-runner-exporter</string>
    <string>--runner-dir</string>
    <string>/Users/runner/actions-runner</string>
    <string>--listen-address</string>
    <string>:9102</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/var/log/github-runner-exporter.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/github-runner-exporter.log</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.jnovack.github-runner-exporter.plist
```

## Windows — NSSM

[NSSM](https://nssm.cc) (Non-Sucking Service Manager) is the simplest way to run the exporter as a Windows service.

```powershell
# Install NSSM first: https://nssm.cc/download
# Or via Chocolatey:
choco install nssm

# Install the service
nssm install github-runner-exporter "C:\actions-runner\github-runner-exporter.exe"
nssm set github-runner-exporter AppParameters "--runner-dir C:\actions-runner --listen-address :9102"
nssm set github-runner-exporter AppDirectory "C:\actions-runner"
nssm set github-runner-exporter Start SERVICE_AUTO_START
nssm set github-runner-exporter ObjectName ".\runner-user"

# Start the service
nssm start github-runner-exporter

# Check status
nssm status github-runner-exporter
```

Alternatively, using the native `sc.exe`:

```powershell
sc.exe create github-runner-exporter `
  binPath= "C:\actions-runner\github-runner-exporter.exe --runner-dir C:\actions-runner --listen-address :9102" `
  start= auto
sc.exe start github-runner-exporter
```

## Docker

The exporter needs access to the runner's `_diag/` directory and `.runner` config file. Mount the runner directory as a volume:

```bash
docker run -d \
  --name github-runner-exporter \
  --restart unless-stopped \
  -p 9102:9102 \
  -v /home/runner/actions-runner:/runner:ro \
  ghcr.io/jnovack/github-runner-exporter:latest \
  --runner-dir /runner
```

> **Note:** Docker on Windows requires a bind mount pointing to the host filesystem path where the runner is installed. Ensure the container user (UID 10001) has read access to the mounted directory.

## Binary from Releases

```bash
# Linux amd64
curl -L https://github.com/jnovack/github-runner-exporter/releases/latest/download/github-runner-exporter-linux-amd64 \
  -o /usr/local/bin/github-runner-exporter
chmod +x /usr/local/bin/github-runner-exporter
```
