# Using WhatsApp Bridge with n8n

This guide explains how to integrate the WhatsApp Bridge REST API with [n8n](https://n8n.io/) to create automated workflows involving WhatsApp messages.

## Prerequisites

- WhatsApp Bridge server running and authenticated
- [n8n](https://n8n.io/) installed and running
- Basic understanding of n8n workflows

## Server Configuration

You can customize the WhatsApp Bridge server using environment variables:

```bash
# Start the server with a custom port (default is 8080)
PORT=3000 go run main.go
```

When using a custom port, make sure to update your n8n workflow's HTTP requests to use the correct port:

```
http://localhost:3000/api/messages?chat_jid=YOUR_CHAT_JID
```

## Available API Endpoints

The WhatsApp Bridge exposes three main REST API endpoints that can be used with n8n:

1. **GET /api/messages** - Retrieve messages from a chat
2. **POST /api/send** - Send messages to WhatsApp contacts
3. **POST /api/download** - Download media from messages

## Testing the API Before Integration

Before setting up n8n workflows, it's recommended to verify that the API is working correctly:

```bash
# Navigate to the WhatsApp bridge directory
cd whatsapp-bridge

# Make the test script executable
chmod +x test_api.sh

# Run the test script
./test_api.sh
```

The test script will check all API endpoints and display detailed responses. Make sure that:
- The WhatsApp Bridge server is running
- Your WhatsApp account is properly authenticated
- The connection status is active

If the test for sending messages returns "Not connected to WhatsApp", you may need to restart the WhatsApp Bridge server to re-establish the connection.

## Example Workflows

### 1. Monitoring WhatsApp Messages

This workflow retrieves the latest messages from a specific chat and processes them.

#### n8n Workflow Setup:

1. **HTTP Request Node**
   - Method: `GET`
   - URL: `http://localhost:8080/api/messages?chat_jid=CONTACT_JID&limit=10`
   - Replace `CONTACT_JID` with the JID of the chat (e.g., `1234567890@s.whatsapp.net`)
   - Authentication: None

2. **Item Lists Node**
   - This processes the messages array from the response
   - Connect this to the HTTP Request node

3. **IF Node**
   - Add a condition to check for specific message content
   - Example: `{{$json.Content.includes("keyword")}}`

4. **Action Nodes**
   - Connect different action nodes based on the content of the messages
   - Examples: Send email, make API calls, trigger webhooks, etc.

#### Example n8n Workflow Definition:

```json
{
  "nodes": [
    {
      "parameters": {
        "url": "http://localhost:8080/api/messages?chat_jid=1234567890@s.whatsapp.net&limit=5",
        "method": "GET",
        "options": {}
      },
      "name": "Get WhatsApp Messages",
      "type": "n8n-nodes-base.httpRequest",
      "id": "1"
    },
    {
      "parameters": {
        "fieldToSplitOut": "messages",
        "options": {}
      },
      "name": "Process Messages",
      "type": "n8n-nodes-base.itemLists",
      "id": "2"
    },
    {
      "parameters": {
        "conditions": {
          "string": [
            {
              "value1": "={{$json.Content}}",
              "operation": "contains",
              "value2": "help"
            }
          ]
        }
      },
      "name": "Help Message Check",
      "type": "n8n-nodes-base.if",
      "id": "3"
    }
  ],
  "connections": {
    "Get WhatsApp Messages": {
      "main": [
        [
          {
            "node": "Process Messages",
            "type": "main",
            "index": 0
          }
        ]
      ]
    },
    "Process Messages": {
      "main": [
        [
          {
            "node": "Help Message Check",
            "type": "main",
            "index": 0
          }
        ]
      ]
    }
  }
}
```

### 2. Automatically Sending WhatsApp Responses

This workflow sends an automatic response when a specific trigger occurs.

#### n8n Workflow Setup:

1. **Trigger Node** (Choose any suitable trigger)
   - Example: Schedule, Webhook, Database trigger, etc.

2. **HTTP Request Node**
   - Method: `POST`
   - URL: `http://localhost:8080/api/send`
   - Headers: Content-Type: application/json
   - Body:
     ```json
     {
       "recipient": "1234567890",
       "message": "This is an automated response from n8n!"
     }
     ```

#### Example n8n Workflow Definition:

```json
{
  "nodes": [
    {
      "parameters": {
        "rule": {
          "interval": [
            {
              "field": "days",
              "minutesInterval": 1
            }
          ]
        }
      },
      "name": "Schedule Trigger",
      "type": "n8n-nodes-base.scheduleTrigger",
      "id": "1"
    },
    {
      "parameters": {
        "url": "http://localhost:8080/api/send",
        "method": "POST",
        "bodyParametersUi": {
          "parameter": [
            {
              "name": "recipient",
              "value": "1234567890"
            },
            {
              "name": "message",
              "value": "This is a daily reminder from your n8n workflow!"
            }
          ]
        },
        "options": {
          "bodyContentType": "json"
        }
      },
      "name": "Send WhatsApp Message",
      "type": "n8n-nodes-base.httpRequest",
      "id": "2"
    }
  ],
  "connections": {
    "Schedule Trigger": {
      "main": [
        [
          {
            "node": "Send WhatsApp Message",
            "type": "main",
            "index": 0
          }
        ]
      ]
    }
  }
}
```

### 3. Downloading and Processing Media

This workflow downloads media from WhatsApp messages and processes it.

#### n8n Workflow Setup:

1. **HTTP Request Node (Get Messages)**
   - Configure as in example 1

2. **Item Lists Node**
   - This processes the messages array from the response
   - Connect this to the HTTP Request node

3. **IF Node**
   - Add a condition to check for media messages
   - Condition: `{{$json.MediaType !== ""}}`

4. **HTTP Request Node (Download Media)**
   - Method: `POST`
   - URL: `http://localhost:8080/api/download`
   - Headers: Content-Type: application/json
   - Body:
     ```json
     {
       "message_id": "={{$json.ID}}",
       "chat_jid": "={{$json.ChatJID}}"
     }
     ```
   - Connect this to the "true" output of the IF node

5. **Action Nodes**
   - Process the downloaded media as needed
   - Examples: File nodes, image processing, etc.

## Error Handling

All API endpoints return standardized error responses. In n8n, you can handle these errors using the Error node:

1. **Connect the Error node** to your HTTP Request nodes
2. **Add conditions** based on the error response:
   - Check the HTTP status code (`$.error.status`)
   - Examine the error message (`$.error.response.body.message`)

## Best Practices

1. **Rate Limiting**: Avoid excessive API calls to prevent overloading the WhatsApp Bridge server
2. **Error Handling**: Always include error handling in your workflows
3. **Security**: Secure your n8n instance if it's publicly accessible
4. **Monitoring**: Set up notifications for workflow failures

## Troubleshooting

If your n8n workflow can't connect to the WhatsApp Bridge API:

1. Verify that the WhatsApp Bridge server is running
2. Check that the WhatsApp client is connected and authenticated
3. Ensure the correct port is specified in the API URL (default: 8080)
4. Check network connectivity between n8n and WhatsApp Bridge
5. Examine the response errors in n8n for specific error messages 