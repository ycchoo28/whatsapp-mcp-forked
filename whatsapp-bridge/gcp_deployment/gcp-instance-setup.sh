#!/bin/bash

# GCP Instance Setup Script for WhatsApp Bridge
# This script sets up the complete environment on a GCP e2-micro instance
# Including: Go, Node.js, PM2, GitHub, WhatsApp authentication, and auto-start configuration

set -e

REPO_URL="https://github.com/xilaishan/whatsapp-mcp-forked-xilaishan.git"
REPO_DIR="$HOME/whatsapp-mcp"
GO_VERSION="1.23.4"
APP_NAME="whatsapp-bridge"
BINARY_NAME="whatsapp-bridge-linux"

echo "========================================"
echo "WhatsApp Bridge GCP Complete Setup"
echo "========================================"
echo ""

# Check if running as root
if [ "$EUID" -eq 0 ]; then
   echo "Please do not run this script as root"
   exit 1
fi

# Update system packages
echo "[1/8] Updating system packages..."
sudo apt-get update
sudo apt-get upgrade -y

# Install Git and essential tools
echo "[2/8] Installing Git and essential tools..."
sudo apt-get install -y git curl wget build-essential

# Install Go
echo "[3/8] Installing Go ${GO_VERSION}..."
if ! command -v go &> /dev/null; then
    cd /tmp
    wget "https://golang.org/dl/go${GO_VERSION}.linux-amd64.tar.gz"
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
    rm "go${GO_VERSION}.linux-amd64.tar.gz"

    # Add Go to PATH
    if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
        echo 'export PATH=$PATH:$HOME/go/bin' >> ~/.bashrc
    fi
    export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

    echo "Go installed: $(go version)"
else
    echo "Go already installed: $(go version)"
fi

# Install Node.js and npm
echo "[4/8] Installing Node.js and npm..."
if ! command -v node &> /dev/null; then
    curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
    sudo apt-get install -y nodejs
    echo "Node.js installed: $(node --version)"
    echo "npm installed: $(npm --version)"
else
    echo "Node.js already installed: $(node --version)"
fi

# Install PM2 globally
echo "[5/8] Installing PM2..."
if ! command -v pm2 &> /dev/null; then
    sudo npm install -g pm2
    echo "PM2 installed: $(pm2 --version)"
else
    echo "PM2 already installed: $(pm2 --version)"
fi

# Clone or update the repository
echo "[6/8] Setting up repository..."
if [ ! -d "$REPO_DIR" ]; then
    echo "Cloning repository..."
    git clone "$REPO_URL" "$REPO_DIR"
else
    echo "Repository already exists. Pulling latest changes..."
    cd "$REPO_DIR"
    git pull
fi

# Build the Go binary for Linux
echo "[7/8] Building WhatsApp bridge..."
cd "$REPO_DIR/whatsapp-bridge"

# Enable CGO for SQLite
export CGO_ENABLED=1

# Build the binary
echo "Building $BINARY_NAME..."
GOOS=linux GOARCH=amd64 go build -o "$BINARY_NAME" main.go

# Make sure store directory exists
mkdir -p store

echo ""
echo "========================================"
echo "[8/8] WhatsApp Authentication & PM2 Setup"
echo "========================================"
echo ""

# Check if WhatsApp is already authenticated
if [ -f "$REPO_DIR/whatsapp-bridge/store/whatsapp.db" ]; then
    echo "✅ WhatsApp database found - already authenticated"
    SKIP_AUTH=true
else
    echo "📱 WhatsApp authentication required"
    SKIP_AUTH=false
fi

if [ "$SKIP_AUTH" = false ]; then
    echo ""
    echo "Starting WhatsApp bridge for QR code authentication..."
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "INSTRUCTIONS:"
    echo "1. A QR code will appear below"
    echo "2. Open WhatsApp on your phone"
    echo "3. Go to: Settings > Linked Devices > Link a Device"
    echo "4. Scan the QR code"
    echo "5. Wait for 'Successfully logged in' message"
    echo "6. Press Ctrl+C to stop the bridge"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    read -p "Press Enter to continue and show the QR code..."

    cd "$REPO_DIR/whatsapp-bridge"
    ./"$BINARY_NAME"

    echo ""
    echo "⚠️  If you stopped the bridge, we'll now configure PM2..."
    sleep 2
fi

# Configure PM2
echo ""
echo "Configuring PM2 for auto-start..."
cd "$REPO_DIR/whatsapp-bridge"

# Check if already running in PM2
if pm2 list | grep -q "$APP_NAME"; then
    echo "⚠️  $APP_NAME is already running in PM2"
    echo "Restarting $APP_NAME..."
    pm2 restart "$APP_NAME"
else
    echo "Starting $APP_NAME with PM2..."
    pm2 start ./"$BINARY_NAME" --name "$APP_NAME"
fi

# Save PM2 process list
echo "Saving PM2 process list..."
pm2 save

# Configure PM2 startup for systemd
echo "Configuring PM2 to start on boot..."

# Generate the startup script
STARTUP_CMD=$(pm2 startup systemd -u $USER --hp $HOME 2>&1 | grep "sudo env" || true)

if [ -n "$STARTUP_CMD" ]; then
    echo "Generated startup command - attempting to configure..."

    # Check if we can run sudo without password
    if sudo -n true 2>/dev/null; then
        echo "Running startup command with sudo..."
        eval "$STARTUP_CMD"
        echo "✅ PM2 startup configured successfully"
    else
        echo ""
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        echo "⚠️  Sudo password required. Please run this command:"
        echo ""
        echo "$STARTUP_CMD"
        echo ""
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        echo ""
        read -p "Press Enter after running the command above..."
    fi
else
    echo "⚠️  Could not generate PM2 startup command automatically"
    echo "Please run manually: pm2 startup"
fi

# Show final status
echo ""
echo "Verifying PM2 status..."
pm2 status

echo ""
echo "========================================"
echo "✅ Setup Complete!"
echo "========================================"
echo ""
echo "WhatsApp Bridge Status:"
echo "  • Application: $APP_NAME"
echo "  • Location: $REPO_DIR/whatsapp-bridge"
echo "  • Binary: $BINARY_NAME"
echo "  • PM2 Status: Running"
echo "  • Auto-start: Enabled (on reboot)"
echo ""
echo "Useful PM2 Commands:"
echo "  pm2 status                - View process status"
echo "  pm2 logs $APP_NAME        - View live logs"
echo "  pm2 restart $APP_NAME     - Restart the bridge"
echo "  pm2 stop $APP_NAME        - Stop the bridge"
echo "  pm2 monit                 - Monitor resources"
echo ""
echo "Test Auto-Start:"
echo "  sudo reboot               - Reboot and verify auto-start"
echo ""
echo "Next Steps:"
echo "  • Setup Google Cloud Logging (optional)"
echo "  • Configure firewall rules for API access"
echo "  • Monitor logs: pm2 logs $APP_NAME"
echo ""
