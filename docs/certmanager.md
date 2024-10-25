# Certmanager

Zertifikate und Key extrahieren:

```shell
kubectl get secret -n certmanager wildcard-cloudflare-production-01 -o jsonpath="{.data['tls\.crt']}" | base64 -d
kubectl get secret -n certmanager wildcard-cloudflare-production-01 -o jsonpath="{.data['tls\.key']}" | base64 -d
```
