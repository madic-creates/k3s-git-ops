#!/bin/bash
# CheckMK Notification Script for Claude Alert Analyzer
# Sends alert data as JSON to the claude-checkmk-analyzer webhook.
#
# CheckMK provides notification data via environment variables prefixed with NOTIFY_
# See: https://docs.checkmk.com/latest/en/notifications.html#environment

set -euo pipefail

WEBHOOK_URL="${NOTIFY_PARAMETER_1:-http://claude-checkmk-analyzer.monitoring:8080/webhook}"
WEBHOOK_SECRET="${NOTIFY_PARAMETER_2:-}"

if [ -z "$WEBHOOK_SECRET" ]; then
  echo "ERROR: WEBHOOK_SECRET not set (pass as notification parameter 2)"
  exit 2
fi

# Build JSON payload from CheckMK environment variables
PAYLOAD=$(cat <<EOF
{
  "hostname": "${NOTIFY_HOSTNAME:-}",
  "host_address": "${NOTIFY_HOSTADDRESS:-}",
  "service_description": "${NOTIFY_SERVICEDESC:-}",
  "service_state": "${NOTIFY_SERVICESTATE:-}",
  "service_output": "${NOTIFY_SERVICEOUTPUT:-}",
  "host_state": "${NOTIFY_HOSTSTATE:-}",
  "notification_type": "${NOTIFY_NOTIFICATIONTYPE:-}",
  "perf_data": "${NOTIFY_SERVICEPERFDATA:-}",
  "long_plugin_output": "${NOTIFY_LONGSERVICEOUTPUT:-}",
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
)

# Send to analyzer webhook
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X POST "$WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $WEBHOOK_SECRET" \
  -d "$PAYLOAD" \
  --connect-timeout 5 \
  --max-time 10)

if [ "$HTTP_CODE" -ge 200 ] && [ "$HTTP_CODE" -lt 300 ]; then
  echo "OK: Notification sent (HTTP $HTTP_CODE)"
  exit 0
elif [ "$HTTP_CODE" -eq 503 ]; then
  echo "WARN: Queue full (HTTP 503), will retry"
  exit 1
else
  echo "ERROR: Webhook returned HTTP $HTTP_CODE"
  exit 2
fi
