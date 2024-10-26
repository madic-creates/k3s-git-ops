# Authentication

Authelia runs [stateless](https://www.authelia.com/overview/authorization/statelessness/){target=_blank} and uses the following backends:

- Session Provider: Redis
- Storage Provider: MariaDB
- Notification Provider: SMTP
- Authentication Provider LDAP

## Authelia

Existing OIDC client secrets converted via

```shell
docker run authelia/authelia:latest authelia crypto hash generate pbkdf2 --variant sha512 --password <client_secret>
```
Generate new client secrets via:

```shell
docker run authelia/authelia:latest authelia crypto hash generate pbkdf2 --variant sha512 --random --random.length 72 --random.charset rfc3986
```

## LLDAP

For user and groups I use [LLDAP](https://github.com/lldap/lldap){target=_blank}. It's an opinionated light ldap implementation including a simple webgui.
It is configured to use MariaDB as it's storage backend.
