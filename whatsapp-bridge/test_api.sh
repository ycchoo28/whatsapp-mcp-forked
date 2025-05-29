#!/bin/bash
# Test script for WhatsApp Bridge API

# Configuration
PORT=${PORT:-8080}  # Use PORT env var if set, otherwise default to 8080
API_BASE="http://localhost:$PORT"
CONTACT_JID="60124456192@s.whatsapp.net"  # Replace with an actual contact JID
MESSAGE="Test message from API"

# Color codes for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}WhatsApp Bridge API Test Script${NC}"
echo -e "Testing API at ${API_BASE}"
echo "=============================="
echo ""

# Function to test an endpoint
test_endpoint() {
    local name=$1
    local command=$2
    
    echo -e "${YELLOW}Testing: ${name}${NC}"
    echo "$command"
    
    # Run the command and capture output
    output=$(eval $command)
    exit_code=$?
    
    # Display response
    echo -e "Response:"
    echo "$output" | jq . 2>/dev/null || echo "$output"
    echo ""
    
    if [ $exit_code -eq 0 ]; then
        echo -e "${GREEN}✓ Test completed${NC}"
    else
        echo -e "${RED}✗ Test failed with exit code $exit_code${NC}"
    fi
    echo "=============================="
    echo ""
}

# Test 1: Get Messages
test_endpoint "GET Messages" "curl -s \"$API_BASE/api/messages?chat_jid=$CONTACT_JID&limit=3\""

# Test 2: Get Messages with invalid parameters
test_endpoint "GET Messages (Bad Request - Missing JID)" "curl -s \"$API_BASE/api/messages\""

# Test 3: Get Messages with non-existent chat
test_endpoint "GET Messages (Not Found)" "curl -s \"$API_BASE/api/messages?chat_jid=nonexistent@s.whatsapp.net\""

# Test 4: Send Message
test_endpoint "Send Message" "curl -s -X POST \"$API_BASE/api/send\" -H \"Content-Type: application/json\" -d '{\"recipient\":\"$CONTACT_JID\",\"message\":\"$MESSAGE\"}'"

# Test 5: Send Message with invalid parameters
test_endpoint "Send Message (Bad Request)" "curl -s -X POST \"$API_BASE/api/send\" -H \"Content-Type: application/json\" -d '{\"message\":\"$MESSAGE\"}'"

echo -e "${GREEN}All tests completed!${NC}" 