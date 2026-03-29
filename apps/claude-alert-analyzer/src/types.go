package main

import "time"

// --- Alertmanager webhook payload ---

type AlertmanagerWebhook struct {
  Version           string            `json:"version"`
  GroupKey          string            `json:"groupKey"`
  Status            string            `json:"status"`
  Receiver          string            `json:"receiver"`
  GroupLabels       map[string]string `json:"groupLabels"`
  CommonLabels      map[string]string `json:"commonLabels"`
  CommonAnnotations map[string]string `json:"commonAnnotations"`
  ExternalURL       string            `json:"externalURL"`
  Alerts            []Alert           `json:"alerts"`
}

type Alert struct {
  Status       string            `json:"status"`
  Labels       map[string]string `json:"labels"`
  Annotations  map[string]string `json:"annotations"`
  StartsAt     time.Time         `json:"startsAt"`
  EndsAt       time.Time         `json:"endsAt"`
  GeneratorURL string            `json:"generatorURL"`
  Fingerprint  string            `json:"fingerprint"`
}

// --- Claude Messages API ---

type ClaudeRequest struct {
  Model     string          `json:"model"`
  MaxTokens int             `json:"max_tokens"`
  System    string          `json:"system"`
  Messages  []ClaudeMessage `json:"messages"`
}

type ClaudeMessage struct {
  Role    string `json:"role"`
  Content string `json:"content"`
}

type ClaudeResponse struct {
  Content []struct {
    Type string `json:"type"`
    Text string `json:"text,omitempty"`
  } `json:"content"`
  Usage struct {
    InputTokens  int `json:"input_tokens"`
    OutputTokens int `json:"output_tokens"`
  } `json:"usage"`
  Error *struct {
    Type    string `json:"type"`
    Message string `json:"message"`
  } `json:"error,omitempty"`
}

// --- Prometheus API ---

type PromQueryResponse struct {
  Status string `json:"status"`
  Data   struct {
    ResultType string       `json:"resultType"`
    Result     []PromResult `json:"result"`
  } `json:"data"`
}

type PromResult struct {
  Metric map[string]string `json:"metric"`
  Value  [2]interface{}    `json:"value"`
}

// --- Analysis context ---

type AnalysisContext struct {
  PrometheusMetrics string
  KubeEvents        string
  PodStatus         string
  PodLogs           string
}

// --- Config ---

type Config struct {
  NtfyPublishURL      string
  NtfyPublishTopic    string
  NtfyPublishToken    string
  PrometheusURL       string
  ClaudeModel string
  CooldownSeconds     int
  SkipResolved        bool
  Port                string
  WebhookSecret       string
  AllowedNamespaces   []string // Namespace allowlist for log collection
  MaxLogBytes         int      // Per-pod log truncation limit
  APIBaseURL          string   // Claude API endpoint (supports Anthropic and OpenRouter)
  APIKey              string   // API key for authentication
}
