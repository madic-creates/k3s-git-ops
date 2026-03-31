# Claude Alert Analyzer

A lightweight Go service that receives Alertmanager webhooks, gathers cluster context (Prometheus metrics, K8s events, pod status, logs), sends everything to an LLM for root-cause analysis, and publishes the result to ntfy.

## Architecture

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

## API Endpoints

- `GET /health` -- liveness/readiness probe
- `POST /webhook` -- Alertmanager webhook receiver (requires `Authorization: Bearer <WEBHOOK_SECRET>`)

## Configuration

All configuration is via environment variables.

### Required (fail-closed)

| Variable | Description |
|----------|-------------|
| `WEBHOOK_SECRET` | Shared secret for Alertmanager webhook authentication |
| `API_KEY` | API key for the LLM provider (Anthropic or OpenRouter) |

### Optional

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

### Provider-specific authentication

The service detects the provider from `API_BASE_URL`:

- URL contains `anthropic.com`: uses `x-api-key` header + `anthropic-version` header
- All other URLs (OpenRouter, etc.): uses `Authorization: Bearer` header

## Alert Processing Flow

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

## Security

- Scratch container image (no shell, no package manager)
- Runs as non-root (UID 65534)
- Read-only root filesystem
- All capabilities dropped
- Webhook authentication required (fail-closed)
- Pod logs only collected from allowlisted namespaces
- Secrets redacted from logs before sending to LLM (passwords, tokens, keys, emails, PEM keys)
- Log output truncated to `MAX_LOG_BYTES`

## CI/CD

GitHub Actions workflow (`.github/workflows/build-claude-alert-analyzer.yaml`):

- Triggers on push to `main` when `apps/claude-alert-analyzer/src/**`, `Dockerfile`, or the workflow changes
- Builds multi-stage Docker image (golang:1.26-alpine -> scratch)
- Pushes to `ghcr.io/madic-creates/claude-alert-analyzer:<short-sha>`
- Auto-commits updated image tag to `k8s.deployment.yaml`
- ArgoCD picks up the change and deploys

## Alertmanager Configuration

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

## Testing

Send a test alert directly to the analyzer:

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

Check logs:

```bash
kubectl logs -n monitoring -l app.kubernetes.io/name=claude-alert-analyzer --tail=20
```

## File Structure

```
apps/claude-alert-analyzer/
  src/
    main.go       # HTTP server, config, work queue, graceful shutdown
    claude.go     # LLM API client (Anthropic + OpenRouter)
    context.go    # Prometheus + K8s context gathering
    ntfy.go       # ntfy publisher
    redact.go     # Secret redaction + log truncation
    types.go      # Type definitions
    go.mod / go.sum
  Dockerfile
  kustomization.yaml
  kustomize-secret-generator.yaml
  secrets.enc.yaml
  k8s.deployment.yaml
  k8s.service.yaml
  k8s.rbac.yaml
  k8s.np.claude-alert-analyzer.yaml
```
