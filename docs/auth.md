# Authentication and Single Sign-On (SSO)

/// note | Overview
This cluster implements a comprehensive authentication architecture with Single Sign-On capabilities using Authelia as the identity provider, LLDAP for user management, and Traefik ForwardAuth middleware to protect all exposed services.
///

## Architecture

The authentication system consists of four main components working together:

```
┌─────────────────────────────────────────────────────────────────┐
│                          User Request                           │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                                 v
┌─────────────────────────────────────────────────────────────────┐
│                     Traefik Ingress Controller                  │
│                 (ForwardAuth Middleware Applied)                │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                                 v
┌─────────────────────────────────────────────────────────────────┐
│                          Authelia                               │
│                  (Authentication & Authorization)               │
│                                                                 │
│  ┌─────────────┐    ┌────────────┐    ┌─────────────┐           │
│  │   Session   │    │  Storage   │    │    OIDC     │           │
│  │   (Redis)   │    │ (MariaDB)  │    │  Provider   │           │
│  └─────────────┘    └────────────┘    └─────────────┘           │
└────────────────────────────────┬────────────────────────────────┘
                                 │
                                 v
┌─────────────────────────────────────────────────────────────────┐
│                            LLDAP                                │
│                   (LDAP User Directory)                         │
│                      (MariaDB Backend)                          │
└─────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Role | Storage Backend | Namespace |
|-----------|------|----------------|-----------|
| **Authelia** | Authentication, authorization, session management, OIDC provider | MariaDB (storage), Redis (sessions) | `authelia` |
| **LLDAP** | Lightweight LDAP directory for user/group management | MariaDB | `authelia` |
| **Redis** | Session storage for Authelia | In-memory with optional persistence | `databases` |
| **MariaDB** | Persistent storage for Authelia data and LLDAP users | Persistent volumes | `databases` |
| **Traefik** | ForwardAuth middleware enforcement | N/A | `kube-system` |

## Deployment Architecture

### Sync Wave Order

Applications are deployed in a specific order via ArgoCD sync waves to ensure dependencies are met:

1. **Wave 09**: Redis (Session backend)
   - Helm chart: `bitnami/redis:23.1.1`
   - Deployed to: `databases` namespace
   - See: `apps/argo-cd-apps/09-redis.yaml`

2. **Wave 10**: Authelia (Authentication service)
   - Helm chart: `authelia/authelia:0.10.46`
   - Deployed to: `authelia` namespace
   - Replicas: 2 (high availability)
   - See: `apps/argo-cd-apps/10-authelia.yaml`

3. **Wave 11**: LLDAP (User directory)
   - Custom deployment
   - Image: `ghcr.io/lldap/lldap:latest-alpine`
   - Deployed to: `authelia` namespace
   - See: `apps/argo-cd-apps/11-lldap.yaml`

/// note | MariaDB Dependency
MariaDB is deployed earlier in sync wave 08 and must be available before Authelia and LLDAP start. See the [Databases](databases.md) documentation for details.
///

## Authelia Configuration

Authelia is responsible for authentication and authorization.

### Authentication Backend: LDAP

Authelia uses LLDAP as its authentication backend:

```yaml
authentication_backend:
  password_reset:
    disable: false
  ldap:
    enabled: true
    implementation: custom
    address: ldaps://lldap.authelia.svc.cluster.local:6360
    base_dn: DC=example,DC=com
    additional_users_dn: ou=people
    users_filter: (&({username_attribute}={input})(objectClass=person))
    additional_groups_dn: ou=groups
    groups_filter: (member={dn})
    attributes:
      username: uid
      group_name: cn
      mail: mail
    user: uid=admin,ou=people,dc=example,dc=com
```

**Key Points:**
- Provices encrypted LDAP connection within cluster (cluster-internal communication)
- Password reset functionality is enabled
- Users are stored under `ou=people`
- Groups are stored under `ou=groups`
- Currently Authelia binds as the LDAP admin user to query directory

### Session Management: Redis

Sessions are stored in Redis for authelia scalability:

```yaml
session:
  name: authelia_session
  expiration: 3600           # 1 hour
  inactivity: 300            # 5 minutes
  remember_me: 1 month
  redis:
    enabled: true
    host: redis-master.databases.svc.cluster.local
    port: 6379
    database_index: 1
```

**Session Behavior:**
- Sessions expire after 1 hour of total time
- Sessions expire after 5 minutes of inactivity
- "Remember me" option extends session to 1 month
- Sessions are stored in Redis database index 1 (separate from other applications)

### Storage Backend: MariaDB

Authelia stores persistent data in MariaDB:

```yaml
storage:
  mysql:
    enabled: true
    address: tcp://mariadb.databases.svc.cluster.local:3306
    database: authelia
    username: authelia
```

**Stored Data:**
- User preferences
- TOTP secrets for 2FA
- U2F device registrations
- OAuth2 consent decisions
- Identity verification tokens

### Access Control Policies

The default policy is `deny` with explicit rules granting access:

```yaml
access_control:
  default_policy: deny
  rules:
    - domain: '*.k8s.example.com'
      policy: two_factor
```

**Policy Explanation:**
- All access is denied by default
- Any subdomain under `k8s.example.com` and `internal.example.com` requires two-factor authentication
- Additional rules can be added for specific applications or paths

/// warning | Two-Factor Enforcement
All exposed services require two-factor authentication (TOTP or U2F). Users must configure 2FA before accessing protected resources.
///

### OIDC Provider

Authelia acts as an OpenID Connect provider for applications that support native OIDC integration:

```yaml
identity_providers:
  oidc:
    enabled: true
    clients:
      - client_id: grafana
        client_name: Grafana
        authorization_policy: two_factor
        redirect_uris:
          - https://grafana.example.com/login/generic_oauth
        scopes:
          - openid
          - profile
          - groups
          - email

      - client_id: argocd
        client_name: Argo CD
        authorization_policy: two_factor
        redirect_uris:
          - https://argocd.example.com/auth/callback
        scopes:
          - openid
          - profile
          - groups
          - email
```

**Some OIDC Clients:**
- **Grafana**: Integrated for monitoring dashboard access
- **Argo CD**: Integrated for GitOps management console
- **Semaphore**: Integrated for Ansible playbook automation

/// tip | Adding OIDC Clients
To add a new OIDC client, generate a client secret using:
```bash
docker run authelia/authelia:latest authelia crypto hash generate pbkdf2 \
  --variant sha512 --random --random.length 72 --random.charset rfc3986
```
Then add the client configuration to `apps/authelia/values.enc.yaml`.
///

### Notification Provider: SMTP

Authelia sends notifications via SMTP for password resets and identity verification:

```yaml
notifier:
  smtp:
    enabled: true
    address: smtp://192.168.1.45:25
    sender: Authelia <auth@example.com>
    disable_require_tls: true
```

/// note | Internal SMTP
This configuration uses an SMTP relay outside of the cluster, but from within the home network, without authentication.
///

### Authorization Endpoints

Authelia exposes multiple authorization endpoints for different middleware types:

```yaml
server:
  endpoints:
    authz:
      forward-auth:
        implementation: ForwardAuth
        authn_strategies:
          - name: HeaderAuthorization
            schemes:
              - Basic
          - name: CookieSession
      auth-request:
        implementation: AuthRequest
      ext-authz:
        implementation: ExtAuthz
```

- **ForwardAuth**: Used by Traefik (currently in use)
- **AuthRequest**: Compatible with NGINX ingress
- **ExtAuthz**: Compatible with Envoy proxy

## LLDAP Configuration

LLDAP provides a lightweight, opinionated LDAP directory with a web UI.

### Deployment

LLDAP runs as a single replica deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lldap
  namespace: authelia
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: lldap
          image: ghcr.io/lldap/lldap:latest-alpine
          ports:
            - containerPort: 3890   # LDAP
            - containerPort: 6360   # LDAPS
            - containerPort: 17170  # Web UI
```

### Service Endpoints

LLDAP exposes two service ports:

- **Port 3890**: LDAP protocol
- **Port 6360**: LDAPS protocol
- **Port 17170**: Web management interface

### Web Interface Access

LLDAP has its own ingress for administrative management:

/// note | LLDAP Admin Access
The LLDAP web interface is accessible but does NOT have ForwardAuth middleware applied. It uses its own internal authentication.
///

### User and Group Management

LLDAP follows a simplified LDAP schema:

- **Base DN**: `DC=example,DC=com`
- **Users**: `ou=people,dc=example,dc=com`
- **Groups**: `ou=groups,dc=example,dc=com`

**Adding Users:**
1. Access the LLDAP web interface at the configured ingress
2. Navigate to "Users" and click "Create User"
3. Fill in required fields: username, email, display name
4. Set initial password
5. Assign groups as needed

**Managing Groups:**
1. Navigate to "Groups" in LLDAP UI
2. Create groups to organize users
3. Add users to groups
4. Use groups in Authelia access control policies or OIDC claims

## Traefik Integration

Traefik enforces authentication using the ForwardAuth middleware.

### ForwardAuth Middleware

The Authelia Helm chart automatically creates a ForwardAuth middleware resource:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: forwardauth-authelia
  namespace: authelia
spec:
  forwardAuth:
    address: http://authelia.authelia.svc.cluster.local/api/authz/forward-auth
    trustForwardHeader: true
    authResponseHeaders:
      - Remote-User
      - Remote-Groups
      - Remote-Name
      - Remote-Email
```

**Middleware Behavior:**
- Traefik sends request headers to Authelia
- Authelia validates authentication and authorization
- If authenticated: Request proceeds with identity headers added
- If not authenticated: User redirected to Authelia login page

### Protecting Services with ForwardAuth

To protect an application with SSO, add the middleware annotation to its Ingress:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  namespace: my-namespace
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    traefik.ingress.kubernetes.io/router.middlewares: traefik-redirect@kubernetescrd, authelia-forwardauth-authelia@kubernetescrd
spec:
  ingressClassName: traefik
  rules:
    - host: my-app.k8s.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app
                port:
                  number: 80
```

## Secret Management

All sensitive authentication data is encrypted with SOPS.

### Encrypted Files

Authentication secrets are stored in:

- `apps/authelia/values.enc.yaml`: Authelia secrets (LDAP password, session keys, OIDC secrets)
- `apps/lldap/lldap-secret.enc.yaml`: LLDAP admin password and JWT secret
- `apps/redis/values.enc.yaml`: Redis password (if authentication enabled; currently disabled)

### Secret Rotation

**Rotating OIDC Client Secrets:**

1. Generate new client secret:
```bash
docker run authelia/authelia:latest authelia crypto hash generate pbkdf2 \
  --variant sha512 --random --random.length 72 --random.charset rfc3986
```

2. Update the application configuration with the new secret
3. Update `apps/authelia/values.enc.yaml` with the new hashed secret
4. Commit changes and let ArgoCD sync

**Rotating Session Encryption Key:**

1. Generate new random key (64 characters recommended)
2. Update `session.encryption_key.value` in Authelia configuration
3. Commit and sync
4. All existing sessions will be invalidated

/// warning | Session Key Rotation
Rotating the session encryption key will log out all users.
///

## Troubleshooting

### emby ldaps connection

Emby requires the sha1 fingerprint from the certificate.
After a renewal, the sha1 fingerprint changes and emby needs the new one.
To get the fingerprint use the following command in a pod within the cluster (e.g. netshoot):

```bash
openssl s_client -showcerts -connect lldap.authelia.svc.cluster.local:6360 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha1 \
  | sed 's/://g' | sed 's/SHA1 Fingerprint=//'
sha1 Fingerprint=77FE790909B81EDDDB8523DCD4206D7BA0B503B8
```

### Login Failures

**Symptom:** User cannot log in with correct credentials

**Diagnosis:**
```bash
# Check Authelia logs
kubectl logs -n authelia -l app.kubernetes.io/name=authelia --tail=100

# Check LLDAP logs
kubectl logs -n authelia -l app.kubernetes.io/name=lldap --tail=100

# Test LDAP connection from netshoot pod
kubectl exec -n kube-system deployment/netshoot -- \
  apk add openldap-clients

kubectl exec -n kube-system deployment/netshoot -- \
 sh -c 'LDAPTLS_REQCERT=never ldapsearch -H ldaps://lldap.authelia.svc.cluster.local:6360 \
  -D "uid=admin,ou=people,dc=example,dc=com" \
  -w "PASSWORD" -b "dc=example,dc=com"'
```

**Common Causes:**
- LDAP bind password incorrect
- LLDAP service not running
- User doesn't exist in LLDAP
- User not in required group

## Best Practices

### Security Recommendations

1. **Enforce Two-Factor Authentication**
   - Set `policy: two_factor` for all production services
   - Use hardware security keys (U2F) for administrator accounts
   - Regularly audit user 2FA enrollment

2. **Rotate Secrets Regularly**
   - Rotate OIDC client secrets annually
   - Rotate session encryption keys during maintenance windows
   - Use strong random passwords for LDAP bind accounts

3. **Monitor Authentication Logs**
   - Set up alerts for repeated login failures
   - Monitor Authelia logs for suspicious activity
   - Track OIDC token issuance

4. **Network Segmentation**
   - Use Cilium network policies to restrict access to authentication components
   - Only allow necessary egress to Authelia from application pods
   - Isolate LLDAP to only Authelia access

5. **Backup Critical Data**
   - Include MariaDB `authelia` database in backup strategy
   - Backup LLDAP database regularly
   - Store SOPS encryption keys securely off-cluster

### Access Control Design

1. **Use Groups for Authorization**
   - Create LDAP groups for different access levels (admins, users, viewers)
   - Reference groups in Authelia access control policies
   - Pass groups as OIDC claims to applications

2. **Granular Policies**
   - Define specific access rules per application
   - Use domain and path-based rules for fine-grained control
   - Document policy decisions in comments

3. **Test Access Policies**
   - Test with non-privileged user accounts
   - Verify group membership affects access
   - Ensure deny-by-default behavior

## Additional Resources

- [Authelia Documentation](https://www.authelia.com/){target=_blank}
- [LLDAP Documentation](https://github.com/lldap/lldap){target=_blank}
- [Traefik ForwardAuth Documentation](https://doc.traefik.io/traefik/middlewares/http/forwardauth/){target=_blank}
- [Cilium Network Policy Guide](https://docs.cilium.io/en/stable/security/policy/){target=_blank}

## Related Documentation

- [Databases](databases.md) - MariaDB and Redis configuration
- [Network](network.md) - Traefik ingress and network architecture
- [Secret Management](secretmanagement.md) - SOPS encryption details
- [Monitoring](monitoring.md) - Observability for authentication components
