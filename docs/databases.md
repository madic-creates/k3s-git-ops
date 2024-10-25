# Datenbanken

## Redis

Datenbank 1 für authelia.
Datenbank 2 für paperless.

Alle Keys einer Datenbank ausgeben:

```bash
redis-cli -n 1 KEYS '*'
```

```bash
redis-cli SET server:name "redis server"
redis-cli GET server:name
```

## MariaDB

Neue Datenbank erstellen

In die shell vom MariaDB Container:

```bash
mariadb -u root -p
```

```sql
SHOW databases;
```

```sql
SHOW GRANTS FOR 'authelia'@'host';
```

Datenbank erstellen:

```sql
CREATE DATABASE authelia;
```

User mit Passwort:

```sql
CREATE USER 'authelia'@'%' IDENTIFIED BY 'zQMNYwDhhuyRsWzK7rzQhJ2NEzVFvgop9sMcJfmPpem9JEcuwE24rd9RYwYsb2SS';
GRANT ALL PRIVILEGES ON authelia.* TO 'authelia'@'%';
FLUSH PRIVILEGES;
```

User ohne Passwort, mit Netzwerkeinschränkung:

```sql
CREATE USER 'authelia'@'10.42.0.0/255.255.0.0';
GRANT ALL PRIVILEGES ON authelia.* TO 'authelia'@'10.42.0.0/255.255.0.0';
FLUSH PRIVILEGES;
```

User löschen:

```sql
DROP USER 'authelia'@'%';
```
