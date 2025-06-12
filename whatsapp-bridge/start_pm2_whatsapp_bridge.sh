#!/bin/bash
# Script to start the WhatsApp bridge Go app with PM2 and enable auto-restart on crash
# Also configures PM2 to start on boot
# Usage: ./start_pm2_whatsapp_bridge.sh

APP_NAME="whatsapp-bridge"
APP_DIR=$(dirname "$0")
GO_MAIN="main.go"

cd "$APP_DIR" || exit 1

# Start the Go app with PM2 (auto-restart enabled by default)
# --interpreter=none tells PM2 to run the command as-is (not as Node.js)
# If already running, restart it
if pm2 list | grep -q "$APP_NAME"; then
  pm2 restart "$APP_NAME"
else
  pm2 start --name "$APP_NAME" --interpreter=none -- go run "$GO_MAIN"
fi

# Save the PM2 process list for resurrecting on reboot
pm2 save

# Configure PM2 to start on boot for various operating systems
if [[ "$OSTYPE" == "darwin"* ]]; then
    echo "Configuring PM2 to start on boot for macOS..."
    PM2_STARTUP_COMMAND=$(PM2_HOME="$HOME/.pm2" pm2 startup launchd -u $(whoami) --hp $HOME | tail -n 1)
    
    # Check if the command requires sudo
    if [[ $PM2_STARTUP_COMMAND == sudo* ]]; then
        echo "Warning: This requires sudo. Attempting to run with sudo..."
        # Use expect to automate sudo (if password-less sudo is not setup)
        if ! sudo -n true 2>/dev/null; then
            echo "Error: Sudo password required. Please configure password-less sudo or run this manually:"
            echo "$PM2_STARTUP_COMMAND"
        else
            eval "$PM2_STARTUP_COMMAND"
        fi
    else
        eval "$PM2_STARTUP_COMMAND"
    fi
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    echo "Configuring PM2 to start on boot for Linux..."
    PM2_STARTUP_COMMAND=$(pm2 startup | grep -v "[PM2] Init System found:" | tail -n 1)
    
    # Check if the command requires sudo
    if [[ $PM2_STARTUP_COMMAND == sudo* ]]; then
        echo "Warning: This requires sudo. Attempting to run with sudo..."
        if ! sudo -n true 2>/dev/null; then
            echo "Error: Sudo password required. Please configure password-less sudo or run this manually:"
            echo "$PM2_STARTUP_COMMAND"
        else
            eval "$PM2_STARTUP_COMMAND"
        fi
    else
        eval "$PM2_STARTUP_COMMAND"
    fi
else
    echo "Unsupported operating system for automatic PM2 startup configuration."
    echo "Please manually configure PM2 to start on boot for your system."
fi

echo "Started $APP_NAME with PM2. Use 'pm2 logs $APP_NAME' to view logs."
echo "PM2 has been configured to start on system boot." 