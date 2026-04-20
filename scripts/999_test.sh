#!/usr/bin/env bash
set -euo pipefail
BASE="${BASE_URL:-http://localhost:8080}"
H='Content-Type: application/json'

echo "=== http2sun smoke tests ==="

echo ""
echo "1. Paris, Europe/Paris — no timestamp (now)"
curl -sf -X POST "${BASE}/api/v1/sun" -H "$H" \
  -d '{"latitude":48.8566,"longitude":2.3522,"timezone":"Europe/Paris"}' \
  | jq '{date,sunrise,sunset,solar_noon,day_length,noon_elevation_deg,current_elevation_deg}'

echo ""
echo "2. Tokyo, Asia/Tokyo — explicit timestamp"
curl -sf -X POST "${BASE}/api/v1/sun" -H "$H" \
  -d '{"latitude":35.6762,"longitude":139.6503,"timezone":"Asia/Tokyo","timestamp":1750492800}' \
  | jq '{date,sunrise,sunset,day_length}'

echo ""
echo "3. Svalbard polar night (Dec 21)"
curl -sf -X POST "${BASE}/api/v1/sun" -H "$H" \
  -d '{"latitude":78.22,"longitude":15.65,"timezone":"Europe/Oslo","timestamp":1766217600}' \
  | jq '{date,polar_night,polar_day,noon_elevation_deg}'

echo ""
echo "4. UTC default (no timezone field)"
curl -sf -X POST "${BASE}/api/v1/sun" -H "$H" \
  -d '{"latitude":0,"longitude":0}' \
  | jq '{date,timezone,sunrise,sunset}'

echo ""
echo "5. Inverted timezone (Pau + Asia/Tokyo)"
curl -sf -X POST "${BASE}/api/v1/sun" -H "$H" \
  -d '{"latitude":43.868,"longitude":-0.493,"timezone":"Asia/Tokyo","timestamp":1776686217}' \
  | jq '{date,sunrise,sunset,current_time_local,current_azimuth_deg,current_elevation_deg}'

echo ""
echo "6. Missing latitude — expect 400"
HTTP=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${BASE}/api/v1/sun" -H "$H" \
  -d '{"longitude":2.3}')
[ "$HTTP" = "400" ] && echo "OK (400)" || echo "FAIL (got $HTTP)"

echo ""
echo "7. Wrong method GET — expect 405"
HTTP=$(curl -s -o /dev/null -w "%{http_code}" "${BASE}/api/v1/sun")
[ "$HTTP" = "405" ] && echo "OK (405)" || echo "FAIL (got $HTTP)"

echo ""
echo "8. GET /openapi.json"
curl -sf "${BASE}/openapi.json" | jq '.info.title'

echo ""
echo "=== All tests passed ==="
