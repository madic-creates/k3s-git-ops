package main

import (
  "context"
  "fmt"
  "io"
  "net/http"
  "strings"
  "time"
)

var ntfyHTTPClient = &http.Client{Timeout: 10 * time.Second}

func publishAnalysis(ctx context.Context, cfg Config, alert Alert, analysis string) error {
  alertname := alert.Labels["alertname"]
  severity := alert.Labels["severity"]
  namespace := alert.Labels["namespace"]

  title := fmt.Sprintf("Analysis: %s", alertname)
  if namespace != "" {
    title = fmt.Sprintf("Analysis: %s (%s)", alertname, namespace)
  }

  priorityMap := map[string]string{
    "critical": "5",
    "warning":  "4",
    "info":     "2",
  }
  priority := priorityMap[severity]
  if priority == "" {
    priority = "3"
  }

  ntfyURL := fmt.Sprintf("%s/%s", cfg.NtfyPublishURL, cfg.NtfyPublishTopic)
  req, err := http.NewRequestWithContext(ctx, "POST", ntfyURL, strings.NewReader(analysis))
  if err != nil {
    return fmt.Errorf("create request: %w", err)
  }

  req.Header.Set("Title", title)
  req.Header.Set("Priority", priority)
  req.Header.Set("Tags", "robot,mag")
  req.Header.Set("Markdown", "yes")
  if cfg.NtfyPublishToken != "" {
    req.Header.Set("Authorization", "Bearer "+cfg.NtfyPublishToken)
  }

  resp, err := ntfyHTTPClient.Do(req)
  if err != nil {
    return fmt.Errorf("publish: %w", err)
  }
  defer resp.Body.Close()
  io.Copy(io.Discard, resp.Body)

  if resp.StatusCode >= 300 {
    return fmt.Errorf("ntfy returned %d", resp.StatusCode)
  }
  return nil
}
