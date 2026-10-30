"""Retry wrapper for dztnr, mounted as sitecustomize.py (see k8s.dztnr.yaml).

Python imports sitecustomize from PYTHONPATH at interpreter start, so this
patches requests before dztnr.py runs, without forking or rebuilding the image.

dztnr calls requests.get() without a timeout and exits on the first
ConnectionError. Deezer also answers rate limiting with HTTP 200 and an error
body ({"error": {"code": 4, ...}}), which dztnr treats as "track not found".
Both cases, plus timeouts and HTTP 429/5xx, are retried here with exponential
backoff before dztnr sees the result.
"""

import logging
import time
from urllib.parse import urlsplit

import requests

MAX_ATTEMPTS = 8
BASE_DELAY = 2  # seconds, doubled per attempt
MAX_DELAY = 60  # seconds; worst case ~3 minutes in total per request
TIMEOUT = 30  # seconds, applied when the caller sets none
RETRY_STATUS = {429, 500, 502, 503, 504}
DEEZER_HOST = "api.deezer.com"
DEEZER_QUOTA_CODE = 4

log = logging.getLogger("dztnr-retry")
_original_request = requests.Session.request


def _deezer_quota_exceeded(response):
    if urlsplit(response.url).hostname != DEEZER_HOST:
        return False
    try:
        body = response.json()
    except ValueError:
        return False
    error = body.get("error") if isinstance(body, dict) else None
    return isinstance(error, dict) and error.get("code") == DEEZER_QUOTA_CODE


def _request_with_retry(self, method, url, **kwargs):
    kwargs.setdefault("timeout", TIMEOUT)
    # Only the host is logged: Navidrome URLs carry the password as a query parameter.
    host = urlsplit(url).hostname
    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            response = _original_request(self, method, url, **kwargs)
        except (requests.exceptions.ConnectionError, requests.exceptions.Timeout) as e:
            if attempt == MAX_ATTEMPTS:
                raise
            reason = type(e).__name__
        else:
            if response.status_code in RETRY_STATUS:
                reason = f"HTTP {response.status_code}"
            elif _deezer_quota_exceeded(response):
                reason = "Deezer quota exceeded"
            else:
                return response
            if attempt == MAX_ATTEMPTS:
                return response
        delay = min(BASE_DELAY * 2 ** (attempt - 1), MAX_DELAY)
        log.warning(
            "%s: %s, retry %d/%d in %ds", host, reason, attempt, MAX_ATTEMPTS - 1, delay
        )
        time.sleep(delay)


requests.Session.request = _request_with_retry
