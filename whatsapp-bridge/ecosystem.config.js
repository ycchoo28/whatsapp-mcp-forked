// ecosystem.config.js
module.exports = {
    apps: [
      {
        name: "whatsapp-bridge",
        script: "go run main.go",
        interpreter: "none",
        exec_mode: "fork",
        watch: false
      }
    ]
  };
  