package main

import (
  "context"
  "encoding/json"
  "fmt"
  "io"
  "log/slog"
  "net/http"
  "net/url"
  "strings"
  "time"

  corev1 "k8s.io/api/core/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
  "k8s.io/client-go/kubernetes"
)

var promHTTPClient = &http.Client{Timeout: 10 * time.Second}

func isNamespaceAllowed(namespace string, allowed []string) bool {
  if len(allowed) == 0 {
    return false // deny by default if no allowlist
  }
  for _, ns := range allowed {
    if ns == namespace || ns == "*" {
      return true
    }
  }
  return false
}

func promqlQuery(ctx context.Context, prometheusURL, query string) string {
  u := fmt.Sprintf("%s/api/v1/query?query=%s", prometheusURL, url.QueryEscape(query))
  req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
  if err != nil {
    return fmt.Sprintf("(request error: %v)", err)
  }
  resp, err := promHTTPClient.Do(req)
  if err != nil {
    return fmt.Sprintf("(query failed: %v)", err)
  }
  defer resp.Body.Close()
  body, err := io.ReadAll(resp.Body)
  if err != nil {
    return "(failed to read response)"
  }

  var result PromQueryResponse
  if err := json.Unmarshal(body, &result); err != nil {
    return "(failed to parse response)"
  }
  if len(result.Data.Result) == 0 {
    return "(no data)"
  }

  var lines []string
  for _, r := range result.Data.Result {
    var labels []string
    for k, v := range r.Metric {
      labels = append(labels, fmt.Sprintf("%s=%s", k, v))
    }
    val := fmt.Sprintf("%v", r.Value[1])
    lines = append(lines, fmt.Sprintf("%s: %s", strings.Join(labels, ", "), val))
  }
  return strings.Join(lines, "\n")
}

func getPrometheusMetrics(ctx context.Context, prometheusURL string, alert Alert) string {
  namespace := alert.Labels["namespace"]
  alertname := alert.Labels["alertname"]
  var sections []string

  sections = append(sections, "## Active Firing Alerts")
  sections = append(sections, promqlQuery(ctx, prometheusURL, `ALERTS{alertstate="firing"}`))

  if namespace != "" {
    sections = append(sections,
      fmt.Sprintf("\n## CPU Usage (%s)", namespace),
      promqlQuery(ctx, prometheusURL,
        fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="%s"}[5m])) by (pod)`, namespace)),
      fmt.Sprintf("\n## Memory Usage (%s)", namespace),
      promqlQuery(ctx, prometheusURL,
        fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace="%s"}) by (pod)`, namespace)),
      fmt.Sprintf("\n## Pod Restarts (%s)", namespace),
      promqlQuery(ctx, prometheusURL,
        fmt.Sprintf(`sum(kube_pod_container_status_restarts_total{namespace="%s"}) by (pod)`, namespace)),
    )
  }

  lower := strings.ToLower(alertname)
  switch {
  case strings.Contains(lower, "crashloop"):
    sections = append(sections, "\n## CrashLoop Details",
      promqlQuery(ctx, prometheusURL, `kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff"}`))
  case strings.Contains(lower, "memory") || strings.Contains(lower, "oom"):
    sections = append(sections, "\n## Top Memory Consumers",
      promqlQuery(ctx, prometheusURL, `topk(5, sum(container_memory_working_set_bytes) by (namespace, pod))`))
  case strings.Contains(lower, "cpu"):
    sections = append(sections, "\n## Top CPU Consumers",
      promqlQuery(ctx, prometheusURL, `topk(5, sum(rate(container_cpu_usage_seconds_total[5m])) by (namespace, pod))`))
  case strings.Contains(lower, "disk") || strings.Contains(lower, "volume") || strings.Contains(lower, "storage"):
    sections = append(sections, "\n## PVC Usage",
      promqlQuery(ctx, prometheusURL, `(kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes) > 0.8`))
  case strings.Contains(lower, "node"):
    sections = append(sections, "\n## Node Conditions",
      promqlQuery(ctx, prometheusURL, `kube_node_status_condition{condition="Ready"}`))
  }

  return strings.Join(sections, "\n")
}

func getKubeContext(ctx context.Context, clientset kubernetes.Interface, alert Alert, cfg Config) (events, pods, logs string) {
  namespace := alert.Labels["namespace"]
  if namespace == "" {
    return "(no namespace in alert)", "(no namespace)", "(no namespace)"
  }

  // Events — always allowed (no secrets in events)
  eventList, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
    FieldSelector: "type!=Normal",
  })
  if err != nil {
    events = fmt.Sprintf("(failed: %v)", err)
  } else {
    var lines []string
    items := eventList.Items
    start := 0
    if len(items) > 20 {
      start = len(items) - 20
    }
    for _, e := range items[start:] {
      ts := ""
      if !e.LastTimestamp.Time.IsZero() {
        ts = e.LastTimestamp.Format(time.RFC3339)
      }
      lines = append(lines, fmt.Sprintf("%s %s %s %s: %s",
        ts, e.Type, e.Reason, e.InvolvedObject.Name, e.Message))
    }
    if len(lines) == 0 {
      events = "(no warning events)"
    } else {
      events = strings.Join(lines, "\n")
    }
  }

  // Pod status — always allowed
  podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
  if err != nil {
    pods = fmt.Sprintf("(failed: %v)", err)
  } else {
    var lines []string
    for _, p := range podList.Items {
      phase := string(p.Status.Phase)
      restarts := 0
      ready := 0
      total := len(p.Status.ContainerStatuses)
      for _, cs := range p.Status.ContainerStatuses {
        restarts += int(cs.RestartCount)
        if cs.Ready {
          ready++
        }
      }
      lines = append(lines, fmt.Sprintf("%s %s %d/%d restarts=%d",
        p.Name, phase, ready, total, restarts))
    }
    pods = strings.Join(lines, "\n")
  }

  // Logs — only for allowlisted namespaces, redacted, truncated
  if !isNamespaceAllowed(namespace, cfg.AllowedNamespaces) {
    logs = fmt.Sprintf("(namespace %q not in log allowlist)", namespace)
    return events, pods, logs
  }

  logs = "(no failing pods)"
  podList2, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
    FieldSelector: "status.phase!=Running,status.phase!=Succeeded",
  })
  if err == nil && len(podList2.Items) > 0 {
    var logLines []string
    limit := 3
    if len(podList2.Items) < limit {
      limit = len(podList2.Items)
    }
    for _, p := range podList2.Items[:limit] {
      tailLines := int64(30)
      logResp := clientset.CoreV1().Pods(namespace).GetLogs(p.Name, &corev1.PodLogOptions{
        TailLines: &tailLines,
      })
      raw, err := logResp.DoRaw(ctx)
      if err != nil {
        logLines = append(logLines, fmt.Sprintf("--- %s --- (no logs)", p.Name))
      } else {
        redacted := redactSecrets(string(raw))
        truncated := truncate(redacted, cfg.MaxLogBytes)
        logLines = append(logLines, fmt.Sprintf("--- %s ---\n%s", p.Name, truncated))
      }
    }
    logs = strings.Join(logLines, "\n\n")
  }

  return events, pods, logs
}

func gatherContext(ctx context.Context, clientset kubernetes.Interface, prometheusURL string, alert Alert, cfg Config) AnalysisContext {
  slog.Info("gathering context",
    "alertname", alert.Labels["alertname"],
    "namespace", alert.Labels["namespace"])

  promCh := make(chan string, 1)
  go func() {
    promCh <- getPrometheusMetrics(ctx, prometheusURL, alert)
  }()

  events, podStatus, podLogs := getKubeContext(ctx, clientset, alert, cfg)

  return AnalysisContext{
    PrometheusMetrics: <-promCh,
    KubeEvents:        events,
    PodStatus:         podStatus,
    PodLogs:           podLogs,
  }
}
