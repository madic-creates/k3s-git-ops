# Certificate Manager

cert-manager is a Kubernetes add-on that automates the management and issuance of TLS certificates from various issuing sources. In this cluster, it handles all TLS certificate provisioning for ingress resources using Let's Encrypt as the certificate authority with CloudFlare DNS-01 challenges.

## Overview

The cert-manager deployment in this cluster provides:

- Automated certificate issuance from Let's Encrypt (staging and production)
- DNS-01 challenge solving via CloudFlare API integration
- Wildcard certificate support
- Automatic certificate renewal before expiration
- Certificate distribution across namespaces via Reflector
- Integration with Traefik ingress controller

## Deployment Details

### Helm Chart Installation

cert-manager is deployed via ArgoCD using the Kustomize Helm chart integration. The deployment is managed in `/apps/certmanager/` with sync wave priority `1`, making it one of the first applications to be deployed after core infrastructure.

**Version**: v1.19.0 (Helm chart from [https://charts.jetstack.io](https://charts.jetstack.io))

**ArgoCD Application**: `apps/argo-cd-apps/01-certmanager.yaml`

**Key Configuration Options**:

```yaml
helmCharts:
  - name: cert-manager
    repo: https://charts.jetstack.io
    version: v1.19.0
    releaseName: cert-manager
    namespace: certmanager
    valuesInline:
      crds:
        enabled: true
        keep: true
      dns01RecursiveNameserversOnly: true
      dns01RecursiveNameservers: "9.9.9.9:53,1.1.1.1:53"
      global:
        revisionHistoryLimit: 3
```

### DNS Challenge Configuration

The cluster uses DNS-01 challenges instead of HTTP-01 challenges. This approach has several advantages:

- Supports wildcard certificates
- Works behind firewalls or private networks
- No need to expose port 80 during validation

The DNS challenge uses recursive nameservers (Quad9 and Cloudflare) to ensure reliable DNS propagation checking before attempting validation with Let's Encrypt.

## ClusterIssuers

Two ClusterIssuers are configured for certificate management:

### Production Issuer

**Name**: `cloudflare-issuer-production`

**ACME Server**: `https://acme-v02.api.letsencrypt.org/directory`

This issuer is used for production certificates with Let's Encrypt's rate limits enforced. It is the default issuer for all production ingress resources.

### Staging Issuer

**Name**: `cloudflare-issuer-staging`

**ACME Server**: `https://acme-staging-v02.api.letsencrypt.org/directory`

This issuer should be used for testing certificate configuration before switching to production. The staging server has much higher rate limits and issues certificates from a test CA that browsers will not trust.

### ClusterIssuer Configuration

Both issuers are configured with the following key settings:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: cloudflare-issuer-production
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: <encrypted-email>
    privateKeySecretRef:
      name: cloudflare-le-secret
    solvers:
      - dns01:
          cloudflare:
            email: <encrypted-email>
            apiTokenSecretRef:
              name: cloudflare-api-token
              key: api-token
```

The ClusterIssuer definitions are stored in encrypted form at `/apps/certmanager/cloudflare_issuer.enc.yaml`.

## CloudFlare DNS Integration

cert-manager uses a CloudFlare API token to manipulate DNS records for DNS-01 challenge validation. The API token is stored as an encrypted Kubernetes secret in `/apps/certmanager/secrets.enc.yaml`.

### CloudFlare API Token Requirements

The API token must have the following permissions:

- **Zone**: DNS:Edit for the zones you want to issue certificates for
- **Zone**: Zone:Read for the zones you want to issue certificates for

### Secret Configuration

The CloudFlare API token is stored in a Kubernetes secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloudflare-api-token
  namespace: certmanager
data:
  api-token: <base64-encoded-token>
```

This secret is referenced by both ClusterIssuers and must be available in the `certmanager` namespace before certificate issuance can succeed.

## Wildcard Certificates

The cluster uses pre-provisioned wildcard certificates that are automatically distributed to all namespaces using the [Reflector](reflector.md) operator. This approach has several advantages:

- Single certificate covers all subdomains
- Reduces Let's Encrypt rate limit consumption
- Certificates are available immediately in new namespaces
- Centralized certificate management

### Wildcard Certificate Resources

Two wildcard certificates are configured in `/apps/certmanager/wildcard.enc.yaml`:

1. **wildcard-cloudflare-production-01**: Covers the primary domain
2. **wildcard-cloudflare-production-02**: Covers a secondary domain

Example certificate configuration:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-cloudflare-production-01
  namespace: certmanager
spec:
  secretName: wildcard-cloudflare-production-01
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: cloudflare-issuer-production
    kind: ClusterIssuer
  commonName: <encrypted-domain>
  dnsNames:
    - <encrypted-wildcard-domain>
  secretTemplate:
    annotations:
      reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
      reflector.v1.k8s.emberstack.com/reflection-auto-enabled: "true"
```

### Certificate Distribution via Reflector

The `secretTemplate` section includes Reflector annotations that automatically distribute the certificate secret to all namespaces:

- `reflector.v1.k8s.emberstack.com/reflection-allowed: "true"`: Enables the secret to be reflected
- `reflector.v1.k8s.emberstack.com/reflection-auto-enabled: "true"`: Automatically creates copies in all namespaces

This means that once a wildcard certificate is issued in the `certmanager` namespace, it is immediately available in all other namespaces without manual intervention.

## Using Certificates in Ingress Resources

### Standard Ingress with Wildcard Certificate

Most ingress resources in this cluster use the pre-provisioned wildcard certificates. Here's an example:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: lldap
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239
    traefik.ingress.kubernetes.io/router.tls: "true"
spec:
  ingressClassName: traefik
  tls:
    - hosts:
        - lldap.example.com
      secretName: wildcard-cloudflare-production-02
  rules:
    - host: lldap.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: lldap
                port:
                  number: 17170
```

Key points:

- The `tls` section references one of the wildcard certificate secrets
- The secret is automatically available in the namespace via Reflector
- No cert-manager annotations are needed when using pre-provisioned certificates
- The hostname must match the wildcard pattern

### Requesting Application-Specific Certificates

If you need a certificate for a specific application that is not covered by the wildcard certificates, you can create a Certificate resource:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: myapp-tls
  namespace: myapp
spec:
  secretName: myapp-tls
  issuerRef:
    name: cloudflare-issuer-production
    kind: ClusterIssuer
  dnsNames:
    - myapp.example.com
    - www.myapp.example.com
```

Then reference it in your Ingress:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: myapp
spec:
  tls:
    - hosts:
        - myapp.example.com
        - www.myapp.example.com
      secretName: myapp-tls
  rules:
    - host: myapp.example.com
      # ... rest of ingress configuration
```

## Certificate Renewal

cert-manager automatically renews certificates before they expire. The default renewal window is 30 days before expiration (for Let's Encrypt's 90-day certificates, renewal happens around day 60).

### Monitoring Certificate Expiration

You can check certificate status using kubectl:

```shell
# View all certificates
kubectl get certificates -n certmanager

# Check certificate details
kubectl describe certificate wildcard-cloudflare-production-01 -n certmanager

# View certificate renewal status
kubectl get certificaterequest -n certmanager
```

### Certificate Renewal Process

1. cert-manager monitors certificate expiration dates
2. When a certificate enters the renewal window, cert-manager creates a CertificateRequest
3. The ClusterIssuer creates a DNS challenge record via CloudFlare API
4. Let's Encrypt validates the DNS record
5. cert-manager retrieves the new certificate and updates the Secret
6. Reflector automatically distributes the updated certificate to all namespaces

## Troubleshooting

### Checking cert-manager Logs

```shell
# View cert-manager controller logs
kubectl logs -n certmanager -l app=cert-manager -f

# View webhook logs
kubectl logs -n certmanager -l app=webhook -f

# View cainjector logs
kubectl logs -n certmanager -l app=cainjector -f
```

### Common Issues and Solutions

#### Certificate Not Issuing

**Symptoms**: Certificate remains in "Pending" state

**Troubleshooting steps**:

1. Check the Certificate status:
   ```shell
   kubectl describe certificate <certificate-name> -n certmanager
   ```

2. Check for CertificateRequest objects:
   ```shell
   kubectl get certificaterequest -n certmanager
   kubectl describe certificaterequest <request-name> -n certmanager
   ```

3. Check the Order and Challenge status:
   ```shell
   kubectl get orders -n certmanager
   kubectl describe order <order-name> -n certmanager

   kubectl get challenges -n certmanager
   kubectl describe challenge <challenge-name> -n certmanager
   ```

4. Verify CloudFlare API token permissions and validity

#### DNS Challenge Failing

**Symptoms**: Challenge remains in "Pending" state, events show DNS validation failures

**Troubleshooting steps**:

1. Verify the CloudFlare API token secret exists:
   ```shell
   kubectl get secret cloudflare-api-token -n certmanager
   ```

2. Check if DNS records are being created in CloudFlare:
   - Log into CloudFlare dashboard
   - Navigate to DNS records for your domain
   - Look for `_acme-challenge` TXT records

3. Verify DNS propagation:
   ```shell
   dig _acme-challenge.example.com TXT @9.9.9.9
   dig _acme-challenge.example.com TXT @1.1.1.1
   ```

4. Check cert-manager can reach DNS servers:
   ```shell
   kubectl logs -n certmanager -l app=cert-manager | grep -i dns
   ```

#### Certificate Not Distributing to Namespaces

**Symptoms**: Certificate exists in `certmanager` namespace but not in other namespaces

**Troubleshooting steps**:

1. Verify Reflector is running:
   ```shell
   kubectl get pods -n reflector
   ```

2. Check Reflector annotations on the certificate secret:
   ```shell
   kubectl get secret wildcard-cloudflare-production-01 -n certmanager -o yaml | grep -A5 annotations
   ```

3. Check Reflector logs:
   ```shell
   kubectl logs -n reflector -l app.kubernetes.io/name=reflector
   ```

4. Verify the certificate secret exists in the source namespace:
   ```shell
   kubectl get secret wildcard-cloudflare-production-01 -n certmanager
   ```

#### Rate Limiting

**Symptoms**: Certificate issuance fails with rate limit errors

**Solutions**:

1. Use the staging issuer for testing:
   - Change `issuerRef.name` to `cloudflare-issuer-staging`
   - Test your configuration thoroughly
   - Switch back to production issuer once verified

2. Let's Encrypt rate limits:
   - 50 certificates per registered domain per week
   - 5 duplicate certificates per week
   - Use wildcard certificates to reduce certificate count

3. Wait for rate limit window to reset (usually 7 days)

### Extracting Certificate for External Use

If you need to extract a certificate and private key for use outside Kubernetes:

```shell
# Extract certificate
kubectl get secret -n certmanager wildcard-cloudflare-production-01 \
  -o jsonpath="{.data['tls\.crt']}" | base64 -d > certificate.crt

# Extract private key (handle with care!)
kubectl get secret -n certmanager wildcard-cloudflare-production-01 \
  -o jsonpath="{.data['tls\.key']}" | base64 -d > private.key

# Extract CA certificate if present
kubectl get secret -n certmanager wildcard-cloudflare-production-01 \
  -o jsonpath="{.data['ca\.crt']}" | base64 -d > ca.crt
```

**Security note**: Handle private keys with extreme care. Never commit them to version control or share them insecurely.

### Manual Certificate Renewal

To force certificate renewal (e.g., after fixing an issue):

```shell
# Delete the secret to trigger re-issuance
kubectl delete secret wildcard-cloudflare-production-01 -n certmanager

# Or use cmctl to renew
cmctl renew wildcard-cloudflare-production-01 -n certmanager
```

## Integration with Other Components

### Traefik Ingress Controller

All ingress resources in this cluster use Traefik as the ingress controller. Traefik automatically reads certificate secrets referenced in the `tls` section of Ingress resources.

Example Traefik ingress with TLS:

```yaml
metadata:
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239
    traefik.ingress.kubernetes.io/router.tls: "true"
spec:
  tls:
    - hosts:
        - app.example.com
      secretName: wildcard-cloudflare-production-02
```

## Secret Management with SOPS

All cert-manager configuration files containing sensitive data are encrypted using SOPS with age encryption:

- `/apps/certmanager/cloudflare_issuer.enc.yaml`: ClusterIssuer definitions with encrypted email addresses
- `/apps/certmanager/wildcard.enc.yaml`: Certificate resources with encrypted domain names
- `/apps/certmanager/secrets.enc.yaml`: CloudFlare API token secret

To decrypt and edit these files:

```shell
# Decrypt and edit in one command
sops apps/certmanager/cloudflare_issuer.enc.yaml

# Or decrypt to a separate file
sops -d apps/certmanager/secrets.enc.yaml > secrets-decrypted.yaml
```

For more information on secret management, see [Secret Management](secretmanagement.md).

## Testing Certificate Configuration Locally

Before committing changes to certificate configuration, test them locally:

```shell
# Build kustomization to preview resources
kubectl kustomize apps/certmanager --enable-helm

# Dry-run apply to validate syntax
kubectl apply --dry-run=client -k apps/certmanager

# Apply to test environment (if using Vagrant)
export KUBECONFIG="$PWD/shared/k3svm1/k3s.yaml"
kubectl kustomize apps/certmanager --enable-helm | kubectl apply -f -

# Check ArgoCD sync diff
argocd app diff 01-certmanager
```

## Best Practices

1. **Use wildcard certificates**: Reduces certificate count and simplifies management
2. **Test with staging issuer**: Always test new configurations with the staging issuer before production
3. **Monitor certificate expiration**: Set up alerts for certificates nearing expiration (built into kube-prometheus-stack)
4. **Secure API tokens**: Ensure CloudFlare API tokens have minimal required permissions
5. **Encrypt sensitive data**: Use SOPS to encrypt all configuration containing domain names, emails, or tokens
6. **Document custom certificates**: If creating application-specific certificates, document the reason for not using wildcards
7. **Regular secret rotation**: Rotate CloudFlare API tokens periodically (recommended annually)

## Related Documentation

- [Secret Management](secretmanagement.md): SOPS encryption and decryption workflows
- [Reflector](reflector.md): Certificate distribution across namespaces
- [Network Architecture](network.md): Traefik ingress controller configuration
- [ArgoCD](argocd.md): Application deployment and sync waves
- [Monitoring](monitoring.md): Certificate expiration alerts

## References

- [cert-manager documentation](https://cert-manager.io/docs/)
- [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/)
- [CloudFlare API token documentation](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/)
- [DNS-01 challenge documentation](https://cert-manager.io/docs/configuration/acme/dns01/)
