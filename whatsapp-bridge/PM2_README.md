# Running WhatsApp Bridge with PM2

This guide explains how to run the Go WhatsApp bridge app using [PM2](https://pm2.keymetrics.io/), a process manager that ensures your app stays running and restarts automatically if it crashes.

## 1. Prerequisites
- [Node.js](https://nodejs.org/) and [npm](https://www.npmjs.com/) installed
- PM2 installed globally:
  ```sh
  npm install -g pm2
  ```
- Go installed (for `go run main.go`)

## 2. Starting the App with PM2

Use the provided script to start the app with PM2:

```sh
./start_pm2_whatsapp_bridge.sh
```

- This will start the Go app (`main.go`) with PM2 under the name `whatsapp-bridge`.
- If the process is already running, it will be restarted.
- PM2 will automatically restart the app if it crashes or exits unexpectedly.

## 3. Viewing Logs and Status

- View logs:
  ```sh
  pm2 logs whatsapp-bridge
  ```
- Check status:
  ```sh
  pm2 status
  ```

## 4. Enable Auto-Start on Boot

To ensure PM2 and your app start automatically after a reboot:

```sh
pm2 startup
pm2 save
```
- Follow any instructions PM2 prints after `pm2 startup` (it may ask you to run a command as sudo).
- `pm2 save` saves the current process list for resurrection on boot.

## 5. Stopping or Restarting

- Restart:
  ```sh
  pm2 restart whatsapp-bridge
  ```
- Stop:
  ```sh
  pm2 stop whatsapp-bridge
  ```
- Delete from PM2:
  ```sh
  pm2 delete whatsapp-bridge
  ```

## 6. Notes
- PM2 will keep your Go app running and restart it if it fails—no extra monitoring script is needed.
- You can adjust the script or PM2 options as needed (see `pm2 --help`). 