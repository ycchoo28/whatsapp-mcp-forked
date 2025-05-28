# WhatsApp Bridge Webhook Feature

This feature allows you to forward every incoming WhatsApp message to an external HTTP endpoint (webhook) as a JSON POST request.

## How it works
- Every time a new WhatsApp message is received, the bridge will send a POST request to the URL specified in the `WEBHOOK_URL` environment variable.
- The payload contains details about the message, including text, media info, sender, and timestamp.

## Configuration
1. Copy `.env.example` to `.env` and set your webhook URL:
   ```sh
   cp whatsapp-bridge/.env.example whatsapp-bridge/.env
   # Edit whatsapp-bridge/.env and set WEBHOOK_URL
   ```
2. Make sure to load the environment variables before running the bridge. For example:
   ```sh
   export $(grep -v '^#' whatsapp-bridge/.env | xargs)
   go run whatsapp-bridge/main.go
   ```

## Payload Format
The webhook will receive a JSON object like this:

```
{
  "id": "string",           // WhatsApp message ID
  "chat_jid": "string",    // Chat JID
  "sender": "string",      // Sender's WhatsApp ID
  "content": "string",     // Text content (if any)
  "timestamp": "string",   // Message timestamp (RFC3339 format)
  "is_from_me": false,      // Whether the message is sent by you
  "media_type": "string",  // Media type (image, video, audio, document, or empty)
  "filename": "string",    // Media filename (if any)
  "url": "string"          // Media URL (if any)
}
```

- Fields may be empty if not applicable (e.g., no media).
- The `timestamp` field is a string representation of the Go `time.Time` object.

## Example
```
{
  "id": "ABCD1234",
  "chat_jid": "1234567890@s.whatsapp.net",
  "sender": "1234567890",
  "content": "Hello!",
  "timestamp": "2024-06-01T12:34:56Z",
  "is_from_me": false,
  "media_type": "",
  "filename": "",
  "url": ""
}
```

## Notes
- If `WEBHOOK_URL` is not set, no webhook will be called.
- Errors in posting to the webhook are logged but do not interrupt message processing. 
- Currently only support text and media type, template will not get through