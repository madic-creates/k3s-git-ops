# td-sync

[td](https://github.com/marcus/td){target=_blank} is a task-tracker CLI with an
optional sync server so multiple machines (or AI sessions) can share the same
issue database. `td-sync` is that sync server, deployed in this cluster.

- **Namespace**: `td-sync`
- **URL**: `https://td.internal.neese-web.de`
- **ArgoCD app**: `62-td-sync`
- **Image**: built by `madic/docker-td` on `forge.geekbundle.org`, pinned by
  tag and digest in `apps/td-sync/k8s.td-sync.yaml`

## Creating a user

Upstream's device-login flow (`td auth login`) cannot create the first
account: unknown emails are silently accepted and suppressed for enumeration
protection, so nothing actually gets created. `SYNC_ALLOW_SIGNUP` is not
consulted by that endpoint at all, and stays `"false"` in this deployment.

The only way to provision an account is the admin CLI, run inside the running
pod:

```bash
kubectl exec -n td-sync deploy/td-sync -- td-sync admin create-user --email <address>
```

Upstream recommends running this while the server is not holding the database
open. Here it runs against the live pod instead; SQLite's WAL locking makes
that acceptable for an idempotent one-shot provisioning command.

## Logging in

td-sync has no SMTP provider configured, so the magic link that
`td auth login` triggers is retrieved from the server's dev inspection
endpoint rather than an inbox.

```bash
export TD_ENABLE_FEATURE=sync_cli   # sync commands are behind this feature flag
td config set sync.url https://td.internal.neese-web.de
td auth login
```

`td auth login` sends the email and waits. Fetch the link it generated:

```bash
curl -s https://td.internal.neese-web.de/internal/dev/last-email | jq -r .text
```

Open the printed `/auth/device/approve?token=...` URL in a browser to approve
the login. Links expire after 15 minutes. The in-memory email provider holds
only the single most recent email and loses it on pod restart, so re-run
`td auth login` if too much time passes between the two steps.

### Security note

`GET /internal/dev/last-email` is unauthenticated and returns the full body
of the most recently sent login email — including the magic link token in
plaintext — to anyone who can reach the ingress. This is enabled deliberately
(`SYNC_EMAIL_PROVIDER=memory` + `SYNC_DEV_EMAIL_INSPECT=true`) as a trade-off
for a single-user deployment with no SMTP relay available. Anyone who can
reach `https://td.internal.neese-web.de` during the ~15 minute window after a
login attempt can complete that login.

## Other admin subcommands

`td-sync admin` also provides:

- `grant` - grant a user access to a project
- `revoke` - revoke a user's access to a project
- `create-key` - create an API key
- `revoke-key` - revoke an API key

Run any of them the same way, via `kubectl exec -n td-sync deploy/td-sync --`.
