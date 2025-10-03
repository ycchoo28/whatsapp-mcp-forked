# GCP Instance Setup Guide for WhatsApp Bridge

This guide provides complete instructions for deploying the WhatsApp Bridge on a Google Cloud Platform (GCP) free tier e2-micro instance.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Setup](#quick-setup)
- [Detailed Setup Steps](#detailed-setup-steps)
- [Google Cloud Logging Setup](#google-cloud-logging-setup)
- [PM2 Process Management](#pm2-process-management)
- [Maintenance & Updates](#maintenance--updates)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

### Local Machine

- GCP account with an active project
- `gcloud` CLI installed and configured
- SSH access to GCP instances

### GCP Instance

- **Instance Type:** e2-micro (free tier eligible)
- **Region:** us-central1, us-east1, or us-west1 (free tier regions)
- **OS:** Debian 12 (Bookworm)
- **Disk:** 30 GB standard persistent disk
- **Firewall:** Port 8080 open (or custom port for WhatsApp Bridge API)

---

## Quick Setup

### 1. Upload Setup Script to Instance

From your local machine:

```bash
# Copy the setup script to your GCP instance
gcloud compute scp gcp-instance-setup.sh YOUR_INSTANCE_NAME:~ --zone=YOUR_ZONE

# SSH into the instance
gcloud compute ssh YOUR_INSTANCE_NAME --zone=YOUR_ZONE
```

### 2. Run the All-in-One Setup Script

On the GCP instance:

```bash
# Make the script executable
chmod +x gcp-instance-setup.sh

# Run the complete setup script
./gcp-instance-setup.sh
```

**This script automatically handles everything:**
- ✅ Updates system packages
- ✅ Installs Git, Go, Node.js, and PM2
- ✅ Clones the repository
- ✅ Builds the WhatsApp bridge binary
- ✅ Prompts for WhatsApp QR code authentication
- ✅ Starts the bridge with PM2
- ✅ Configures PM2 auto-start on reboot
- ✅ Saves the PM2 process list

**During execution:**
1. The script will pause and show instructions for scanning the WhatsApp QR code
2. Open WhatsApp on your phone: Settings > Linked Devices > Link a Device
3. Scan the QR code displayed in the terminal
4. Wait for "Successfully logged in" message
5. Press `Ctrl+C` to continue
6. The script will automatically configure PM2 and enable auto-start

**That's it!** The bridge will now automatically restart on system reboot.

---

## Detailed Setup Steps

### Installing Go

The setup script installs Go 1.23.4, which is required to build and run the WhatsApp bridge.

**Manual Installation:**

```bash
# Download Go
cd /tmp
wget https://golang.org/dl/go1.23.4.linux-amd64.tar.gz

# Remove old installation and extract
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.23.4.linux-amd64.tar.gz

# Add to PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export PATH=$PATH:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc

# Verify installation
go version
```

### Installing Node.js and PM2

**Node.js 22.x (LTS):**

```bash
# Install Node.js repository
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -

# Install Node.js
sudo apt-get install -y nodejs

# Verify
node --version
npm --version
```

**PM2 Process Manager:**

```bash
# Install PM2 globally
sudo npm install -g pm2

# Verify
pm2 --version
```

### Building the WhatsApp Bridge

The bridge needs to be compiled with CGO enabled for SQLite support:

```bash
cd ~/whatsapp-mcp/whatsapp-bridge

# Enable CGO and build
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o whatsapp-bridge-linux main.go

# Make executable (should already be)
chmod +x whatsapp-bridge-linux

# Create store directory for databases
mkdir -p store
```

---

## Google Cloud Logging Setup

Google Cloud Logging allows you to view PM2 logs without SSH access. The free tier includes **50 GiB/month** of logs.

### 1. Install Ops Agent

```bash
# Download and install
curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh
sudo bash add-google-cloud-ops-agent-repo.sh --also-install
rm add-google-cloud-ops-agent-repo.sh
```

### 2. Configure Ops Agent

Create the configuration file:

```bash
sudo tee /etc/google-cloud-ops-agent/config.yaml > /dev/null << 'EOF'
logging:
  receivers:
    pm2_logs:
      type: files
      include_paths:
        - /home/*/.pm2/logs/*-out.log
        - /home/*/.pm2/logs/*-error.log
      record_log_file_path: true

  processors:
    pm2_parser:
      type: parse_regex
      field: message
      regex: '^(?<timestamp>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z)\s+(?<level>\w+)\s+(?<message>.*)'
      time_key: timestamp
      time_format: "%Y-%m-%dT%H:%M:%S.%LZ"

  service:
    pipelines:
      pm2_pipeline:
        receivers:
          - pm2_logs
        processors:
          - pm2_parser

metrics:
  receivers:
    hostmetrics:
      type: hostmetrics
      collection_interval: 60s
  service:
    pipelines:
      default_pipeline:
        receivers:
          - hostmetrics
EOF
```

### 3. Update VM Access Scopes

**IMPORTANT:** The instance must have Cloud Logging write permissions. This requires stopping the instance.

From your **local machine**:

```bash
# Stop the instance
gcloud compute instances stop YOUR_INSTANCE_NAME --zone=YOUR_ZONE

# Update service account scopes
gcloud compute instances set-service-account YOUR_INSTANCE_NAME \
  --zone=YOUR_ZONE \
  --service-account=PROJECT_NUMBER-compute@developer.gserviceaccount.com \
  --scopes=https://www.googleapis.com/auth/devstorage.read_only,https://www.googleapis.com/auth/logging.write,https://www.googleapis.com/auth/monitoring.write,https://www.googleapis.com/auth/service.management.readonly,https://www.googleapis.com/auth/servicecontrol,https://www.googleapis.com/auth/trace.append

# Start the instance
gcloud compute instances start YOUR_INSTANCE_NAME --zone=YOUR_ZONE
```

### 4. Restart Ops Agent

On the instance:

```bash
# Restart the agent
sudo service google-cloud-ops-agent restart

# Verify status
sudo service google-cloud-ops-agent status
```

Look for `[API Check] Result: PASS` in the output.

### 5. View Logs in Cloud Console

Access logs at:
```
https://console.cloud.google.com/logs/query?project=YOUR_PROJECT_ID
```

**Query for PM2 logs:**
```
resource.type="gce_instance"
log names="pm2_logs"
```

Or filter by app name:
```
resource.type="gce_instance"
log names="pm2_logs"
jsonPayload.message=~"whatsapp-bridge"
```

---

## PM2 Process Management

### Essential PM2 Commands

```bash
# View status of all processes
pm2 status

# View logs (live)
pm2 logs whatsapp-bridge

# View logs (last 100 lines)
pm2 logs whatsapp-bridge --lines 100

# View only error logs
pm2 logs whatsapp-bridge --err

# Restart the bridge
pm2 restart whatsapp-bridge

# Stop the bridge
pm2 stop whatsapp-bridge

# Remove from PM2
pm2 delete whatsapp-bridge

# View detailed info
pm2 show whatsapp-bridge

# Monitor resources
pm2 monit
```

### PM2 Startup Configuration

To ensure the bridge starts automatically after reboot:

```bash
# Generate startup script
pm2 startup

# This will print a command like:
# sudo env PATH=$PATH:/usr/bin /usr/lib/node_modules/pm2/bin/pm2 startup systemd -u YOUR_USER --hp /home/YOUR_USER

# Run the printed command
sudo env PATH=$PATH:/usr/bin /usr/lib/node_modules/pm2/bin/pm2 startup systemd -u $USER --hp $HOME

# Save current process list
pm2 save
```

### Verifying Auto-Start Configuration

Check if PM2 auto-start is properly configured:

```bash
# Check PM2 startup service status
systemctl status pm2-$USER.service

# Verify PM2 saved processes
ls -la ~/.pm2/dump.pm2

# View current PM2 processes
pm2 status
```

Expected output:
- Service should be **active (running)** and **enabled**
- dump.pm2 file should exist
- whatsapp-bridge should be **online** in PM2

### Testing Auto-Start After Reboot

```bash
# Restart the instance
sudo reboot

# After reboot, SSH back in and check status
pm2 status

# The whatsapp-bridge should automatically be running
```

---

## Maintenance & Updates

### Pulling Latest Code from GitHub

```bash
# Navigate to repository
cd ~/whatsapp-mcp

# Pull latest changes
git pull origin main

# Rebuild the binary
cd whatsapp-bridge
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o whatsapp-bridge-linux main.go

# Restart with PM2
pm2 restart whatsapp-bridge
```

### Updating Dependencies

```bash
cd ~/whatsapp-mcp/whatsapp-bridge

# Update Go modules
go get -u ./...
go mod tidy

# Rebuild
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o whatsapp-bridge-linux main.go

# Restart
pm2 restart whatsapp-bridge
```

### Monitoring Disk Space

```bash
# Check disk usage
df -h

# Check PM2 logs size
du -sh ~/.pm2/logs/

# Rotate/clear old logs
pm2 flush
```

### Database Maintenance

The bridge stores data in SQLite databases in `~/whatsapp-mcp/whatsapp-bridge/store/`:

```bash
# Check database size
du -sh ~/whatsapp-mcp/whatsapp-bridge/store/

# Backup databases
cd ~/whatsapp-mcp/whatsapp-bridge
tar -czf backup-$(date +%Y%m%d).tar.gz store/

# If WhatsApp gets out of sync, delete databases and re-authenticate
pm2 stop whatsapp-bridge
rm -rf store/messages.db store/whatsapp.db
pm2 start whatsapp-bridge
# Scan QR code again
```

---

## Troubleshooting

### WhatsApp Bridge Won't Start

```bash
# Check PM2 logs
pm2 logs whatsapp-bridge --lines 50

# Check if port 8080 is in use
sudo netstat -tlnp | grep 8080

# Try running directly to see errors
cd ~/whatsapp-mcp/whatsapp-bridge
./whatsapp-bridge-linux
```

### QR Code Not Displaying

```bash
# Stop PM2 version
pm2 stop whatsapp-bridge

# Run directly to see QR code
cd ~/whatsapp-mcp/whatsapp-bridge
./whatsapp-bridge-linux

# After scanning, start with PM2 again
pm2 start whatsapp-bridge-linux --name whatsapp-bridge
```

### Build Errors with CGO

```bash
# Ensure build-essential is installed
sudo apt-get install -y build-essential

# Verify CGO is enabled
go env CGO_ENABLED
# Should output: 1

# Set explicitly and rebuild
export CGO_ENABLED=1
cd ~/whatsapp-mcp/whatsapp-bridge
go build -o whatsapp-bridge-linux main.go
```

### Cloud Logging Not Working

```bash
# Check Ops Agent status
sudo service google-cloud-ops-agent status

# Look for authentication errors
# If you see API Check FAIL, you need to update VM scopes (see section above)

# Restart Ops Agent
sudo service google-cloud-ops-agent restart

# View Ops Agent logs
journalctl -u google-cloud-ops-agent -f
```

### PM2 Not Starting on Boot

```bash
# Remove existing startup script
pm2 unstartup

# Regenerate
pm2 startup

# Run the printed sudo command

# Save processes
pm2 save

# Test by rebooting
sudo reboot
```

### Out of Memory Errors

The e2-micro instance has only 1 GB RAM. If you encounter memory issues:

```bash
# Check memory usage
free -h

# View PM2 resource usage
pm2 monit

# Restart the bridge to free memory
pm2 restart whatsapp-bridge

# Consider adding swap space
sudo fallocate -l 1G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# Make swap permanent
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

---

## Configuration Options

### Environment Variables

Create a `.env` file in `~/whatsapp-mcp/whatsapp-bridge/`:

```bash
# Custom port (default: 8080)
PORT=3000

# Webhook URL for incoming messages
WEBHOOK_URL=https://your-n8n-instance.com/webhook/whatsapp

# Message whitelist (comma-separated phone numbers)
WHITELIST=123456789,987654321
```

### PM2 Ecosystem File

For advanced PM2 configuration, edit `~/whatsapp-mcp/whatsapp-bridge/ecosystem.config.js`:

```javascript
module.exports = {
  apps: [
    {
      name: "whatsapp-bridge",
      script: "./whatsapp-bridge-linux",
      interpreter: "none",
      exec_mode: "fork",
      watch: false,
      env: {
        PORT: 8080,
        WEBHOOK_URL: "https://your-webhook-url.com"
      }
    }
  ]
};
```

Start with ecosystem file:
```bash
pm2 start ecosystem.config.js
```

---

## Security Recommendations

1. **Firewall Rules:** Only open necessary ports
   ```bash
   # Allow only your IP to access port 8080
   gcloud compute firewall-rules create allow-bridge-api \
     --allow=tcp:8080 \
     --source-ranges=YOUR_IP/32 \
     --target-tags=whatsapp-bridge
   ```

2. **Regular Updates:**
   ```bash
   sudo apt-get update && sudo apt-get upgrade -y
   ```

3. **SSH Key Authentication:** Disable password authentication
   ```bash
   sudo nano /etc/ssh/sshd_config
   # Set: PasswordAuthentication no
   sudo systemctl restart sshd
   ```

4. **Monitor Billing:** Set up billing alerts at $0.01 threshold

---

## Free Tier Compliance

✅ **Staying within limits:**
- e2-micro instance in us-central1, us-east1, or us-west1
- 30 GB standard persistent disk
- First 50 GiB of Cloud Logging per month
- First 1 GB network egress to North America per month

⚠️ **Monitor usage:**
- Check egress: https://console.cloud.google.com/networking/usage
- Check logging: https://console.cloud.google.com/logs/usage
- Set billing alert at $0.01

---

## Additional Resources

- [GCP Free Tier Documentation](https://cloud.google.com/free/docs/free-cloud-features)
- [PM2 Documentation](https://pm2.keymetrics.io/docs/usage/quick-start/)
- [WhatsApp Bridge API Documentation](whatsapp-bridge/API.md)
- [Google Cloud Ops Agent Documentation](https://cloud.google.com/stackdriver/docs/solutions/agents/ops-agent)

---

**Last Updated:** October 2025
