package main

import "regexp"

var sensitivePatterns = []*regexp.Regexp{
  regexp.MustCompile(`(?i)(password|passwd|secret|token|key|authorization|bearer)\s*[=:]\s*\S+`),
  regexp.MustCompile(`(?i)(sk-ant-|sk-|ghp_|gho_|github_pat_|xox[bpas]-)\S+`),
  regexp.MustCompile(`(?i)-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?-----END[A-Z ]*PRIVATE KEY-----`),
  regexp.MustCompile(`(?i)(basic|bearer)\s+[A-Za-z0-9+/=]{20,}`),
  regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
}

func redactSecrets(input string) string {
  result := input
  for _, re := range sensitivePatterns {
    result = re.ReplaceAllString(result, "[REDACTED]")
  }
  return result
}

func truncate(s string, maxBytes int) string {
  if len(s) <= maxBytes {
    return s
  }
  return s[:maxBytes] + "\n... [truncated]"
}
