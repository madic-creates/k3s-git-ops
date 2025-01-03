#!/bin/bash

USAGE() {
        echo "Usage: $0 <K8S_CERT_SECRET> <DEST_SERVER>

This script gets a certificate from a kubernetes secret and copies it to a server.
Great for e.g. updating the certificate for a reverse proxy.

Example: $0 wildcard-cloudflare-production-01 server.example.com" 1>&2
        exit 1
}

[[ $# -ne 2 ]] && USAGE

CERT=$1
DEST_SERVER=$2

## Exit if command fails on non-zero status code and exit on empty variable
set -eu
## Change script return code to the last command to exit with a non-zero status
set -o pipefail

## Get certificate
kubectl get secret "${CERT}" -n certmanager -o jsonpath="{.data.tls\.crt}" | base64 -d > "${CERT}".crt
## Get private key
kubectl get secret "${CERT}" -n certmanager -o jsonpath="{.data.tls\.key}" | base64 -d > "${CERT}".key

scp -p wildcard-cloudflare-production-01.* "${DEST_SERVER}":
ssh -t "${DEST_SERVER}" "sudo cp ${CERT}.* /etc/nginx/ && sudo systemctl reload nginx"
rm -f "${CERT}".*
