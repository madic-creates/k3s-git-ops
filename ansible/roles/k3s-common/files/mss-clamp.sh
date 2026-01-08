#!/bin/bash
# MSS Clamping script for Path MTU Discovery issues
# Prevents SSH timeouts and connection drops to hosts with lower MTU
# This is required when using Cilium with WireGuard encryption and Geneve tunnels

set -e

# Check if rule already exists to ensure idempotency
if ! iptables -t mangle -C POSTROUTING -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null; then
    iptables -t mangle -A POSTROUTING -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
    echo "MSS clamping rule added successfully"
else
    echo "MSS clamping rule already exists"
fi
