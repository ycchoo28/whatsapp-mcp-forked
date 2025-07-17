package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal"

	"bytes"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// Message represents a chat message for our client
type Message struct {
	Time          time.Time
	Sender        string
	Content       string
	IsFromMe      bool
	MediaType     string
	Filename      string
	QuotedMessage string
}

// SenderWhitelist holds the list of approved senders
var SenderWhitelist map[string]bool

// Database handler for storing message history
type MessageStore struct {
	db *sql.DB
}

// Initialize message store
func NewMessageStore() (*MessageStore, error) {
	// Create directory for database if it doesn't exist
	if err := os.MkdirAll("store", 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %v", err)
	}

	// Open SQLite database for messages
	db, err := sql.Open("sqlite3", "file:store/messages.db?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	// Create tables if they don't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);
		
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			quoted_message TEXT,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	return &MessageStore{db: db}, nil
}

// Close the database connection
func (store *MessageStore) Close() error {
	return store.db.Close()
}

// Store a chat in the database
func (store *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := store.db.Exec(
		"INSERT OR REPLACE INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
		jid, name, lastMessageTime,
	)
	return err
}

// Store a message in the database
func (store *MessageStore) StoreMessage(id, chatJID, sender, content string, timestamp time.Time, isFromMe bool,
	mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64, quotedMessage string) error {
	// Only store if there's actual content or media
	if content == "" && mediaType == "" {
		return nil
	}

	_, err := store.db.Exec(
		`INSERT OR REPLACE INTO messages 
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length, quoted_message) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, quotedMessage,
	)
	return err
}

// Get messages from a chat
func (store *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := store.db.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename, quoted_message FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		var quotedMessage sql.NullString
		err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename, &quotedMessage)
		if err != nil {
			return nil, err
		}
		msg.Time = timestamp
		if quotedMessage.Valid {
			msg.QuotedMessage = quotedMessage.String
		} else {
			msg.QuotedMessage = ""
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// Get all chats
func (store *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		var lastMessageTime time.Time
		err := rows.Scan(&jid, &lastMessageTime)
		if err != nil {
			return nil, err
		}
		chats[jid] = lastMessageTime
	}

	return chats, nil
}

// Extract text content from a message
func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	// Try to get text content
	if text := msg.GetConversation(); text != "" {
		return text
	} else if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	} else if img := msg.GetImageMessage(); img != nil && img.GetCaption() != "" {
		return img.GetCaption()
	} else if vid := msg.GetVideoMessage(); vid != nil && vid.GetCaption() != "" {
		return vid.GetCaption()
	} else if doc := msg.GetDocumentMessage(); doc != nil && doc.GetCaption() != "" {
		return doc.GetCaption()
	} else if proto := msg.GetProtocolMessage(); proto != nil {
		if proto.GetType() == waProto.ProtocolMessage_MESSAGE_EDIT {
			// Handle edited message content
			if edited := proto.GetEditedMessage(); edited != nil {
				if editedText := edited.GetConversation(); editedText != "" {
					return "[EDITED] " + editedText
				} else if editedExtText := edited.GetExtendedTextMessage(); editedExtText != nil {
					return "[EDITED] " + editedExtText.GetText()
				}
			}
		} else if proto.GetType() == waProto.ProtocolMessage_REVOKE {
			// Handle revoked (deleted) message
			return "[MESSAGE DELETED]"
		}
	}

	// For now, we're ignoring non-text messages
	return ""
}

// Extract quoted message content from a message
func extractQuotedMessage(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	// Check for extended text message with context info
	if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		if ctx := extendedText.GetContextInfo(); ctx != nil && ctx.QuotedMessage != nil {
			// Try to extract text from quoted message
			quotedMsg := ctx.QuotedMessage

			// Extract text based on message type
			if text := quotedMsg.GetConversation(); text != "" {
				return text
			} else if extText := quotedMsg.GetExtendedTextMessage(); extText != nil {
				return extText.GetText()
			} else if img := quotedMsg.GetImageMessage(); img != nil && img.GetCaption() != "" {
				return img.GetCaption()
			} else if vid := quotedMsg.GetVideoMessage(); vid != nil && vid.GetCaption() != "" {
				return vid.GetCaption()
			} else if doc := quotedMsg.GetDocumentMessage(); doc != nil && doc.GetCaption() != "" {
				return doc.GetCaption()
			}

			// If we couldn't extract text, return a placeholder based on the message type
			if quotedMsg.GetImageMessage() != nil {
				return "[Image]"
			} else if quotedMsg.GetVideoMessage() != nil {
				return "[Video]"
			} else if quotedMsg.GetAudioMessage() != nil {
				return "[Audio]"
			} else if quotedMsg.GetDocumentMessage() != nil {
				return "[Document]"
			} else if quotedMsg.GetStickerMessage() != nil {
				return "[Sticker]"
			} else if quotedMsg.GetContactMessage() != nil {
				return "[Contact]"
			} else if quotedMsg.GetLocationMessage() != nil {
				return "[Location]"
			}

			// Unknown message type
			return "[Message]"
		}
	}

	return ""
}

// SendMessageResponse represents the response for the send message API
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SendMessageRequest represents the request body for the send message API
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

// SendURLImageRequest represents the request body for sending images via URL
type SendURLImageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	ImageURL  string `json:"image_url"`
}

// ImageBase64Response represents the response for the image base64 API
type ImageBase64Response struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Base64   string `json:"base64,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// Function to send a WhatsApp message
func sendWhatsAppMessage(client *whatsmeow.Client, recipient string, message string, mediaPath string) (bool, string) {
	fmt.Println("sendWhatsAppMessage called with:", recipient, message, mediaPath)

	if !client.IsConnected() {
		fmt.Println("Error: Not connected to WhatsApp")
		return false, "Not connected to WhatsApp"
	}

	// Create JID for recipient
	var recipientJID types.JID
	var err error

	// Check if recipient is a JID
	isJID := strings.Contains(recipient, "@")

	if isJID {
		// Parse the JID string
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			fmt.Println("Error parsing JID:", err)
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		// Create JID from phone number
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net", // For personal chats
		}
	}

	fmt.Println("Recipient JID:", recipientJID.String())

	msg := &waProto.Message{}

	// Check if we have media to send
	if mediaPath != "" {
		// Read media file
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			fmt.Println("Error reading media file:", err)
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		// Determine media type and mime type based on file extension
		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		// Handle different media types
		switch fileExt {
		// Image types
		case "jpg", "jpeg":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/jpeg"
		case "png":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/png"
		case "gif":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/gif"
		case "webp":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/webp"

		// Audio types
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"

		// Video types
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"

		// Document types (for any other file type)
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		// Upload media to WhatsApp servers
		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			fmt.Println("Error uploading media:", err)
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		fmt.Println("Media uploaded", resp)

		// Create the appropriate message type based on media type
		switch mediaType {
		case whatsmeow.MediaImage:
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaAudio:
			// Handle ogg audio files
			var seconds uint32 = 30 // Default fallback
			var waveform []byte = nil

			// Try to analyze the ogg file
			if strings.Contains(mimeType, "ogg") {
				analyzedSeconds, analyzedWaveform, err := analyzeOggOpus(mediaData)
				if err == nil {
					seconds = analyzedSeconds
					waveform = analyzedWaveform
				} else {
					fmt.Println("Failed to analyze Ogg Opus file:", err)
					return false, fmt.Sprintf("Failed to analyze Ogg Opus file: %v", err)
				}
			} else {
				fmt.Printf("Not an Ogg Opus file: %s\n", mimeType)
			}

			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				Seconds:       proto.Uint32(seconds),
				PTT:           proto.Bool(true),
				Waveform:      waveform,
			}
		case whatsmeow.MediaVideo:
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaDocument:
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(mediaPath[strings.LastIndex(mediaPath, "/")+1:]),
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	// Send message
	fmt.Println("Sending message to:", recipientJID.String())
	resp, err := client.SendMessage(context.Background(), recipientJID, msg)

	if err != nil {
		fmt.Println("Error sending message:", err)
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	fmt.Println("Message sent successfully with ID:", resp.ID)

	// Create an events.Message struct for the outgoing message
	outgoingMsg := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     recipientJID,                  // The recipient is the chat
				Sender:   client.Store.ID.ToNonAD(),     // Our own JID
				IsFromMe: true,                          // Mark as sent by us
				IsGroup:  recipientJID.Server == "g.us", // Check if it's a group
			},
			ID:        resp.ID,    // Message ID from the send response
			Timestamp: time.Now(), // Current time
		},
		Message: msg, // The original message object we sent
	}

	// Manually dispatch the event to trigger the same handlers as incoming messages
	client.DangerousInternals().DispatchEvent(outgoingMsg)

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// Extract media info from a message
func extractMediaInfo(msg *waProto.Message) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	// Check for image message
	if img := msg.GetImageMessage(); img != nil {
		return "image", "image_" + time.Now().Format("20060102_150405") + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}

	// Check for video message
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video_" + time.Now().Format("20060102_150405") + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}

	// Check for audio message
	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", "audio_" + time.Now().Format("20060102_150405") + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}

	// Check for document message
	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document_" + time.Now().Format("20060102_150405")
		}
		return "document", filename,
			doc.GetURL(), doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256(), doc.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}

// formatOrderAsNaturalLanguage converts order details to a natural language string
func formatOrderAsNaturalLanguage(node *waBinary.Node) string {
	if node == nil {
		return ""
	}

	// Find the order node in the response
	var orderNode *waBinary.Node
	for _, content := range node.GetChildren() {
		if content.Tag == "order" {
			orderNode = &content
			break
		}
	}

	if orderNode == nil {
		return ""
	}

	type productInfo struct {
		name     string
		quantity string
	}
	var products []productInfo

	// Extract product information
	for _, child := range orderNode.GetChildren() {
		if child.Tag == "product" {
			var p productInfo
			for _, productChild := range child.GetChildren() {
				if productChild.Tag == "name" && productChild.Content != nil {
					p.name = string(productChild.Content.([]byte))
				} else if productChild.Tag == "quantity" && productChild.Content != nil {
					p.quantity = string(productChild.Content.([]byte))
				}
			}
			// Set defaults if not found
			if p.quantity == "" {
				p.quantity = "1"
			}
			if p.name != "" {
				products = append(products, p)
			}
		}
	}

	// Format the order in natural language
	if len(products) > 0 {
		var productStrings []string
		for _, p := range products {
			productStrings = append(productStrings, fmt.Sprintf("%s x%s", p.name, p.quantity))
		}
		// Format: "我想购买: 全麦葡萄干核桃馒头 x1, 奶香芋泥馒 x1"
		return fmt.Sprintf("我想购买: %s", strings.Join(productStrings, ", "))
	}

	return ""
}

// Handle regular incoming messages with media support
func handleMessage(client *whatsmeow.Client, messageStore *MessageStore, msg *events.Message, logger waLog.Logger) {
	// Save message to database
	chatJID := msg.Info.Chat.String()
	sender := msg.Info.Sender.User

	// Check whitelist
	if !isWhitelistedOrSelf(msg.Info.IsFromMe, sender, logger) {
		return
	}

	// Get chat name and update chat record
	name := GetChatName(client, messageStore, msg.Info.Chat, chatJID, nil, sender, logger)
	logger.Infof("Chat name: %s", name)
	if err := messageStore.StoreChat(chatJID, name, msg.Info.Timestamp); err != nil {
		logger.Warnf("Failed to store chat: %v", err)
	}

	// Check for special message types
	isEditedMessage, isRevokedMessage, originalMessageID := checkSpecialMessageTypes(msg, logger)

	// Extract message content and media info
	content := extractTextContent(msg.Message)
	quotedMessage := extractQuotedMessage(msg.Message)
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message)

	// Process order message if present
	isOrder, orderID, orderFormatted := processOrderMessage(client, msg.Message, &content, logger)

	// Skip processing if no content to save
	if shouldSkipMessage(content, mediaType, isRevokedMessage, isOrder, isEditedMessage, logger, msg.Message) {
		return
	}

	fmt.Println("Processing message", content, mediaType)

	// Handle edited or revoked messages
	if (isEditedMessage || isRevokedMessage) && originalMessageID != "" {
		handleEditedOrRevokedMessage(messageStore, isRevokedMessage, originalMessageID, chatJID, content, msg.Info.Timestamp, logger)
	} else {
		// Store new message
		storeNewMessage(messageStore, msg.Info.ID, chatJID, sender, content, msg.Info.Timestamp,
			msg.Info.IsFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256,
			fileLength, quotedMessage, logger)
	}

	// Send webhook for eligible messages
	if isEligibleForWebhook(msg, chatJID, isRevokedMessage, logger) {
		sendWebhook(msg.Info.ID, chatJID, sender, content, msg.Info.Timestamp,
			msg.Info.IsFromMe, mediaType, filename, url, quotedMessage,
			isEditedMessage, originalMessageID, isOrder, orderID, orderFormatted, logger)
	}
}

// Check if sender is whitelisted or message is from self
func isWhitelistedOrSelf(isFromMe bool, sender string, logger waLog.Logger) bool {
	if isFromMe {
		return true
	}

	if len(SenderWhitelist) > 0 {
		if !SenderWhitelist[sender] {
			logger.Infof("Ignoring message from non-whitelisted sender: %s", sender)
			return false
		}
		logger.Infof("Processing whitelisted message from: %s", sender)
	}

	return true
}

// Check for edited or revoked messages
func checkSpecialMessageTypes(msg *events.Message, logger waLog.Logger) (isEdited bool, isRevoked bool, originalID string) {
	if proto := msg.Message.GetProtocolMessage(); proto != nil {
		if proto.GetKey() != nil {
			originalID = proto.GetKey().GetId()
		}

		switch proto.GetType() {
		case waProto.ProtocolMessage_MESSAGE_EDIT:
			isEdited = true
			logger.Infof("Processing edited message (original ID: %s)", originalID)
		case waProto.ProtocolMessage_REVOKE:
			isRevoked = true
			logger.Infof("Processing revoked message (original ID: %s)", originalID)
		}
	}

	return isEdited, isRevoked, originalID
}

// Process order message and update content if needed
func processOrderMessage(client *whatsmeow.Client, msg *waProto.Message, content *string, logger waLog.Logger) (bool, string, string) {
	orderID, token, isOrder := ExtractOrderFromMessage(msg)
	if !isOrder {
		return false, "", ""
	}

	logger.Infof("Detected order message with ID: %s", orderID)

	// Get order details
	orderDetails, err := GetOrderDetails(client, orderID, token)
	if err != nil {
		logger.Warnf("Failed to get order details: %v", err)
		return true, orderID, ""
	}

	logger.Infof("Retrieved order details successfully")

	// Format order as natural language
	orderFormatted := formatOrderAsNaturalLanguage(orderDetails)
	if orderFormatted != "" {
		// If we have a formatted order string, append it to the message content
		if *content != "" {
			*content += "\n" + orderFormatted
		} else {
			*content = orderFormatted
		}

		logger.Infof("Formatted order: %s", orderFormatted)
	}

	return true, orderID, orderFormatted
}

// Determine if message should be skipped
func shouldSkipMessage(content string, mediaType string, isRevokedMessage bool, isOrder bool, isEditedMessage bool, logger waLog.Logger, msg *waProto.Message) bool {
	if content == "" && mediaType == "" && !isRevokedMessage && !isOrder {
		// Log edit message for debugging even if we skip it
		if isEditedMessage {
			logger.Infof("Skipping edited message with no content/media: %v", msg)
		} else {
			logger.Infof("Skipping message with no content/media")
		}
		return true
	}

	return false
}

// Handle edited or revoked messages
func handleEditedOrRevokedMessage(messageStore *MessageStore, isRevokedMessage bool, originalID string, chatJID string, content string, timestamp time.Time, logger waLog.Logger) {
	var err error

	if isRevokedMessage {
		err = messageStore.MarkMessageAsDeleted(originalID, chatJID, timestamp)
		if err != nil {
			logger.Warnf("Failed to mark message as deleted: %v", err)
		} else {
			logger.Infof("Successfully marked message as deleted (ID: %s)", originalID)
		}
	} else {
		// Must be an edited message
		err = messageStore.UpdateEditedMessage(originalID, chatJID, content, timestamp)
		if err != nil {
			logger.Warnf("Failed to update edited message: %v", err)
		} else {
			logger.Infof("Successfully updated edited message (ID: %s): %s", originalID, content)
		}
	}
}

// Store a new message in the database and log it
func storeNewMessage(messageStore *MessageStore, msgID string, chatJID string, sender string, content string,
	timestamp time.Time, isFromMe bool, mediaType string, filename string, url string, mediaKey []byte,
	fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64, quotedMessage string, logger waLog.Logger) {

	err := messageStore.StoreMessage(
		msgID, chatJID, sender, content, timestamp, isFromMe,
		mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, quotedMessage,
	)

	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
		return
	}

	// Log message reception
	logTime := timestamp.Format("2006-01-02 15:04:05")
	direction := "←"
	if isFromMe {
		direction = "→"
	}

	// Log based on message type
	if mediaType != "" {
		logger.Infof("[%s] %s %s: [%s: %s] %s", logTime, direction, sender, mediaType, filename, content)
	} else if content != "" {
		logger.Infof("[%s] %s %s: %s", logTime, direction, sender, content)
	}

	// Log quoted message if present
	if quotedMessage != "" {
		logger.Infof("  ↳ Reply to: %s", quotedMessage)
	}
}

// Send webhook notification
func sendWebhook(msgID string, chatJID string, sender string, content string, timestamp time.Time,
	isFromMe bool, mediaType string, filename string, url string, quotedMessage string,
	isEditedMessage bool, originalMessageID string, isOrder bool, orderID string, orderFormatted string, logger waLog.Logger) {

	// Prepare webhook payload
	webhookPayload := map[string]interface{}{
		"id":             msgID,
		"chat_jid":       chatJID,
		"sender":         sender,
		"content":        content,
		"timestamp":      timestamp,
		"is_from_me":     isFromMe,
		"media_type":     mediaType,
		"filename":       filename,
		"url":            url,
		"quoted_message": quotedMessage,
		"is_edited":      isEditedMessage,
	}

	// Add order details to webhook payload if available
	if isOrder {
		webhookPayload["is_order"] = true
		webhookPayload["order_id"] = orderID
		if orderFormatted != "" {
			webhookPayload["order_formatted"] = orderFormatted
		}
	}

	if isEditedMessage {
		webhookPayload["original_message_id"] = originalMessageID
	}

	// Marshal payload to JSON
	jsonPayload, err := json.Marshal(webhookPayload)
	if err != nil {
		logger.Warnf("Failed to marshal webhook payload: %v", err)
		return
	}

	// Get webhook URL from environment
	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		logger.Warnf("WEBHOOK_URL is not set")
		return
	}

	// Send webhook request
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		logger.Warnf("Failed to POST to webhook %s: %v", webhookURL, err)
		return
	}

	defer resp.Body.Close()
	logger.Infof("Sent to webhook %s", webhookURL)
}

// DownloadMediaRequest represents the request body for the download media API
type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

// DownloadMediaResponse represents the response for the download media API
type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Store additional media info in the database
func (store *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := store.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

// Get media info from the database
func (store *MessageStore) GetMediaInfo(id, chatJID string) (string, string, string, []byte, []byte, []byte, uint64, error) {
	var mediaType, filename sql.NullString
	var url sql.NullString
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength sql.NullInt64

	err := store.db.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	// Convert nullable types to their non-nullable equivalents
	mediaTypeStr := ""
	if mediaType.Valid {
		mediaTypeStr = mediaType.String
	}

	filenameStr := ""
	if filename.Valid {
		filenameStr = filename.String
	}

	urlStr := ""
	if url.Valid {
		urlStr = url.String
	}

	fileLengthVal := uint64(0)
	if fileLength.Valid {
		fileLengthVal = uint64(fileLength.Int64)
	}

	return mediaTypeStr, filenameStr, urlStr, mediaKey, fileSHA256, fileEncSHA256, fileLengthVal, err
}

// MediaDownloader implements the whatsmeow.DownloadableMessage interface
type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

// GetDirectPath implements the DownloadableMessage interface
func (d *MediaDownloader) GetDirectPath() string {
	return d.DirectPath
}

// GetURL implements the DownloadableMessage interface
func (d *MediaDownloader) GetURL() string {
	return d.URL
}

// GetMediaKey implements the DownloadableMessage interface
func (d *MediaDownloader) GetMediaKey() []byte {
	return d.MediaKey
}

// GetFileLength implements the DownloadableMessage interface
func (d *MediaDownloader) GetFileLength() uint64 {
	return d.FileLength
}

// GetFileSHA256 implements the DownloadableMessage interface
func (d *MediaDownloader) GetFileSHA256() []byte {
	return d.FileSHA256
}

// GetFileEncSHA256 implements the DownloadableMessage interface
func (d *MediaDownloader) GetFileEncSHA256() []byte {
	return d.FileEncSHA256
}

// GetMediaType implements the DownloadableMessage interface
func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType {
	return d.MediaType
}

// Function to download media from a message
func downloadMedia(client *whatsmeow.Client, messageStore *MessageStore, messageID, chatJID string) (bool, string, string, string, error) {
	// Query the database for the message
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64
	var err error

	// First, check if we already have this file
	chatDir := fmt.Sprintf("store/%s", strings.ReplaceAll(chatJID, ":", "_"))
	localPath := ""

	// Get media info from the database
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err = messageStore.GetMediaInfo(messageID, chatJID)

	if err != nil {
		// Try to get basic info if extended info isn't available
		var mediaTypeNull, filenameNull sql.NullString
		err = messageStore.db.QueryRow(
			"SELECT media_type, filename FROM messages WHERE id = ? AND chat_jid = ?",
			messageID, chatJID,
		).Scan(&mediaTypeNull, &filenameNull)

		if err != nil {
			return false, "", "", "", fmt.Errorf("failed to find message: %v", err)
		}

		if mediaTypeNull.Valid {
			mediaType = mediaTypeNull.String
		}

		if filenameNull.Valid {
			filename = filenameNull.String
		}
	}

	// Check if this is a media message
	if mediaType == "" {
		return false, "", "", "", fmt.Errorf("not a media message")
	}

	// Create directory for the chat if it doesn't exist
	if err := os.MkdirAll(chatDir, 0755); err != nil {
		return false, "", "", "", fmt.Errorf("failed to create chat directory: %v", err)
	}

	// Generate a local path for the file
	localPath = fmt.Sprintf("%s/%s", chatDir, filename)

	// Get absolute path
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	// Check if file already exists
	if _, err := os.Stat(localPath); err == nil {
		// File exists, return it
		return true, mediaType, filename, absPath, nil
	}

	// If we don't have all the media info we need, we can't download
	if url == "" || len(mediaKey) == 0 || len(fileSHA256) == 0 || len(fileEncSHA256) == 0 || fileLength == 0 {
		return false, "", "", "", fmt.Errorf("incomplete media information for download")
	}

	fmt.Printf("Attempting to download media for message %s in chat %s...\n", messageID, chatJID)

	// Extract direct path from URL
	directPath := extractDirectPathFromURL(url)

	// Create a downloader that implements DownloadableMessage
	var waMediaType whatsmeow.MediaType
	switch mediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		return false, "", "", "", fmt.Errorf("unsupported media type: %s", mediaType)
	}

	downloader := &MediaDownloader{
		URL:           url,
		DirectPath:    directPath,
		MediaKey:      mediaKey,
		FileLength:    fileLength,
		FileSHA256:    fileSHA256,
		FileEncSHA256: fileEncSHA256,
		MediaType:     waMediaType,
	}

	// Download the media using whatsmeow client
	mediaData, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %v", err)
	}

	// Save the downloaded media to file
	if err := os.WriteFile(localPath, mediaData, 0644); err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media file: %v", err)
	}

	fmt.Printf("Successfully downloaded %s media to %s (%d bytes)\n", mediaType, absPath, len(mediaData))
	return true, mediaType, filename, absPath, nil
}

// Extract direct path from a WhatsApp media URL
func extractDirectPathFromURL(url string) string {
	// The direct path is typically in the URL, we need to extract it
	// Example URL: https://mmg.whatsapp.net/v/t62.7118-24/13812002_698058036224062_3424455886509161511_n.enc?ccb=11-4&oh=...

	// Find the path part after the domain
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url // Return original URL if parsing fails
	}

	pathPart := parts[1]

	// Remove query parameters
	pathPart = strings.SplitN(pathPart, "?", 2)[0]

	// Create proper direct path format
	return "/" + pathPart
}

// Start a REST API server to expose the WhatsApp client functionality
func startRESTServer(client *whatsmeow.Client, messageStore *MessageStore, port int) {
	// Get logger reference for the REST server
	logger := waLog.Stdout("REST", "INFO", true)

	// Handler for sending messages
	http.HandleFunc("/api/send", func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if client is connected to WhatsApp
		if !client.IsConnected() {
			logger.Warnf("API call failed: WhatsApp client not connected")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: "WhatsApp client is not connected. Please ensure the service is properly authenticated and connected.",
			})
			return
		}

		// Parse the request body
		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warnf("API call failed: Invalid request format: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: fmt.Sprintf("Invalid request format: %v", err),
			})
			return
		}

		// Validate request
		if req.Recipient == "" {
			logger.Warnf("API call failed: Recipient is required")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: "Recipient is required",
			})
			return
		}

		if req.Message == "" && req.MediaPath == "" {
			logger.Warnf("API call failed: Message or media path is required")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: "Message or media path is required",
			})
			return
		}

		// Check media path if specified
		if req.MediaPath != "" {
			if _, err := os.Stat(req.MediaPath); os.IsNotExist(err) {
				logger.Warnf("API call failed: Media file not found: %s", req.MediaPath)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(SendMessageResponse{
					Success: false,
					Message: fmt.Sprintf("Media file not found: %s", req.MediaPath),
				})
				return
			}
		}

		logger.Infof("Received request to send message to %s", req.Recipient)

		// Send the message
		success, message := sendWhatsAppMessage(client, req.Recipient, req.Message, req.MediaPath)

		// Set response headers
		w.Header().Set("Content-Type", "application/json")

		// Set appropriate status code
		if !success {
			logger.Warnf("Failed to send message: %s", message)
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			logger.Infof("Message sent successfully: %s", message)
		}

		// Send response
		json.NewEncoder(w).Encode(SendMessageResponse{
			Success: success,
			Message: message,
		})
	})

	// Handler for sending images from URL
	http.HandleFunc("/api/send-image-url", func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if client is connected to WhatsApp
		if !client.IsConnected() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: "WhatsApp client is not connected. Please ensure the service is properly authenticated and connected.",
			})
			return
		}

		// Parse the request body
		var req SendURLImageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: fmt.Sprintf("Invalid request format: %v", err),
			})
			return
		}

		// Validate request
		if req.Recipient == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: "Recipient is required",
			})
			return
		}

		if req.ImageURL == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: "Image URL is required",
			})
			return
		}

		logger.Infof("Received request to send image from URL to %s", req.Recipient)

		// Download the image from URL
		tempFilePath, err := downloadImageFromURL(req.ImageURL)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(SendMessageResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to download image: %v", err),
			})
			return
		}

		// Clean up the temporary file after sending
		defer func() {
			if err := os.Remove(tempFilePath); err != nil {
				logger.Warnf("Failed to remove temporary file %s: %v", tempFilePath, err)
			} else {
				logger.Infof("Removed temporary file %s", tempFilePath)
			}
		}()

		// Send the message using the existing function
		success, message := sendWhatsAppMessage(client, req.Recipient, req.Message, tempFilePath)

		// Set response headers
		w.Header().Set("Content-Type", "application/json")

		// Set appropriate status code
		if !success {
			w.WriteHeader(http.StatusInternalServerError)
		}

		// Send response
		json.NewEncoder(w).Encode(SendMessageResponse{
			Success: success,
			Message: message,
		})
	})

	// Handler for getting messages
	http.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		// Only allow GET requests
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if client is connected to WhatsApp
		if !client.IsConnected() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "WhatsApp client is not connected",
				"message": "The WhatsApp connection is not active. Please ensure the service is properly authenticated and connected.",
			})
			return
		}

		// Parse query parameters
		chatJID := r.URL.Query().Get("chat_jid")
		if chatJID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Missing required parameter: chat_jid",
				"message": "The chat_jid parameter is required to identify which chat to retrieve messages from",
			})
			return
		}

		// Parse limit parameter with default value
		limit := 20
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			} else if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   "Invalid limit parameter: must be a positive number",
					"message": "The limit parameter must be a valid positive integer",
				})
				return
			}
		}

		// Check if chat exists
		var chatExists bool
		err := messageStore.db.QueryRow("SELECT EXISTS(SELECT 1 FROM chats WHERE jid = ?)", chatJID).Scan(&chatExists)
		if err != nil {
			logger.Warnf("Database error checking chat existence: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Database error",
				"message": fmt.Sprintf("Failed to check if chat exists: %v", err),
			})
			return
		}

		if !chatExists {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Chat not found",
				"message": fmt.Sprintf("No chat found with JID: %s", chatJID),
			})
			return
		}

		// Get messages
		messages, err := messageStore.GetMessages(chatJID, limit)

		// Set response headers
		w.Header().Set("Content-Type", "application/json")

		// Handle errors
		if err != nil {
			logger.Warnf("Error retrieving messages: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Database error",
				"message": fmt.Sprintf("Failed to retrieve messages: %v", err),
			})
			return
		}

		// Handle case where no messages were found
		if len(messages) == 0 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"messages": []struct{}{},
				"message":  "No messages found for this chat",
			})
			return
		}

		// Send successful response
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"messages": messages,
		})
	})

	// Handler for downloading media
	http.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if client is connected to WhatsApp
		if !client.IsConnected() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: "WhatsApp client is not connected. Please ensure the service is properly authenticated and connected.",
			})
			return
		}

		// Parse the request body
		var req DownloadMediaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Invalid request format: %v", err),
			})
			return
		}

		// Validate request
		if req.MessageID == "" || req.ChatJID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: "Message ID and Chat JID are required",
			})
			return
		}

		// Check if message exists
		var messageExists bool
		err := messageStore.db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE id = ? AND chat_jid = ?)",
			req.MessageID, req.ChatJID).Scan(&messageExists)
		if err != nil {
			logger.Warnf("Database error checking message existence: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Database error: %v", err),
			})
			return
		}

		if !messageExists {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Message not found with ID %s in chat %s", req.MessageID, req.ChatJID),
			})
			return
		}

		// Download the media
		success, mediaType, filename, path, err := downloadMedia(client, messageStore, req.MessageID, req.ChatJID)

		// Set response headers
		w.Header().Set("Content-Type", "application/json")

		// Handle download result
		if !success || err != nil {
			errMsg := "Unknown error"
			if err != nil {
				errMsg = err.Error()
			}

			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to download media: %s", errMsg),
			})
			return
		}

		// Send successful response
		json.NewEncoder(w).Encode(DownloadMediaResponse{
			Success:  true,
			Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
			Filename: filename,
			Path:     path,
		})
	})

	// Handler for getting image as base64
	http.HandleFunc("/api/image-base64", func(w http.ResponseWriter, r *http.Request) {
		logger.Infof("Received request for /api/image-base64 from %s", r.RemoteAddr)

		// Allow both GET and POST requests
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			logger.Warnf("Method not allowed: %s", r.Method)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Get parameters either from query string (GET) or request body (POST)
		var chatJID, filename string
		var deleteAfterSend bool

		if r.Method == http.MethodGet {
			chatJID = r.URL.Query().Get("chat_jid")
			filename = r.URL.Query().Get("filename")
			deleteParam := r.URL.Query().Get("delete_after_send")
			deleteAfterSend = deleteParam == "true" || deleteParam == "1" || deleteParam == "yes"
			logger.Debugf("GET request parameters - chat_jid: %s, filename: %s, delete_after_send: %v", chatJID, filename, deleteAfterSend)
		} else {
			// Parse POST body
			if err := r.ParseForm(); err != nil {
				logger.Errorf("Failed to parse form data: %v", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(ImageBase64Response{
					Success: false,
					Message: fmt.Sprintf("Failed to parse form data: %v", err),
				})
				return
			}
			chatJID = r.FormValue("chat_jid")
			filename = r.FormValue("filename")
			deleteParam := r.FormValue("delete_after_send")
			deleteAfterSend = deleteParam == "true" || deleteParam == "1" || deleteParam == "yes"
			logger.Debugf("POST request parameters - chat_jid: %s, filename: %s, delete_after_send: %v", chatJID, filename, deleteAfterSend)
		}

		// Validate parameters
		if chatJID == "" || filename == "" {
			logger.Warnf("Missing required parameters - chat_jid: %s, filename: %s", chatJID, filename)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ImageBase64Response{
				Success: false,
				Message: "chat_jid and filename are required parameters",
			})
			return
		}

		// Sanitize the chat JID for use in a path
		sanitizedChatJID := strings.ReplaceAll(chatJID, ":", "_")

		// Construct the path where the file should be
		chatDir := fmt.Sprintf("store/%s", sanitizedChatJID)
		filePath := filepath.Join(chatDir, filename)
		logger.Debugf("Looking for file at path: %s", filePath)

		// Track if the file was downloaded just for this request
		wasDownloadedForThisRequest := false

		// Check if file exists
		fileExists := true
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			logger.Warnf("File not found at path: %s, will attempt to download", filePath)
			fileExists = false

			// Try to find the message ID in the database
			messageID, err := messageStore.FindMessageIDByFilename(chatJID, filename)
			if err != nil {
				logger.Errorf("Failed to find message ID for file %s in chat %s: %v", filename, chatJID, err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(ImageBase64Response{
					Success: false,
					Message: fmt.Sprintf("File not found and unable to locate message in database: %v", err),
				})
				return
			}

			logger.Infof("Found message ID %s for file %s, attempting to download", messageID, filename)

			// Try to download the file
			success, mediaType, _, downloadedPath, err := downloadMedia(client, messageStore, messageID, chatJID)
			if err != nil || !success {
				errMsg := "Unknown error"
				if err != nil {
					errMsg = err.Error()
				}
				logger.Errorf("Failed to download file: %s", errMsg)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(ImageBase64Response{
					Success: false,
					Message: fmt.Sprintf("Failed to download file: %s", errMsg),
				})
				return
			}

			logger.Infof("Successfully downloaded file to %s (type: %s)", downloadedPath, mediaType)
			filePath = downloadedPath
			fileExists = true
			wasDownloadedForThisRequest = true
		}

		// At this point, we should have the file either already existed or was downloaded
		if !fileExists {
			logger.Warnf("File still not available after download attempt: %s", filePath)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ImageBase64Response{
				Success: false,
				Message: "File could not be retrieved",
			})
			return
		}

		// Read the file
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			logger.Errorf("Failed to read file %s: %v", filePath, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ImageBase64Response{
				Success: false,
				Message: fmt.Sprintf("Failed to read file: %v", err),
			})
			return
		}

		// Determine MIME type based on file extension
		fileExt := strings.ToLower(filename[strings.LastIndex(filename, ".")+1:])
		var mimeType string
		switch fileExt {
		case "jpg", "jpeg":
			mimeType = "image/jpeg"
		case "png":
			mimeType = "image/png"
		case "gif":
			mimeType = "image/gif"
		case "webp":
			mimeType = "image/webp"
		default:
			mimeType = "application/octet-stream"
		}
		logger.Debugf("Detected MIME type: %s for file extension: %s", mimeType, fileExt)

		// Encode to base64
		base64Data := base64.StdEncoding.EncodeToString(fileData)
		logger.Infof("Successfully encoded file %s to base64 (size: %d bytes)", filename, len(base64Data))

		// Return success response with base64 data
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ImageBase64Response{
			Success:  true,
			Message:  "File successfully encoded to base64",
			Filename: filename,
			Base64:   base64Data,
			MimeType: mimeType,
		})
		logger.Infof("Successfully responded to request for file %s", filename)

		// Delete the file if requested or if it was downloaded just for this request
		if deleteAfterSend || wasDownloadedForThisRequest {
			logger.Infof("Deleting file after sending response: %s", filePath)
			if err := os.Remove(filePath); err != nil {
				logger.Errorf("Failed to delete file %s: %v", filePath, err)
			} else {
				logger.Infof("Successfully deleted file: %s", filePath)
			}
		}
	})

	// Handler for getting a PDF file directly
	http.HandleFunc("/api/get-pdf", func(w http.ResponseWriter, r *http.Request) {
		logger.Infof("Received request for /api/get-pdf from %s", r.RemoteAddr)

		// Allow both GET and POST requests
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			logger.Warnf("Method not allowed: %s", r.Method)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Get parameters either from query string (GET) or request body (POST)
		var chatJID, filename string
		var deleteAfterSend bool

		if r.Method == http.MethodGet {
			chatJID = r.URL.Query().Get("chat_jid")
			filename = r.URL.Query().Get("filename")
			deleteParam := r.URL.Query().Get("delete_after_send")
			deleteAfterSend = deleteParam == "true" || deleteParam == "1" || deleteParam == "yes"
			logger.Debugf("GET request parameters - chat_jid: %s, filename: %s, delete_after_send: %v", chatJID, filename, deleteAfterSend)
		} else {
			// Parse POST body
			if err := r.ParseForm(); err != nil {
				logger.Errorf("Failed to parse form data: %v", err)
				http.Error(w, fmt.Sprintf("Failed to parse form data: %v", err), http.StatusBadRequest)
				return
			}
			chatJID = r.FormValue("chat_jid")
			filename = r.FormValue("filename")
			deleteParam := r.FormValue("delete_after_send")
			deleteAfterSend = deleteParam == "true" || deleteParam == "1" || deleteParam == "yes"
			logger.Debugf("POST request parameters - chat_jid: %s, filename: %s, delete_after_send: %v", chatJID, filename, deleteAfterSend)
		}

		// Validate parameters
		if chatJID == "" || filename == "" {
			logger.Warnf("Missing required parameters - chat_jid: %s, filename: %s", chatJID, filename)
			http.Error(w, "chat_jid and filename are required parameters", http.StatusBadRequest)
			return
		}

		// Sanitize the chat JID for use in a path
		sanitizedChatJID := strings.ReplaceAll(chatJID, ":", "_")

		// Construct the path where the file should be
		chatDir := fmt.Sprintf("store/%s", sanitizedChatJID)
		filePath := filepath.Join(chatDir, filename)
		logger.Debugf("Looking for file at path: %s", filePath)

		// Track if the file was downloaded just for this request
		wasDownloadedForThisRequest := false

		// Check if file exists
		fileExists := true
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			logger.Warnf("File not found at path: %s, will attempt to download", filePath)
			fileExists = false

			// Try to find the message ID in the database
			messageID, err := messageStore.FindMessageIDByFilename(chatJID, filename)
			if err != nil {
				logger.Errorf("Failed to find message ID for file %s in chat %s: %v", filename, chatJID, err)
				http.Error(w, fmt.Sprintf("File not found and unable to locate message in database: %v", err), http.StatusNotFound)
				return
			}

			logger.Infof("Found message ID %s for file %s, attempting to download", messageID, filename)

			// Try to download the file
			success, _, _, downloadedPath, err := downloadMedia(client, messageStore, messageID, chatJID)
			if err != nil || !success {
				errMsg := "Unknown error"
				if err != nil {
					errMsg = err.Error()
				}
				logger.Errorf("Failed to download file: %s", errMsg)
				http.Error(w, fmt.Sprintf("Failed to download file: %s", errMsg), http.StatusInternalServerError)
				return
			}

			logger.Infof("Successfully downloaded file to %s", downloadedPath)
			filePath = downloadedPath
			fileExists = true
			wasDownloadedForThisRequest = true
		}

		// At this point, we should have the file either already existed or was downloaded
		if !fileExists {
			logger.Warnf("File still not available after download attempt: %s", filePath)
			http.Error(w, "File could not be retrieved", http.StatusInternalServerError)
			return
		}

		// Read the file
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			logger.Errorf("Failed to read file %s: %v", filePath, err)
			http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusInternalServerError)
			return
		}

		// Set headers and send the file
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fileData)))

		if _, err := w.Write(fileData); err != nil {
			logger.Errorf("Failed to write PDF file to response: %v", err)
		} else {
			logger.Infof("Successfully sent PDF file %s", filename)
		}

		// Delete the file if requested or if it was downloaded just for this request
		if deleteAfterSend || wasDownloadedForThisRequest {
			logger.Infof("Deleting file after sending response: %s", filePath)
			if err := os.Remove(filePath); err != nil {
				logger.Errorf("Failed to delete file %s: %v", filePath, err)
			} else {
				logger.Infof("Successfully deleted file: %s", filePath)
			}
		}
	})

	// Start the server
	serverAddr := fmt.Sprintf("0.0.0.0:%d", port)
	fmt.Printf("Starting REST API server on %s...\n", serverAddr)

	// Run server in a goroutine so it doesn't block
	go func() {
		if err := http.ListenAndServe(serverAddr, nil); err != nil {
			fmt.Printf("REST API server error: %v\n", err)
		}
	}()
}

func main() {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Warning: .env file not found or failed to load")
	}

	// Set up logger
	logger := waLog.Stdout("Client", "INFO", true)
	logger.Infof("Starting WhatsApp client...")

	// Initialize whitelist from environment variable
	initWhitelist(logger)

	// Create database connection for storing session data
	dbLog := waLog.Stdout("Database", "INFO", true)

	// Create directory for database if it doesn't exist
	if err := os.MkdirAll("store", 0755); err != nil {
		logger.Errorf("Failed to create store directory: %v", err)
		return
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", dbLog)
	if err != nil {
		logger.Errorf("Failed to connect to database: %v", err)
		return
	}

	// Get device store - This contains session information
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			// No device exists, create one
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			logger.Errorf("Failed to get device: %v", err)
			return
		}
	}

	// Create client instance
	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		logger.Errorf("Failed to create WhatsApp client")
		return
	}

	// Initialize message store
	messageStore, err := NewMessageStore()
	if err != nil {
		logger.Errorf("Failed to initialize message store: %v", err)
		return
	}
	defer messageStore.Close()

	// Setup event handling for messages and history sync
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			// Process regular messages
			handleMessage(client, messageStore, v, logger)

		case *events.HistorySync:
			// Process history sync events
			handleHistorySync(client, messageStore, v, logger)

		case *events.Connected:
			logger.Infof("Connected to WhatsApp")

		case *events.LoggedOut:
			logger.Warnf("Device logged out, please scan QR code to log in again")
		}
	})

	// Create channel to track connection success
	connected := make(chan bool, 1)

	// Connect to WhatsApp
	if client.Store.ID == nil {
		// No ID stored, this is a new client, need to pair with phone
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			logger.Errorf("Failed to connect: %v", err)
			return
		}

		// Print QR code for pairing with phone
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("\nScan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "success" {
				connected <- true
				break
			}
		}

		// Wait for connection
		select {
		case <-connected:
			fmt.Println("\nSuccessfully connected and authenticated!")
		case <-time.After(3 * time.Minute):
			logger.Errorf("Timeout waiting for QR code scan")
			return
		}
	} else {
		// Already logged in, just connect
		err = client.Connect()
		if err != nil {
			logger.Errorf("Failed to connect: %v", err)
			return
		}
		connected <- true
	}

	// Wait a moment for connection to stabilize
	time.Sleep(2 * time.Second)

	if !client.IsConnected() {
		logger.Errorf("Failed to establish stable connection")
		return
	}

	fmt.Println("\n✓ Connected to WhatsApp! Type 'help' for commands.")

	// Start REST API server
	port := 8080 // Default port
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
			logger.Infof("Using port %d from environment variable", port)
		} else if err != nil {
			logger.Warnf("Invalid PORT environment variable: %s, using default port %d", portStr, port)
		}
	}
	startRESTServer(client, messageStore, port)

	// Create a channel to keep the main goroutine alive
	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("REST server is running. Press Ctrl+C to disconnect and exit.")

	// Wait for termination signal
	<-exitChan

	fmt.Println("Disconnecting...")
	// Disconnect client
	client.Disconnect()
}

// GetChatName determines the appropriate name for a chat based on JID and other info
func GetChatName(client *whatsmeow.Client, messageStore *MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, logger waLog.Logger) string {
	// First, check if chat already exists in database with a name
	var existingName string
	err := messageStore.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&existingName)
	if err == nil && existingName != "" {
		// Chat exists with a name, use that
		logger.Infof("Using existing chat name for %s: %s", chatJID, existingName)
		return existingName
	}

	// Need to determine chat name
	var name string

	if jid.Server == "g.us" {
		// This is a group chat
		logger.Infof("Getting name for group: %s", chatJID)

		// Use conversation data if provided (from history sync)
		if conversation != nil {
			// Extract name from conversation if available
			// This uses type assertions to handle different possible types
			var displayName, convName *string
			// Try to extract the fields we care about regardless of the exact type
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()

				// Try to find DisplayName field
				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}

				// Try to find Name field
				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}

			// Use the name we found
			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		// If we didn't get a name, try group info
		if name == "" {
			groupInfo, err := client.GetGroupInfo(jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				// Fallback name for groups
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}

		logger.Infof("Using group name: %s", name)
	} else {
		// This is an individual contact
		logger.Infof("Getting name for contact: %s", chatJID)

		// Just use contact info (full name)
		contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else if sender != "" {
			// Fallback to sender
			name = sender
		} else {
			// Last fallback to JID
			name = jid.User
		}

		logger.Infof("Using contact name: %s", name)
	}

	return name
}

// Handle history sync events
func handleHistorySync(client *whatsmeow.Client, messageStore *MessageStore, historySync *events.HistorySync, logger waLog.Logger) {
	fmt.Printf("Received history sync event with %d conversations\n", len(historySync.Data.Conversations))

	syncedCount := 0
	for _, conversation := range historySync.Data.Conversations {
		// Parse JID from the conversation
		if conversation.ID == nil {
			continue
		}

		chatJID := *conversation.ID

		// Try to parse the JID
		jid, err := types.ParseJID(chatJID)
		if err != nil {
			logger.Warnf("Failed to parse JID %s: %v", chatJID, err)
			continue
		}

		// Get appropriate chat name by passing the history sync conversation directly
		name := GetChatName(client, messageStore, jid, chatJID, conversation, "", logger)

		// Process messages
		messages := conversation.Messages
		if len(messages) > 0 {
			// Update chat with latest message timestamp
			latestMsg := messages[0]
			if latestMsg == nil || latestMsg.Message == nil {
				continue
			}

			// Get timestamp from message info
			timestamp := time.Time{}
			if ts := latestMsg.Message.GetMessageTimestamp(); ts != 0 {
				timestamp = time.Unix(int64(ts), 0)
			} else {
				continue
			}

			messageStore.StoreChat(chatJID, name, timestamp)

			// Store messages
			for _, msg := range messages {
				if msg == nil || msg.Message == nil {
					continue
				}

				// Extract text content
				var content string
				if msg.Message.Message != nil {
					if conv := msg.Message.Message.GetConversation(); conv != "" {
						content = conv
					} else if ext := msg.Message.Message.GetExtendedTextMessage(); ext != nil {
						content = ext.GetText()
					}
				}

				// Extract quoted message content
				var quotedMessage string
				if msg.Message.Message != nil {
					quotedMessage = extractQuotedMessage(msg.Message.Message)
				}

				// Extract media info
				var mediaType, filename, url string
				var mediaKey, fileSHA256, fileEncSHA256 []byte
				var fileLength uint64

				if msg.Message.Message != nil {
					mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message)
				}

				// Log the message content for debugging
				logger.Infof("Message content: %v, Media Type: %v", content, mediaType)

				// Skip messages with no content and no media
				if content == "" && mediaType == "" {
					continue
				}

				// Determine sender
				var sender string
				isFromMe := false
				if msg.Message.Key != nil {
					if msg.Message.Key.FromMe != nil {
						isFromMe = *msg.Message.Key.FromMe
					}
					if !isFromMe && msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "" {
						sender = *msg.Message.Key.Participant
					} else if isFromMe {
						sender = client.Store.ID.User
					} else {
						sender = jid.User
					}
				} else {
					sender = jid.User
				}

				// If whitelist is enabled (non-empty) and this is not from the user,
				// check if sender is in the whitelist
				if len(SenderWhitelist) > 0 && !isFromMe {
					if !SenderWhitelist[sender] {
						logger.Infof("Skipping history message from non-whitelisted sender: %s", sender)
						continue
					}
					logger.Infof("Processing history message from whitelisted sender: %s", sender)
				}

				// Store message
				msgID := ""
				if msg.Message.Key != nil && msg.Message.Key.ID != nil {
					msgID = *msg.Message.Key.ID
				}

				// Get message timestamp
				timestamp := time.Time{}
				if ts := msg.Message.GetMessageTimestamp(); ts != 0 {
					timestamp = time.Unix(int64(ts), 0)
				} else {
					continue
				}

				err = messageStore.StoreMessage(
					msgID,
					chatJID,
					sender,
					content,
					timestamp,
					isFromMe,
					mediaType,
					filename,
					url,
					mediaKey,
					fileSHA256,
					fileEncSHA256,
					fileLength,
					quotedMessage,
				)
				if err != nil {
					logger.Warnf("Failed to store history message: %v", err)
				} else {
					syncedCount++
					// Log successful message storage
					if mediaType != "" {
						logger.Infof("Stored message: [%s] %s -> %s: [%s: %s] %s",
							timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, mediaType, filename, content)
					} else {
						logger.Infof("Stored message: [%s] %s -> %s: %s",
							timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, content)
					}
				}
			}
		}
	}

	fmt.Printf("History sync complete. Stored %d messages.\n", syncedCount)
}

// Request history sync from the server
func requestHistorySync(client *whatsmeow.Client) {
	if client == nil {
		fmt.Println("Client is not initialized. Cannot request history sync.")
		return
	}

	if !client.IsConnected() {
		fmt.Println("Client is not connected. Please ensure you are connected to WhatsApp first.")
		return
	}

	if client.Store.ID == nil {
		fmt.Println("Client is not logged in. Please scan the QR code first.")
		return
	}

	// Build and send a history sync request
	historyMsg := client.BuildHistorySyncRequest(nil, 100)
	if historyMsg == nil {
		fmt.Println("Failed to build history sync request.")
		return
	}

	_, err := client.SendMessage(context.Background(), types.JID{
		Server: "s.whatsapp.net",
		User:   "status",
	}, historyMsg)

	if err != nil {
		fmt.Printf("Failed to request history sync: %v\n", err)
	} else {
		fmt.Println("History sync requested. Waiting for server response...")
	}
}

// analyzeOggOpus tries to extract duration and generate a simple waveform from an Ogg Opus file
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	// Try to detect if this is a valid Ogg file by checking for the "OggS" signature
	// at the beginning of the file
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	// Parse Ogg pages to find the last page with a valid granule position
	var lastGranule uint64
	var sampleRate uint32 = 48000 // Default Opus sample rate
	var preSkip uint16 = 0
	var foundOpusHead bool

	// Scan through the file looking for Ogg pages
	for i := 0; i < len(data); {
		// Check if we have enough data to read Ogg page header
		if i+27 >= len(data) {
			break
		}

		// Verify Ogg page signature
		if string(data[i:i+4]) != "OggS" {
			// Skip until next potential page
			i++
			continue
		}

		// Extract header fields
		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])

		// Extract segment table
		if i+27+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+27 : i+27+numSegments]

		// Calculate page size
		pageSize := 27 + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		// Check if we're looking at an OpusHead packet (should be in first few pages)
		if !foundOpusHead && pageSeqNum <= 1 {
			// Look for "OpusHead" marker in this page
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				// Found OpusHead, extract sample rate and pre-skip
				// OpusHead format: Magic(8) + Version(1) + Channels(1) + PreSkip(2) + SampleRate(4) + ...
				headPos += 8 // Skip "OpusHead" marker
				// PreSkip is 2 bytes at offset 10
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
					fmt.Printf("Found OpusHead: sampleRate=%d, preSkip=%d\n", sampleRate, preSkip)
				}
			}
		}

		// Keep track of last valid granule position
		if granulePos != 0 {
			lastGranule = granulePos
		}

		// Move to next page
		i += pageSize
	}

	if !foundOpusHead {
		fmt.Println("Warning: OpusHead not found, using default values")
	}

	// Calculate duration based on granule position
	if lastGranule > 0 {
		// Formula for duration: (lastGranule - preSkip) / sampleRate
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
		fmt.Printf("Calculated Opus duration from granule: %f seconds (lastGranule=%d)\n",
			durationSeconds, lastGranule)
	} else {
		// Fallback to rough estimation if granule position not found
		fmt.Println("Warning: No valid granule position found, using estimation")
		durationEstimate := float64(len(data)) / 2000.0 // Very rough approximation
		duration = uint32(durationEstimate)
	}

	// Make sure we have a reasonable duration (at least 1 second, at most 300 seconds)
	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}

	// Generate waveform
	waveform = placeholderWaveform(duration)

	fmt.Printf("Ogg Opus analysis: size=%d bytes, calculated duration=%d sec, waveform=%d bytes\n",
		len(data), duration, len(waveform))

	return duration, waveform, nil
}

// min returns the smaller of x or y
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// placeholderWaveform generates a synthetic waveform for WhatsApp voice messages
// that appears natural with some variability based on the duration
func placeholderWaveform(duration uint32) []byte {
	// WhatsApp expects a 64-byte waveform for voice messages
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	// Seed the random number generator for consistent results with the same duration
	rand.Seed(int64(duration))

	// Create a more natural looking waveform with some patterns and variability
	// rather than completely random values

	// Base amplitude and frequency - longer messages get faster frequency
	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		// Position in the waveform (normalized 0-1)
		pos := float64(i) / float64(waveformLength)

		// Create a wave pattern with some randomness
		// Use multiple sine waves of different frequencies for more natural look
		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		// Add some randomness to make it look more natural
		val += (rand.Float64() - 0.5) * 15

		// Add some fade-in and fade-out effects
		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)

		// Center around 50 (typical voice baseline)
		val = val + 50

		// Ensure values stay within WhatsApp's expected range (0-100)
		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}

// Initialize whitelist from environment variable
func initWhitelist(logger waLog.Logger) {
	// Create an empty whitelist map
	SenderWhitelist = make(map[string]bool)

	// Check for whitelist in environment
	whitelistStr := os.Getenv("WHATSAPP_WHITELIST")
	if whitelistStr == "" {
		// No whitelist defined, process all messages
		logger.Infof("Whitelist not enabled - processing all messages")
		return
	}

	// Split the string by commas
	whitelist := strings.Split(whitelistStr, ",")

	for _, number := range whitelist {
		number = strings.TrimSpace(number)
		if number != "" {
			SenderWhitelist[number] = true
			logger.Infof("Added %s to whitelist", number)
		}
	}

	logger.Infof("Whitelist enabled: Only processing messages from %d whitelisted numbers", len(SenderWhitelist))
}

// Function to download an image from URL and save it to a temporary file
func downloadImageFromURL(imageURL string) (string, error) {
	// Create a temporary directory if it doesn't exist
	tempDir := filepath.Join("store", "temp_media")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %v", err)
	}

	// Generate a unique filename based on timestamp and random number
	timestamp := time.Now().UnixNano()
	randomNum := rand.Intn(10000)
	filename := fmt.Sprintf("%d_%d", timestamp, randomNum)

	// Extract file extension from URL
	urlPath := strings.Split(imageURL, "?")[0] // Remove query parameters
	ext := filepath.Ext(urlPath)

	if ext == "" {
		// Default to .jpg if no extension found
		ext = ".jpg"
	}

	// Create full temporary file path
	tempFilePath := filepath.Join(tempDir, filename+ext)

	// Download the file
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %v", err)
	}
	defer resp.Body.Close()

	// Check if response is OK
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image, status code: %d", resp.StatusCode)
	}

	// Create the temporary file
	out, err := os.Create(tempFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer out.Close()

	// Copy the response body to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save downloaded image: %v", err)
	}

	return tempFilePath, nil
}

// Find message ID by chat_jid and filename
func (store *MessageStore) FindMessageIDByFilename(chatJID string, filename string) (string, error) {
	var messageID sql.NullString
	err := store.db.QueryRow(
		"SELECT id FROM messages WHERE chat_jid = ? AND filename = ? LIMIT 1",
		chatJID, filename,
	).Scan(&messageID)

	if err != nil {
		return "", err
	}

	if messageID.Valid {
		return messageID.String, nil
	}

	return "", fmt.Errorf("message ID is NULL")
}

// Update an edited message in the database
func (store *MessageStore) UpdateEditedMessage(originalID, chatJID, content string, timestamp time.Time) error {
	_, err := store.db.Exec(
		"UPDATE messages SET content = ?, timestamp = ? WHERE id = ? AND chat_jid = ?",
		content, timestamp, originalID, chatJID,
	)
	return err
}

// Mark a message as deleted in the database
func (store *MessageStore) MarkMessageAsDeleted(originalID, chatJID string, timestamp time.Time) error {
	_, err := store.db.Exec(
		"UPDATE messages SET content = '[MESSAGE DELETED]', timestamp = ? WHERE id = ? AND chat_jid = ?",
		timestamp, originalID, chatJID,
	)
	return err
}

// InfoQueryType represents the type of IQ query
type InfoQueryType string

const (
	// GetInfoQuery represents a "get" IQ query
	GetInfoQuery InfoQueryType = "get"
	// SetInfoQuery represents a "set" IQ query
	SetInfoQuery InfoQueryType = "set"
	// ResultInfoQuery represents a "result" IQ query
	ResultInfoQuery InfoQueryType = "result"
)

// InfoQuery represents a WhatsApp IQ query
type InfoQuery struct {
	Namespace string
	Type      InfoQueryType
	To        types.JID
	Target    types.JID
	ID        string
	SmaxId    string
	Content   []waBinary.Node
}

// generateRequestID creates a unique request ID for IQ queries
func generateRequestID() string {
	return fmt.Sprintf("%d.%d%d", time.Now().Unix(), rand.Intn(1000), rand.Intn(1000))
}

// sendIQ sends an IQ query and waits for the response
func sendIQ(client *whatsmeow.Client, query InfoQuery) (*waBinary.Node, error) {
	// If no ID is set, generate one
	if len(query.ID) == 0 {
		query.ID = generateRequestID()
	}

	// Prepare the attributes for the IQ node
	attrs := waBinary.Attrs{
		"id":    query.ID,
		"xmlns": query.Namespace,
		"type":  string(query.Type),
	}

	// Add smax_id if provided
	if len(query.SmaxId) > 0 {
		attrs["smax_id"] = query.SmaxId
	}

	// Add 'to' attribute if JID is not empty
	if !query.To.IsEmpty() {
		attrs["to"] = query.To
	}

	// Add 'target' attribute if JID is not empty
	if !query.Target.IsEmpty() {
		attrs["target"] = query.Target
	}

	// Create the IQ node
	node := waBinary.Node{
		Tag:     "iq",
		Attrs:   attrs,
		Content: query.Content,
	}

	// Register a response waiter before sending the request
	respChan := client.DangerousInternals().WaitResponse(query.ID)

	// Send the node
	err := client.DangerousInternals().SendNode(node)
	if err != nil {
		client.DangerousInternals().CancelResponse(query.ID, respChan)
		return nil, fmt.Errorf("failed to send IQ query: %v", err)
	}

	// Wait for response
	select {
	case resp := <-respChan:
		return resp, nil
	case <-time.After(30 * time.Second):
		client.DangerousInternals().CancelResponse(query.ID, respChan)
		return nil, fmt.Errorf("timeout waiting for IQ response")
	}
}

// GetOrderDetails retrieves the details of an order by its ID and token
func GetOrderDetails(client *whatsmeow.Client, orderID, tokenBase64 string) (*waBinary.Node, error) {
	// Create order content nodes
	imageDimensionsContent := []waBinary.Node{
		{
			Tag:     "width",
			Content: []byte("100"),
		},
		{
			Tag:     "height",
			Content: []byte("100"),
		},
	}

	// Create the image dimensions node
	imageDimensionsNode := waBinary.Node{
		Tag:     "image_dimensions",
		Content: imageDimensionsContent,
	}

	// Create the token node
	tokenNode := waBinary.Node{
		Tag:     "token",
		Content: []byte(tokenBase64),
	}

	// Create the order node
	orderNode := waBinary.Node{
		Tag: "order",
		Attrs: waBinary.Attrs{
			"op": "get",
			"id": orderID,
		},
		Content: []waBinary.Node{imageDimensionsNode, tokenNode},
	}

	// Prepare the IQ query
	query := InfoQuery{
		Namespace: "fb:thrift_iq",
		Type:      GetInfoQuery,
		To:        types.ServerJID,
		SmaxId:    "5", // Fixed value for order details
		Content:   []waBinary.Node{orderNode},
	}

	// Send the IQ query and get the response
	response, err := sendIQ(client, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get order details: %v", err)
	}

	// Log the raw response for debugging
	fmt.Printf("Order details raw response: %+v\n", response)

	// Decode and print human-readable order details
	decodeOrderDetails(response)

	return response, nil
}

// decodeOrderDetails parses a binary node response and prints human-readable order details
func decodeOrderDetails(node *waBinary.Node) {
	if node == nil {
		fmt.Println("No order details to decode")
		return
	}

	// Find the order node in the response
	var orderNode *waBinary.Node
	for _, content := range node.GetChildren() {
		if content.Tag == "order" {
			orderNode = &content
			break
		}
	}

	if orderNode == nil {
		fmt.Println("Order node not found in response")
		return
	}

	// Print order basic info
	fmt.Println("\n===== ORDER DETAILS =====")
	fmt.Printf("Order ID: %s\n", orderNode.AttrGetter().String("id"))
	fmt.Printf("Creation Timestamp: %s\n", orderNode.AttrGetter().String("creation_ts"))

	// Find product info
	for _, child := range orderNode.GetChildren() {
		if child.Tag == "product" {
			fmt.Println("\n--- PRODUCT INFO ---")

			// Extract product ID
			for _, productChild := range child.GetChildren() {
				if productChild.Tag == "id" {
					productID := string(productChild.Content.([]byte))
					fmt.Printf("Product ID: %s\n", productID)
				} else if productChild.Tag == "name" {
					productName := string(productChild.Content.([]byte))
					fmt.Printf("Product Name: %s\n", productName)
				} else if productChild.Tag == "price" {
					price := string(productChild.Content.([]byte))
					fmt.Printf("Price: %s\n", price)
				} else if productChild.Tag == "currency" {
					currency := string(productChild.Content.([]byte))
					fmt.Printf("Currency: %s\n", currency)
				} else if productChild.Tag == "quantity" {
					quantity := string(productChild.Content.([]byte))
					fmt.Printf("Quantity: %s\n", quantity)
				} else if productChild.Tag == "image" {
					fmt.Println("--- IMAGE INFO ---")
					for _, imageChild := range productChild.GetChildren() {
						if imageChild.Tag == "url" && imageChild.Content != nil {
							imageURL := string(imageChild.Content.([]byte))
							fmt.Printf("Image URL: %s\n", imageURL)
						} else if imageChild.Tag == "id" && imageChild.Content != nil {
							imageID := string(imageChild.Content.([]byte))
							fmt.Printf("Image ID: %s\n", imageID)
						}
					}
				}
			}
		} else if child.Tag == "catalog" {
			fmt.Println("\n--- CATALOG INFO ---")
			for _, catalogChild := range child.GetChildren() {
				if catalogChild.Tag == "id" && catalogChild.Content != nil {
					catalogID := string(catalogChild.Content.([]byte))
					fmt.Printf("Catalog ID: %s\n", catalogID)
				}
			}
		} else if child.Tag == "price" {
			fmt.Println("\n--- PRICE DETAILS ---")
			for _, priceChild := range child.GetChildren() {
				if priceChild.Content != nil {
					fmt.Printf("%s: %s\n", priceChild.Tag, string(priceChild.Content.([]byte)))
				}
			}
		}
	}

	fmt.Println("========================\n")
}

// ExtractOrderFromMessage attempts to extract order details from a message
// This function can be used to detect when a message contains an order
// and extract the relevant information needed to retrieve order details.
//
// Usage example:
//
//	orderID, token, isOrder := ExtractOrderFromMessage(msg.Message)
//	if isOrder {
//	    orderDetails, err := GetOrderDetails(client, orderID, token)
//	    // Process order details...
//	}
func ExtractOrderFromMessage(msg *waProto.Message) (orderID string, token string, ok bool) {
	if msg == nil {
		return "", "", false
	}

	// Check for order message
	if orderMsg := msg.GetOrderMessage(); orderMsg != nil {
		// Extract order ID and token
		if orderMsg.OrderID != nil && orderMsg.Token != nil {
			return *orderMsg.OrderID, *orderMsg.Token, true
		}
	}

	return "", "", false
}

// Check if a message is eligible for webhook notification
func isEligibleForWebhook(msg *events.Message, chatJID string, isRevokedMessage bool, logger waLog.Logger) bool {
	// Don't send webhook for messages from self
	if msg.Info.IsFromMe {
		return false
	}
	
	// Don't send webhook for revoked messages
	if isRevokedMessage {
		return false
	}
	
	// Don't send webhook for group messages
	if msg.Info.IsGroup {
		return false
	}
	
	// Don't send webhook for @lid JIDs
	if strings.HasSuffix(chatJID, "@lid") {
		logger.Infof("Skipping webhook for message from @lid JID: %s", chatJID)
		return false
	}
	
	return true
}
