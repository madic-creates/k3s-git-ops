# Claude Alert Analyzer

Claude-powered alert analyzers that receive monitoring webhooks, gather context, send everything to an LLM for root-cause analysis, and publish results to ntfy. Part of the [claude-alert-analyzer](https://github.com/madic-creates/claude-alert-analyzer) shared repository.

For full documentation, architecture, configuration, and security details, see the [README in the analyzer repository](https://github.com/madic-creates/claude-alert-analyzer).

## Kubernetes Analyzer

Receives Alertmanager webhooks and gathers cluster context (Prometheus metrics, K8s events, pod status, logs).

### Architecture

```
Alertmanager
    |
    | POST /webhook + Bearer secret
    | continue: true (ntfy-alertmanager still fires)
    v
claude-alert-analyzer (scratch image, ~13MB)
    |
    +-- Gather context
    |     +-- Prometheus: firing alerts, CPU, memory, restarts, alert-specific queries
    |     +-- K8s API: events (Warning), pod status, pod logs (allowlisted namespaces)
    |
    +-- LLM analysis (Anthropic or OpenRouter)
    |
    +-- Publish to ntfy
    |
    +-- Cooldown (per alert fingerprint)
```

### API Endpoints

- `GET /health` -- liveness/readiness probe
- `POST /webhook` -- Alertmanager webhook receiver (requires `Authorization: Bearer <WEBHOOK_SECRET>`)

### Configuration

All configuration is via environment variables.

#### Required (fail-closed)

| Variable | Description |
|----------|-------------|
| `WEBHOOK_SECRET` | Shared secret for Alertmanager webhook authentication |
| `API_KEY` | API key for the LLM provider (Anthropic or OpenRouter) |

#### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `API_BASE_URL` | `https://api.anthropic.com/v1/messages` | LLM API endpoint. Set to `https://openrouter.ai/api/v1/messages` for OpenRouter |
| `CLAUDE_MODEL` | `claude-sonnet-4-6` | Model name. For OpenRouter use `anthropic/claude-sonnet-4-6` |
| `PORT` | `8080` | HTTP server port |
| `PROMETHEUS_URL` | `http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090` | Prometheus query endpoint |
| `COOLDOWN_SECONDS` | `300` | Per-alert cooldown to avoid duplicate analyses |
| `SKIP_RESOLVED` | `true` | Skip resolved alerts |
| `ALLOWED_NAMESPACES` | `monitoring,databases,media` | Comma-separated namespace allowlist for pod log collection |
| `MAX_LOG_BYTES` | `2048` | Per-pod log truncation limit |
| `NTFY_PUBLISH_URL` | `https://ntfy.geekbundle.org` | ntfy server URL |
| `NTFY_PUBLISH_TOPIC` | `kubernetes-analysis` | ntfy topic |
| `NTFY_PUBLISH_TOKEN` | (empty) | ntfy authentication token |

#### Provider-specific authentication

The service detects the provider from `API_BASE_URL`:

- URL contains `anthropic.com`: uses `x-api-key` header + `anthropic-version` header
- All other URLs (OpenRouter, etc.): uses `Authorization: Bearer` header

### Alert Processing Flow

1. Alertmanager sends webhook with one or more alerts
2. Webhook secret is validated (401 if invalid)
3. Resolved alerts are skipped (if `SKIP_RESOLVED=true`)
4. Per-alert fingerprint cooldown is checked
5. Alert is queued (bounded queue of 20, 5 concurrent workers)
6. If queue is full, returns 503 (triggers Alertmanager retry)
7. Context is gathered in parallel:
   - Prometheus metrics (firing alerts, namespace CPU/memory/restarts, alert-specific queries)
   - K8s events (Warning type, last 20)
   - Pod status (all pods in namespace)
   - Pod logs (only for allowlisted namespaces, redacted, truncated)
8. LLM analyzes alert + context
9. Result is published to ntfy with priority based on severity
10. On failure (analysis or publish), cooldown is cleared to allow retry

### Security

- Scratch container image (no shell, no package manager)
- Runs as non-root (UID 65534)
- Read-only root filesystem
- All capabilities dropped
- Webhook authentication required (fail-closed)
- Pod logs only collected from allowlisted namespaces
- Secrets redacted from logs before sending to LLM (passwords, tokens, keys, emails, PEM keys)
- Log output truncated to `MAX_LOG_BYTES`

### CI/CD

GitHub Actions workflow (`.github/workflows/build-claude-alert-analyzer.yaml`):

- Triggers on push to `main` when `apps/claude-alert-analyzer/src/**`, `Dockerfile`, or the workflow changes
- Builds multi-stage Docker image (golang:1.26-alpine -> scratch)
- Pushes to `ghcr.io/madic-creates/claude-alert-analyzer:<short-sha>`
- Auto-commits updated image tag to `k8s.deployment.yaml`
- ArgoCD picks up the change and deploys

### Alertmanager Configuration

The claude-analyzer receiver is configured in `apps/monitoring/values.enc.yaml`:

```yaml
route:
  routes:
    - receiver: claude-analyzer
      matchers:
        - severity =~ "warning|critical"
      continue: true  # ntfy-alertmanager still receives the alert

receivers:
  - name: claude-analyzer
    webhook_configs:
      - url: http://claude-alert-analyzer.monitoring.svc.cluster.local:8080/webhook
        send_resolved: false
        http_config:
          authorization:
            type: Bearer
            credentials: <WEBHOOK_SECRET>
```

### Testing

```bash
# Get a shell in the analyzer pod (via debug container)
POD=$(kubectl get pods -n monitoring -l app.kubernetes.io/name=claude-alert-analyzer -o jsonpath='{.items[0].metadata.name}')
kubectl debug -n monitoring $POD --image=curlimages/curl --profile=general -i -- \
  curl -sf -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <WEBHOOK_SECRET>" \
  -d '{
    "version": "4",
    "groupKey": "test",
    "status": "firing",
    "receiver": "claude-analyzer",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "TestAlert",
        "severity": "warning",
        "namespace": "monitoring"
      },
      "annotations": {
        "summary": "Test alert"
      },
      "startsAt": "2026-01-01T00:00:00Z",
      "fingerprint": "test-123"
    }]
  }' \
  http://localhost:8080/webhook
```

```bash
# Check logs
kubectl logs -n monitoring -l app.kubernetes.io/name=claude-alert-analyzer --tail=20
```

## CheckMK Analyzer

Receives CheckMK notification webhooks and analyzes Nagios-based monitoring alerts.

### GitOps Deployment

The analyzer is deployed in the `monitoring` namespace via ArgoCD (`apps/claude-checkmk-analyzer/`).

### Prerequisites

1. **Encrypt secrets** with real values:

    ```bash
    sops --config .sops.yaml apps/claude-checkmk-analyzer/secrets.enc.yaml
    ```

    Required values: `API_KEY`, `WEBHOOK_SECRET`, `CHECKMK_API_USER`, `CHECKMK_API_SECRET`, `NTFY_PUBLISH_TOKEN`

2. **Populate known_hosts** with monitored host keys:

    ```bash
    ssh-keyscan -t ed25519 host1.example.com host2.example.com > apps/claude-checkmk-analyzer/known_hosts
    ```

3. **Configure CheckMK notification rule:**
    - Go to Setup > Notifications > Add rule
    - Notification method: Custom script `claude-analyzer-notify.sh`
    - Parameter 1: Webhook URL (default: `http://claude-checkmk-analyzer.monitoring:8080/webhook`)
    - Parameter 2: Webhook secret (must match `WEBHOOK_SECRET` in the secret)

### Testing

```bash
# Send a test notification
POD=$(kubectl get pods -n monitoring -l app.kubernetes.io/name=claude-checkmk-analyzer -o jsonpath='{.items[0].metadata.name}')
kubectl debug -n monitoring $POD --image=curlimages/curl --profile=general -i -- \
  curl -sf -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <WEBHOOK_SECRET>" \
  -d '{
    "hostname": "testhost",
    "host_address": "192.168.1.1",
    "service_description": "CPU load",
    "service_state": "CRITICAL",
    "service_output": "CRIT - 15min load 12.5 at 4 CPUs",
    "host_state": "UP",
    "notification_type": "PROBLEM",
    "perf_data": "load1=8.2;4;8;0;",
    "long_plugin_output": "",
    "timestamp": "2026-04-01T00:00:00Z"
  }' \
  http://localhost:8080/webhook
```

```bash
# Check logs
kubectl logs -n monitoring -l app.kubernetes.io/name=claude-checkmk-analyzer --tail=20
```
