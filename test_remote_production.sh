#!/bin/bash

# Production Testing Script for MCP Web Scrape
# Tests all 5 phases of anti-bot evasion on remote server

echo "🧪 MCP Web Scrape - Production Testing"
echo "======================================"
echo ""
echo "Testing Phase 1-5 on real protected sites"
echo ""

SERVER_URL="${1:-http://localhost:8192}"

echo "📊 Configuration:"
echo "   Server: $SERVER_URL"
echo "   Timeout: 60s per request"
echo ""

# Test scenarios
declare -A SCENARIOS=(
    ["1. Basic Site"]="https://example.com|Baseline|Should always work"
    ["2. Bot Detection"]="https://bot.sannysoft.com|Phase 3|Stealth score test"
    ["3. Pixel Scan"]="https://pixelscan.net|Phase 3+4|Fingerprinting test"
    ["4. Are You Headless"]="https://arh.antoinevastel.com/bots/areyouheadless|Phase 3|Headless detection"
    ["5. TLS Test"]="https://tls.peet.ws|Phase 4|TLS fingerprint test"
    ["6. Cloudflare Site"]="https://nowsecure.nl|Phase 5|Retry loop test"
)

# Function to perform scrape test
test_scrape() {
    local name="$1"
    local url="$2"
    local phase="$3"
    local description="$4"

    echo "🧪 Test: $name"
    echo "   URL: $url"
    echo "   Phase: $phase"
    echo "   Description: $description"

    local start_time=$(date +%s)

    # Make request
    response=$(curl -s -X POST "$SERVER_URL/tools/scrape_with_js" \
        -H "Content-Type: application/json" \
        -d "{
            \"url\": \"$url\",
            \"stealth_enabled\": true,
            \"stealth_scroll\": true,
            \"stealth_mouse\": true,
            \"wait_time_ms\": 3000,
            \"output_format\": \"html\"
        }" \
        -w "\n%{http_code}")

    local end_time=$(date +%s)
    local duration=$((end_time - start_time))

    # Extract HTTP code (last line)
    local http_code=$(echo "$response" | tail -n1)

    if [ "$http_code" = "200" ]; then
        echo "   ✅ Success! (${duration}s)"
        echo "   📊 Response received"
    else
        echo "   ❌ Failed! HTTP $http_code (${duration}s)"
        echo "   📄 Response (first 200 chars):"
        echo "$response" | head -c 200
        echo ""
    fi

    echo ""
    sleep 2  # Don't overwhelm the server
}

# Run tests
for scenario in "${!SCENARIOS[@]}"; do
    IFS='|' read -r url phase description <<< "${SCENARIOS[$scenario]}"
    test_scrape "$scenario" "$url" "$phase" "$description"
done

echo "======================================"
echo "✅ Testing Complete!"
echo ""
echo "📝 Check logs for details:"
echo "   docker logs mcp-web-scrape | grep -E 'Phase|Stealth|TLS|Retry'"
echo ""
echo "🎯 Key Metrics to Check:"
echo "   - Phase 3: Look for 'Extended Stealth' in logs"
echo "   - Phase 4: Look for 'TLS fingerprinting' in logs"
echo "   - Phase 5: Look for 'Retry attempt' in logs"
echo "   - Success rate per site type"
echo "   - Performance (duration)"
echo ""
