package main

import (
  "bytes"
  "context"
  "encoding/json"
  "fmt"
  "io"
  "log/slog"
  "net/http"
  "strings"
  "time"
)

const (
  claudeAPIURL     = "https://api.claude.ai/v1/messages"
  anthropicVersion = "2023-06-01"
)

var claudeHTTPClient = &http.Client{Timeout: 120 * time.Second}

func analyzeWithClaude(ctx context.Context, tm *TokenManager, systemPrompt, userPrompt, model string) (string, error) {
  token, err := tm.GetAccessToken()
  if err != nil {
    return "", fmt.Errorf("get token: %w", err)
  }

  reqBody := ClaudeRequest{
    Model:     model,
    MaxTokens: 2048,
    System:    systemPrompt,
    Messages:  []ClaudeMessage{{Role: "user", Content: userPrompt}},
  }

  bodyBytes, err := json.Marshal(reqBody)
  if err != nil {
    return "", fmt.Errorf("marshal request: %w", err)
  }

  req, err := http.NewRequestWithContext(ctx, "POST", claudeAPIURL, bytes.NewReader(bodyBytes))
  if err != nil {
    return "", fmt.Errorf("create request: %w", err)
  }
  req.Header.Set("Content-Type", "application/json")
  req.Header.Set("Authorization", "Bearer "+token)
  req.Header.Set("anthropic-version", anthropicVersion)

  resp, err := claudeHTTPClient.Do(req)
  if err != nil {
    return "", fmt.Errorf("API request: %w", err)
  }
  defer resp.Body.Close()

  respBody, err := io.ReadAll(resp.Body)
  if err != nil {
    return "", fmt.Errorf("read response: %w", err)
  }

  if resp.StatusCode != http.StatusOK {
    return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
  }

  var result ClaudeResponse
  if err := json.Unmarshal(respBody, &result); err != nil {
    return "", fmt.Errorf("parse response: %w", err)
  }

  if result.Error != nil {
    return "", fmt.Errorf("API error: %s: %s", result.Error.Type, result.Error.Message)
  }

  var parts []string
  for _, c := range result.Content {
    if c.Type == "text" && c.Text != "" {
      parts = append(parts, c.Text)
    }
  }

  slog.Info("Claude analysis complete",
    "model", model,
    "inputTokens", result.Usage.InputTokens,
    "outputTokens", result.Usage.OutputTokens)

  return strings.Join(parts, "\n"), nil
}
