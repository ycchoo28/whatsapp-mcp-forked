# WhatsApp Bridge API Documentation

This document outlines the available API endpoints for the WhatsApp Bridge service.

## Configuration

You can configure the WhatsApp Bridge server using environment variables:

- `PORT`: Set the port for the API server (default: 8080)
- `WEBHOOK_URL`: Set a webhook URL to receive notifications for incoming messages

Example:
```bash
PORT=3000 WEBHOOK_URL=http://localhost:8000/webhook go run main.go
```

## Base URL

All API endpoints are served relative to:

```
http://localhost:8080
```

Or if you've set a custom port:

```
http://localhost:{PORT}
```

## Endpoints

### 1. Send Message

Send a text message or media file to a WhatsApp recipient.

**Endpoint:** `POST /api/send`

**Request Body:**
```json
{
  "recipient": "1234567890",   // Phone number or JID (required)
  "message": "Hello world",     // Text message (required if no media_path)
  "media_path": "/path/to/file" // Path to media file (optional)
}
```

**Response:**
```json
{
  "success": true,
  "message": "Message sent to 1234567890"
}
```

**Error Responses:**
- `400 Bad Request` - Missing required parameters
- `500 Internal Server Error` - Failed to send message
- `503 Service Unavailable` - WhatsApp client is not connected

### 2. Send Image from URL

Send an image to a WhatsApp recipient by downloading it from a URL.

**Endpoint:** `POST /api/send-image-url`

**Request Body:**
```json
{
  "recipient": "1234567890",   // Phone number or JID (required)
  "message": "Image caption",   // Caption for the image (optional)
  "image_url": "https://example.com/image.jpg" // URL of the image to send (required)
}
```

**Response:**
```json
{
  "success": true,
  "message": "Message sent to 1234567890"
}
```

**Error Responses:**
- `400 Bad Request` - Missing required parameters
- `500 Internal Server Error` - Failed to download image or send message
- `503 Service Unavailable` - WhatsApp client is not connected

### 3. Get Messages

Retrieve messages from a specific chat.

**Endpoint:** `GET /api/messages`

**Query Parameters:**
- `chat_jid` (required): The JID of the chat to retrieve messages from
- `limit` (optional): Maximum number of messages to retrieve (default: 20)

**Example:**
```
GET /api/messages?chat_jid=1234567890@s.whatsapp.net&limit=10
```

**Success Response:**
```json
{
  "success": true,
  "messages": [
    {
      "Time": "2023-07-15T10:30:45Z",
      "Sender": "1234567890",
      "Content": "Hello there",
      "IsFromMe": false,
      "MediaType": "",
      "Filename": ""
    },
    {
      "Time": "2023-07-15T10:29:30Z",
      "Sender": "9876543210",
      "Content": "How are you?",
      "IsFromMe": true,
      "MediaType": "",
      "Filename": ""
    }
  ]
}
```

**Error Responses:**
- `400 Bad Request` - Missing chat_jid or invalid limit parameter
- `404 Not Found` - Chat not found
- `500 Internal Server Error` - Database error
- `503 Service Unavailable` - WhatsApp client is not connected

**Empty Chat Response:**
```json
{
  "success": true,
  "messages": [],
  "message": "No messages found for this chat"
}
```

### 4. Download Media

Download media from a message.

**Endpoint:** `POST /api/download`

**Request Body:**
```json
{
  "message_id": "MESSAGE_ID",   // ID of the message (required)
  "chat_jid": "CHAT_JID"        // JID of the chat (required)
}
```

**Success Response:**
```json
{
  "success": true,
  "message": "Successfully downloaded image media",
  "filename": "image_20230715_103045.jpg",
  "path": "/absolute/path/to/downloaded/file.jpg"
}
```

**Error Responses:**
- `400 Bad Request` - Missing required parameters
- `404 Not Found` - Message or chat not found
- `500 Internal Server Error` - Failed to download media
- `503 Service Unavailable` - WhatsApp client is not connected

## Using with n8n Workflows

The `/api/messages` endpoint is particularly useful for n8n workflows to retrieve WhatsApp messages directly from the backend:

1. Use an HTTP Request node in n8n
2. Set the Method to GET
3. Set the URL to `http://localhost:8080/api/messages?chat_jid=YOUR_CHAT_JID`
4. Optional: Add `&limit=10` to limit the number of messages returned
5. The response can then be processed in subsequent nodes of your workflow

This allows you to build automated workflows that react to new WhatsApp messages or analyze conversation history.

## Troubleshooting

### Common Error Scenarios

1. **Connection Issues**
   - If you receive a 503 error (Service Unavailable), the WhatsApp client is not connected. Ensure the WhatsApp bridge is running and properly authenticated with your WhatsApp account.
   - If you can't connect to the API at all, verify that the WhatsApp bridge server is running.

2. **Missing or Invalid Data**
   - 400 errors indicate missing or invalid parameters. Check the required parameters for each endpoint.
   - 404 errors usually mean the chat or message you're trying to access doesn't exist in the database.

3. **Database Issues**
   - 500 errors with database-related messages indicate problems with the underlying SQLite database. These may require restarting the server or possibly rebuilding the database.

### Recovering from Errors

If you're experiencing consistent errors, try these recovery steps:

1. Restart the WhatsApp bridge server
2. Verify your WhatsApp authentication status
3. In extreme cases, delete the database files (`store/messages.db` and `store/whatsapp.db`) and restart the server to rebuild them (requires re-authentication)

## API Testing Script

The repository includes a test script (`test_api.sh`) to verify API functionality. This script performs multiple tests against each endpoint to confirm proper operation and error handling.

### Running the Test Script

```bash
# Make the script executable
chmod +x test_api.sh

# Run the test suite
./test_api.sh
```

### What the Script Tests

1. **Message Retrieval** - Gets recent messages from a specified chat
2. **Parameter Validation** - Tests error responses when required parameters are missing
3. **Error Handling** - Verifies proper handling of non-existent resources
4. **Message Sending** - Tests the message sending functionality
5. **Connection Status** - Verifies WhatsApp connection status detection

### Example Output

```
WhatsApp Bridge API Test Script
==============================

Testing: GET Messages
curl -s "http://localhost:8080/api/messages?chat_jid=60124456192@s.whatsapp.net&limit=3"
Response:
{
  "messages": [
    {
      "Time": "2025-03-25T11:57:10+08:00",
      "Sender": "60124456192",
      "Content": "",
      "IsFromMe": true,
      "MediaType": "audio",
      "Filename": "audio_20250528_153524.ogg"
    },
    ...
  ],
  "success": true
}

✓ Test completed
==============================
```

### Modifying the Test Script

You can customize the test script by modifying these variables at the top of the file:

```bash
# Configuration
API_BASE="http://localhost:8080"
CONTACT_JID="60124456192@s.whatsapp.net"  # Replace with an actual contact JID
MESSAGE="Test message from API"
```

This allows you to test with different contacts and message content. 