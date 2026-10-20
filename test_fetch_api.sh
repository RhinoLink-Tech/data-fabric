#!/bin/bash

# Test script for fetch API endpoints.
#
# /api/v1/fetch always requires a valid Bearer token (#6). Mint one with the
# server's JWT_SECRET (requires pyjwt) and export it first:
#   python3 -c "import jwt,time; print(jwt.encode({'userId':'dev','username':'dev','roles':['admin'],'iat':int(time.time()),'exp':int(time.time())+86400}, 'YOUR_JWT_SECRET', algorithm='HS256'))"
#   export FETCH_TOKEN=<that token>

echo "=== Testing Data Fabric Fetch API ==="
echo ""

BASE_URL="${BASE_URL:-http://localhost:8080}"
: "${FETCH_TOKEN:?FETCH_TOKEN must be set to a JWT minted with the server's JWT_SECRET}"

# Test 1: Fetch API POST (requires Bearer authentication)
echo "1. Testing authenticated fetch API POST /api/v1/fetch"
curl -X POST "$BASE_URL/api/v1/fetch" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $FETCH_TOKEN" \
  -d '{
    "dataSourceId": "12345678-1234-4234-8234-123456789012",
    "sql": "SELECT * FROM test_table LIMIT 1000",
    "type": 1,
    "batch_size": 100,
    "timeout": 60
  }'

echo -e "\n\n"

# Test 2: Fetch API GET next batch (Bearer token + per-session path token)
echo "2. Testing authenticated fetch API GET /api/v1/fetch/{query_id}/{slug}/{token}"
echo "   (replace the path placeholders with the queryId/slug/token from the POST response)"
curl -X GET "$BASE_URL/api/v1/fetch/test-query-id/test-slug/test-token?batch_size=50" \
  -H "Authorization: Bearer $FETCH_TOKEN"

echo -e "\n\n"

echo "=== Test completed ==="
