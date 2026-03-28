package main

import (
  "encoding/json"
  "fmt"
  "io"
  "log/slog"
  "net/http"
  "net/url"
  "os"
  "strings"
  "sync"
  "time"

  "golang.org/x/sync/singleflight"
)

const (
  claudeOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
  claudeOAuthTokenURL = "https://platform.claude.com/v1/oauth/token"
  refreshInterval     = 30 * time.Minute
  expiryBuffer        = 1 * time.Hour
  credentialsMount    = "/etc/claude-credentials/credentials.json"
  credentialsLive     = "/tmp/claude-credentials.json"
)

type TokenManager struct {
  mu          sync.RWMutex
  credentials ClaudeCredentials
  livePath    string
  sfGroup     singleflight.Group
  httpClient  *http.Client
}

func NewTokenManager() (*TokenManager, error) {
  data, err := os.ReadFile(credentialsMount)
  if err != nil {
    return nil, fmt.Errorf("read mounted credentials: %w", err)
  }
  if err := os.WriteFile(credentialsLive, data, 0600); err != nil {
    return nil, fmt.Errorf("write live credentials: %w", err)
  }

  var creds ClaudeCredentials
  if err := json.Unmarshal(data, &creds); err != nil {
    return nil, fmt.Errorf("parse credentials: %w", err)
  }
  if creds.ClaudeAiOauth == nil || creds.ClaudeAiOauth.AccessToken == "" {
    return nil, fmt.Errorf("no OAuth access token in credentials")
  }

  slog.Info("credentials initialized from mounted secret")
  return &TokenManager{
    credentials: creds,
    livePath:    credentialsLive,
    httpClient:  &http.Client{Timeout: 15 * time.Second},
  }, nil
}

// GetAccessToken returns a valid token. Never returns an expired token as success.
func (tm *TokenManager) GetAccessToken() (string, error) {
  tm.mu.RLock()
  token := tm.credentials.ClaudeAiOauth.AccessToken
  expiresAt := tm.credentials.ClaudeAiOauth.ExpiresAt
  tm.mu.RUnlock()

  now := time.Now().UnixMilli()

  if now >= expiresAt {
    // Expired — must refresh synchronously via singleflight
    slog.Info("OAuth token expired, refreshing synchronously")
    newToken, err := tm.refreshSingle()
    if err != nil {
      return "", fmt.Errorf("token expired and refresh failed: %w", err)
    }
    return newToken, nil
  }

  if now+expiryBuffer.Milliseconds() >= expiresAt {
    // Expiring soon — background refresh via singleflight (non-blocking)
    slog.Info("OAuth token expiring soon, refreshing in background")
    go func() {
      if _, err := tm.refreshSingle(); err != nil {
        slog.Warn("background refresh failed", "error", err)
      }
    }()
  }

  return token, nil
}

// refreshSingle deduplicates concurrent refresh calls via singleflight.
func (tm *TokenManager) refreshSingle() (string, error) {
  result, err, _ := tm.sfGroup.Do("refresh", func() (interface{}, error) {
    return tm.doRefresh()
  })
  if err != nil {
    return "", err
  }
  return result.(string), nil
}

func (tm *TokenManager) doRefresh() (string, error) {
  tm.mu.Lock()
  defer tm.mu.Unlock()

  oauth := tm.credentials.ClaudeAiOauth
  if oauth == nil || oauth.RefreshToken == "" {
    return "", fmt.Errorf("no refresh token available")
  }

  scopes := "user:inference user:profile"
  if len(oauth.Scopes) > 0 {
    scopes = strings.Join(oauth.Scopes, " ")
  }

  form := url.Values{
    "grant_type":    {"refresh_token"},
    "client_id":     {claudeOAuthClientID},
    "refresh_token": {oauth.RefreshToken},
    "scope":         {scopes},
  }

  resp, err := tm.httpClient.PostForm(claudeOAuthTokenURL, form)
  if err != nil {
    return "", fmt.Errorf("refresh request: %w", err)
  }
  defer resp.Body.Close()

  body, err := io.ReadAll(resp.Body)
  if err != nil {
    return "", fmt.Errorf("read response: %w", err)
  }

  var result struct {
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token"`
    ExpiresIn    int64  `json:"expires_in"`
    Error        string `json:"error"`
  }
  if err := json.Unmarshal(body, &result); err != nil {
    return "", fmt.Errorf("parse response: %w", err)
  }
  if result.AccessToken == "" {
    return "", fmt.Errorf("refresh failed: %s", result.Error)
  }

  // Server rotates refresh_token — must persist new one
  oauth.AccessToken = result.AccessToken
  if result.RefreshToken != "" {
    oauth.RefreshToken = result.RefreshToken
  }
  if result.ExpiresIn > 0 {
    oauth.ExpiresAt = time.Now().UnixMilli() + result.ExpiresIn*1000
  }

  data, err := json.MarshalIndent(tm.credentials, "", "  ")
  if err != nil {
    return "", fmt.Errorf("marshal credentials: %w", err)
  }
  if err := os.WriteFile(tm.livePath, data, 0600); err != nil {
    slog.Warn("failed to persist refreshed credentials", "error", err)
  }

  slog.Info("OAuth token refreshed",
    "expiresAt", time.UnixMilli(oauth.ExpiresAt).Format(time.RFC3339))
  return result.AccessToken, nil
}

// StartRefreshLoop proactively refreshes every 30 minutes.
func (tm *TokenManager) StartRefreshLoop() {
  slog.Info("token refresh loop started", "interval", refreshInterval)
  if _, err := tm.GetAccessToken(); err != nil {
    slog.Warn("startup token refresh failed", "error", err)
  }
  go func() {
    ticker := time.NewTicker(refreshInterval)
    defer ticker.Stop()
    for range ticker.C {
      if _, err := tm.GetAccessToken(); err != nil {
        slog.Warn("proactive token refresh failed", "error", err)
      }
    }
  }()
}
