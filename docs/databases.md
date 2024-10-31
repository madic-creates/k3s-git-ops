# Datenbanken

## Redis

Database 1 for authelia.
Database 2 for paperless.

Get all keys from a database:

```bash
redis-cli -n 1 KEYS '*'
```

```bash
redis-cli SET server:name "redis server"
redis-cli GET server:name
```

## MariaDB

Create new database

Execute in the shell of the MariaDB Container:

```bash
mariadb -u root --password=$MARIADB_ROOT_PASSWORD
```

```sql
SHOW databases;
```

```sql
SHOW GRANTS FOR 'authelia'@'host';
```

Create database:

```sql
CREATE DATABASE authelia;
```

User with password:

```sql
CREATE USER 'authelia'@'%' IDENTIFIED BY 'zQMNYwDhhuyRsWzK7rzQhJ2NEzVFvgop9sMcJfmPpem9JEcuwE24rd9RYwYsb2SS';
GRANT ALL PRIVILEGES ON authelia.* TO 'authelia'@'%';
FLUSH PRIVILEGES;
```

User without password, but only allowed from specific networks:

```sql
CREATE USER 'authelia'@'10.42.0.0/255.255.0.0';
GRANT ALL PRIVILEGES ON authelia.* TO 'authelia'@'10.42.0.0/255.255.0.0';
FLUSH PRIVILEGES;
```

Delete user:

```sql
DROP USER 'authelia'@'%';
```
