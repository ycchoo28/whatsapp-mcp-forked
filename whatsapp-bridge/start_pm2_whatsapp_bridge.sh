#!/bin/bash
# Script to start the WhatsApp bridge Go app with PM2 and enable auto-restart on crash
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

echo "Started $APP_NAME with PM2. Use 'pm2 logs $APP_NAME' to view logs." 