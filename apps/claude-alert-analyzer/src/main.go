package main

import (
  "context"
  "encoding/json"
  "fmt"
  "io"
  "log/slog"
  "net/http"
  "os"
  "os/signal"
  "strconv"
  "strings"
  "sync"
  "syscall"
  "time"

  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/rest"
)

const systemPrompt = `You are a Kubernetes SRE analyst for a k3s home cluster with Prometheus, Grafana, Longhorn storage, Traefik ingress, and Cilium CNI.

Analyze the provided alert with its cluster context and produce a concise root-cause analysis:
1. Identify the most likely root cause
2. Assess severity and blast radius
3. Suggest concrete remediation steps (kubectl commands, config changes)
4. Note correlations with other active alerts

Keep response under 500 words. Use plain text only — no markdown tables, no markdown headers, no bold/italic syntax. Use simple bullet points (- or *) and blank lines for structure. Reference actual metric values and pod names.`

// Cooldown with TTL eviction
type cooldownEntry struct {
  setAt time.Time
}

var (
  cooldowns   = make(map[string]cooldownEntry)
  cooldownsMu sync.Mutex
)

func checkAndSetCooldown(fingerprint string, ttl time.Duration) bool {
  cooldownsMu.Lock()
  defer cooldownsMu.Unlock()

  // Evict expired entries (cap at 100 to bound work)
  now := time.Now()
  evicted := 0
  for k, v := range cooldowns {
    if now.Sub(v.setAt) > ttl && evicted < 100 {
      delete(cooldowns, k)
      evicted++
    }
  }

  if entry, ok := cooldowns[fingerprint]; ok {
    if now.Sub(entry.setAt) < ttl {
      return false // in cooldown
    }
  }

  // Set cooldown BEFORE processing (atomic check-and-set)
  cooldowns[fingerprint] = cooldownEntry{setAt: now}
  return true // proceed
}

func clearCooldown(fingerprint string) {
  cooldownsMu.Lock()
  delete(cooldowns, fingerprint)
  cooldownsMu.Unlock()
}

func envOrDefault(key, fallback string) string {
  if v := os.Getenv(key); v != "" {
    return v
  }
  return fallback
}

func loadConfig() Config {
  cooldown, _ := strconv.Atoi(envOrDefault("COOLDOWN_SECONDS", "300"))
  maxLogBytes, _ := strconv.Atoi(envOrDefault("MAX_LOG_BYTES", "2048"))

  allowedNS := envOrDefault("ALLOWED_NAMESPACES", "monitoring,databases,media")
  var nsList []string
  for _, ns := range strings.Split(allowedNS, ",") {
    ns = strings.TrimSpace(ns)
    if ns != "" {
      nsList = append(nsList, ns)
    }
  }

  webhookSecret := os.Getenv("WEBHOOK_SECRET")
  if webhookSecret == "" {
    slog.Error("WEBHOOK_SECRET is required (fail-closed)")
    os.Exit(1)
  }

  apiKey := os.Getenv("API_KEY")
  if apiKey == "" {
    slog.Error("API_KEY is required (fail-closed)")
    os.Exit(1)
  }

  return Config{
    NtfyPublishURL:      envOrDefault("NTFY_PUBLISH_URL", "https://ntfy.geekbundle.org"),
    NtfyPublishTopic:    envOrDefault("NTFY_PUBLISH_TOPIC", "kubernetes-analysis"),
    NtfyPublishToken:    os.Getenv("NTFY_PUBLISH_TOKEN"),
    PrometheusURL:       envOrDefault("PROMETHEUS_URL", "http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090"),
    ClaudeModel: envOrDefault("CLAUDE_MODEL", "claude-sonnet-4-6"),
    CooldownSeconds:     cooldown,
    SkipResolved:        envOrDefault("SKIP_RESOLVED", "true") != "false",
    Port:                envOrDefault("PORT", "8080"),
    WebhookSecret:       webhookSecret,
    AllowedNamespaces:   nsList,
    MaxLogBytes:         maxLogBytes,
    APIBaseURL:          envOrDefault("API_BASE_URL", "https://api.anthropic.com/v1/messages"),
    APIKey:              apiKey,
  }
}

func processAlert(ctx context.Context, cfg Config, clientset kubernetes.Interface, alert Alert) {
  alertname := alert.Labels["alertname"]
  severity := alert.Labels["severity"]
  namespace := alert.Labels["namespace"]

  slog.Info("processing alert",
    "alertname", alertname, "severity", severity,
    "namespace", namespace, "status", alert.Status)

  actx := gatherContext(ctx, clientset, cfg.PrometheusURL, alert, cfg)

  userPrompt := fmt.Sprintf(`## Alert: %s
- Status: %s
- Severity: %s
- Namespace: %s
- Started: %s
- Labels: %v
- Annotations: %v

## Prometheus Metrics
%s

## Kubernetes Events
%s

## Pod Status
%s

## Pod Logs
%s`,
    alertname, alert.Status, severity, namespace,
    alert.StartsAt.Format(time.RFC3339),
    alert.Labels, alert.Annotations,
    actx.PrometheusMetrics, actx.KubeEvents, actx.PodStatus, actx.PodLogs)

  analysis, err := analyzeWithClaude(ctx, cfg, systemPrompt, userPrompt, cfg.ClaudeModel)
  if err != nil {
    slog.Error("analysis failed", "alertname", alertname, "error", err)
    _ = publishAnalysis(ctx, cfg, alert,
      fmt.Sprintf("**Analysis failed** for %s: %v\n\nManual investigation needed.", alertname, err))
    clearCooldown(alert.Fingerprint) // Allow retry
    return
  }

  if err := publishAnalysis(ctx, cfg, alert, analysis); err != nil {
    slog.Error("publish failed", "alertname", alertname, "error", err)
    clearCooldown(alert.Fingerprint) // Allow retry
    return
  }

  slog.Info("analysis complete", "alertname", alertname, "model", cfg.ClaudeModel)
}

func main() {
  cfg := loadConfig()

  // K8s client -- singleton
  k8sConfig, err := rest.InClusterConfig()
  if err != nil {
    slog.Error("k8s config failed", "error", err)
    os.Exit(1)
  }
  clientset, err := kubernetes.NewForConfig(k8sConfig)
  if err != nil {
    slog.Error("k8s client failed", "error", err)
    os.Exit(1)
  }

  // Cancellable context for workers -- cancelled on SIGTERM
  workerCtx, workerCancel := context.WithCancel(context.Background())

  // Bounded work queue (max 5 concurrent analyses)
  type workItem struct {
    alert Alert
  }
  workQueue := make(chan workItem, 20)
  var wg sync.WaitGroup
  for range 5 {
    wg.Add(1)
    go func() {
      defer wg.Done()
      for item := range workQueue {
        processAlert(workerCtx, cfg, clientset, item.alert)
      }
    }()
  }

  cooldownTTL := time.Duration(cfg.CooldownSeconds) * time.Second

  mux := http.NewServeMux()

  mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    fmt.Fprint(w, "ok")
  })

  mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
    // Webhook shared secret validation (fail-closed: secret is required at startup)
    if r.Header.Get("Authorization") != "Bearer "+cfg.WebhookSecret {
      http.Error(w, "unauthorized", http.StatusUnauthorized)
      return
    }

    body, err := io.ReadAll(r.Body)
    if err != nil {
      http.Error(w, "bad request", http.StatusBadRequest)
      return
    }

    var payload AlertmanagerWebhook
    if err := json.Unmarshal(body, &payload); err != nil {
      http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
      return
    }

    // Enqueue work items
    queued := 0
    dropped := 0
    for _, alert := range payload.Alerts {
      alert := alert // capture

      if cfg.SkipResolved && alert.Status == "resolved" {
        slog.Info("skipping resolved", "alertname", alert.Labels["alertname"])
        continue
      }

      if !checkAndSetCooldown(alert.Fingerprint, cooldownTTL) {
        slog.Info("in cooldown", "alertname", alert.Labels["alertname"])
        continue
      }

      select {
      case workQueue <- workItem{alert: alert}:
        queued++
      default:
        slog.Warn("work queue full, rejecting", "alertname", alert.Labels["alertname"])
        clearCooldown(alert.Fingerprint)
        dropped++
      }
    }

    if dropped > 0 {
      // 503 triggers Alertmanager retry
      w.WriteHeader(http.StatusServiceUnavailable)
      fmt.Fprintf(w, "queued %d, dropped %d (queue full)", queued, dropped)
      return
    }
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "queued %d alerts", queued)
  })

  server := &http.Server{
    Addr:         ":" + cfg.Port,
    Handler:      mux,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  60 * time.Second,
  }

  // Graceful shutdown
  ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
  defer stop()

  go func() {
    slog.Info("Claude Alert Analyzer started",
      "port", cfg.Port,
      "model", cfg.ClaudeModel,
      "apiBaseURL", cfg.APIBaseURL,
      "allowedNamespaces", cfg.AllowedNamespaces)
    if err := server.ListenAndServe(); err != http.ErrServerClosed {
      slog.Error("server failed", "error", err)
      os.Exit(1)
    }
  }()

  <-ctx.Done()
  slog.Info("shutting down...")

  // 1. Stop accepting new requests
  shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
  defer cancel()
  server.Shutdown(shutdownCtx)

  // 2. Drain work queue -- let already-accepted alerts finish
  close(workQueue)

  // 3. Wait for workers with timeout, then force-cancel
  done := make(chan struct{})
  go func() { wg.Wait(); close(done) }()
  select {
  case <-done:
    slog.Info("all workers finished")
  case <-time.After(25 * time.Second):
    slog.Warn("worker drain timeout, cancelling in-flight work")
    workerCancel()
    wg.Wait()
  }
  slog.Info("shutdown complete")
}
