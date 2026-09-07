#!/usr/bin/env python3
"""Expose Open-Meteo solar irradiance as Prometheus metrics.

This exporter provides the *reference* against which actual PV output is
judged. It emits measurements and site configuration only -- never derived
quantities. Expected power and performance ratio are computed by recording
rules in apps/monitoring/k8s.prometheusrule.energy.yaml, so that recalibrating
the model is a commit rather than a pod restart.

Standard library only, deliberately: the script is mounted from a ConfigMap
into a stock python image, so a `pip install` at container start would make the
pod depend on a package index being reachable at boot.

PRIVACY: the site coordinates identify the operator's home address. They arrive
via environment variables from a SOPS-encrypted Secret and must never be
written to a log line, an error message, or a Prometheus label. Open-Meteo
echoes them back in every response, so the temptation to pass them through is
real -- a label would be readable by anyone with dashboard access and would be
copied into every backup of the TSDB.
"""

import json
import os
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

API_URL = "https://api.open-meteo.com/v1/forecast"

# Panel geometry is not location-identifying and stays in plain text.
# Azimuth follows the Open-Meteo convention: 0 = south, negative = east,
# positive = west. The operator's compass bearings (0 = north) convert as
# bearing - 180, so 45 deg NE -> -135 and 225 deg SW -> +45.
#
# The two strings face nearly opposite directions, which is why each needs its
# own request: the API accepts only one tilt/azimuth pair per call. Passing
# comma-separated values returns HTTP 400.
STRINGS = (
    {"name": "pv1", "tilt": 40.0, "azimuth": -135.0, "peak_watts": 560.0},
    {"name": "pv2", "tilt": 40.0, "azimuth": 45.0, "peak_watts": 560.0},
)

# The API's native granularity is 900 s, so polling faster gains nothing.
POLL_INTERVAL_SECONDS = int(os.environ.get("OPENMETEO_POLL_INTERVAL", "900"))

# After a FAILED poll, retry much sooner than the normal cadence. Sleeping the
# full interval on failure means a single transient error costs 15 minutes of
# stale data, and at startup it leaves the pod unready for that long -- which
# is exactly what happened on first deployment.
#
# Back off exponentially rather than retrying at a fixed short interval: a
# prolonged outage should not hammer a free public API. The backoff is capped
# at POLL_INTERVAL_SECONDS too, so retrying can never end up slower than
# ordinary polling however the values are configured.
POLL_RETRY_MIN_SECONDS = int(os.environ.get("OPENMETEO_RETRY_MIN", "30"))
POLL_RETRY_MAX_SECONDS = int(os.environ.get("OPENMETEO_RETRY_MAX", "300"))
HTTP_TIMEOUT_SECONDS = int(os.environ.get("OPENMETEO_HTTP_TIMEOUT", "15"))
LISTEN_PORT = int(os.environ.get("OPENMETEO_LISTEN_PORT", "9779"))


def log(message):
    """Log without ever interpolating a coordinate."""
    print(message, file=sys.stderr, flush=True)


class State:
    """Last known good reading, shared between the poller and the HTTP server.

    Values survive a failed poll on purpose: a transient API error should not
    take the reference offline. `last_success` stops advancing instead, which
    is what the SolarIrradianceStale alert watches.
    """

    def __init__(self):
        self._lock = threading.Lock()
        self.tilted = {}
        self.global_horizontal = None
        self.cloud_cover = None
        self.last_success = 0.0
        self.failures = 0

    def record_success(self, tilted, global_horizontal, cloud_cover):
        with self._lock:
            self.tilted = tilted
            self.global_horizontal = global_horizontal
            self.cloud_cover = cloud_cover
            self.last_success = time.time()

    def record_failure(self):
        with self._lock:
            self.failures += 1

    def snapshot(self):
        with self._lock:
            return (
                dict(self.tilted),
                self.global_horizontal,
                self.cloud_cover,
                self.last_success,
                self.failures,
            )


STATE = State()


def fetch(latitude, longitude, string, want_context):
    """Fetch current values for one string orientation.

    `want_context` adds the orientation-independent fields, which are only
    requested once per poll rather than once per string.
    """
    fields = ["global_tilted_irradiance"]
    if want_context:
        fields += ["shortwave_radiation", "cloud_cover"]
    query = urllib.parse.urlencode(
        {
            "latitude": latitude,
            "longitude": longitude,
            "current": ",".join(fields),
            "tilt": string["tilt"],
            "azimuth": string["azimuth"],
        }
    )
    request = urllib.request.Request(
        API_URL + "?" + query,
        headers={"User-Agent": "k3s-git-ops-openmeteo-exporter/1.0"},
    )
    with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT_SECONDS) as response:
        payload = json.load(response)
    if payload.get("error"):
        # The API puts its reason in the body; it never contains coordinates.
        raise RuntimeError("api error: %s" % payload.get("reason", "unknown"))
    return payload.get("current", {})


def poll_once(latitude, longitude):
    tilted = {}
    global_horizontal = None
    cloud_cover = None
    for index, string in enumerate(STRINGS):
        current = fetch(latitude, longitude, string, want_context=(index == 0))
        value = current.get("global_tilted_irradiance")
        if value is None:
            raise RuntimeError("no global_tilted_irradiance for %s" % string["name"])
        tilted[string["name"]] = float(value)
        if index == 0:
            if current.get("shortwave_radiation") is not None:
                global_horizontal = float(current["shortwave_radiation"])
            if current.get("cloud_cover") is not None:
                # Reported as a percentage; exported as a 0..1 ratio.
                cloud_cover = float(current["cloud_cover"]) / 100.0
    STATE.record_success(tilted, global_horizontal, cloud_cover)


def poll_loop(latitude, longitude):
    backoff = POLL_RETRY_MIN_SECONDS
    while True:
        try:
            poll_once(latitude, longitude)
            delay = POLL_INTERVAL_SECONDS
            backoff = POLL_RETRY_MIN_SECONDS
        except (urllib.error.URLError, RuntimeError, ValueError, KeyError) as error:
            # Never log the query string: it carries the coordinates.
            STATE.record_failure()
            delay = backoff
            backoff = min(backoff * 2, POLL_RETRY_MAX_SECONDS, POLL_INTERVAL_SECONDS)
            log(
                "poll failed, retrying in %ds: %s: %s"
                % (delay, type(error).__name__, error)
            )
        time.sleep(delay)


def render():
    tilted, global_horizontal, cloud_cover, last_success, failures = STATE.snapshot()
    lines = []

    lines.append("# HELP solar_irradiance_tilted_wm2 Global tilted irradiance at the orientation of one PV string.")
    lines.append("# TYPE solar_irradiance_tilted_wm2 gauge")
    for string in STRINGS:
        if string["name"] in tilted:
            lines.append(
                'solar_irradiance_tilted_wm2{string="%s"} %g'
                % (string["name"], tilted[string["name"]])
            )

    lines.append("# HELP solar_array_peak_power_watts Rated peak power of one PV string.")
    lines.append("# TYPE solar_array_peak_power_watts gauge")
    for string in STRINGS:
        lines.append(
            'solar_array_peak_power_watts{string="%s"} %g'
            % (string["name"], string["peak_watts"])
        )

    if global_horizontal is not None:
        lines.append("# HELP solar_irradiance_global_horizontal_wm2 Global horizontal irradiance, independent of panel orientation.")
        lines.append("# TYPE solar_irradiance_global_horizontal_wm2 gauge")
        lines.append("solar_irradiance_global_horizontal_wm2 %g" % global_horizontal)

    if cloud_cover is not None:
        lines.append("# HELP solar_cloud_cover_ratio Total cloud cover, 0 to 1.")
        lines.append("# TYPE solar_cloud_cover_ratio gauge")
        lines.append("solar_cloud_cover_ratio %g" % cloud_cover)

    lines.append("# HELP solar_openmeteo_last_success_timestamp_seconds Unix time of the last successful Open-Meteo poll.")
    lines.append("# TYPE solar_openmeteo_last_success_timestamp_seconds gauge")
    # Deliberately not %g: six significant digits would round a Unix timestamp
    # to the nearest ~1000 s and make the staleness alert meaningless.
    lines.append("solar_openmeteo_last_success_timestamp_seconds %.3f" % last_success)

    lines.append("# HELP solar_openmeteo_request_failures_total Failed Open-Meteo polls since start.")
    lines.append("# TYPE solar_openmeteo_request_failures_total counter")
    lines.append("solar_openmeteo_request_failures_total %d" % failures)

    return "\n".join(lines) + "\n"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _respond(self, status, body, content_type="text/plain; charset=utf-8"):
        encoded = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self):
        if self.path.startswith("/metrics"):
            # Serving is a pure in-memory read and never triggers an API call,
            # so the scrape interval is unrelated to the poll interval.
            self._respond(200, render())
        elif self.path.startswith("/-/ready"):
            _, _, _, last_success, _ = STATE.snapshot()
            if last_success > 0:
                self._respond(200, "ready\n")
            else:
                self._respond(503, "no successful poll yet\n")
        else:
            self._respond(404, "not found\n")

    def log_message(self, fmt, *args):
        """Silence per-request logging; scrapes would otherwise dominate."""


def main():
    latitude = os.environ.get("OPENMETEO_LATITUDE", "").strip()
    longitude = os.environ.get("OPENMETEO_LONGITUDE", "").strip()
    if not latitude or not longitude:
        log("OPENMETEO_LATITUDE and OPENMETEO_LONGITUDE must be set")
        return 1

    threading.Thread(
        target=poll_loop, args=(latitude, longitude), daemon=True
    ).start()

    server = ThreadingHTTPServer(("0.0.0.0", LISTEN_PORT), Handler)
    log("listening on :%d" % LISTEN_PORT)
    server.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
