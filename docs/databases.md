# Datenbanken

## Redis

| Database | Application |
| -------- | ----------- |
| 1        | authelia    |
| 2        | paperless   |

Get all keys from a database:

```bash
redis-cli -n 1 KEYS '*'
```

```bash
redis-cli SET server:name "redis server"
redis-cli GET server:name
```

## MariaDB

Execute these commands in the shell of the MariaDB Container.

Logging into the database:

```bash
mariadb -u root --password=$MARIADB_ROOT_PASSWORD
```

Show all databases:

```sql
SHOW databases;
```

Show permiissions for user:

```sql
SHOW GRANTS FOR 'authelia'@'host';
```

Create database:

```sql
CREATE DATABASE authelia;
```

Create user with password:

```sql
CREATE USER 'authelia'@'%' IDENTIFIED BY '<NotSoSecure>';
GRANT ALL PRIVILEGES ON authelia.* TO 'authelia'@'%';
FLUSH PRIVILEGES;
```

Create user without password, but only allowed from specific networks:

```sql
CREATE USER 'authelia'@'10.42.0.0/255.255.0.0';
GRANT ALL PRIVILEGES ON authelia.* TO 'authelia'@'10.42.0.0/255.255.0.0';
FLUSH PRIVILEGES;
```

Delete user:

```sql
DROP USER 'authelia'@'%';
```

### Backup

Backup is done via mariadb-backup and restic. mariadb-backup sends its stdout to restic stdin, which is then stored as file in the restic repository, including deduplication.

```shell title="Backup command"
mariadb-backup --host mariadb.databases.svc.cluster.local --user=root --password=$MARIADB_ROOT_PASSWORD --backup --stream=xbstream | restic backup --stdin --stdin-filename mariadb.xb --tag MariaDB
```

```shell title="Example restore proccess"
# Get backups
restic snapshots --tag MariaDB
# Restore latest snapshot
restic restore latest --target .
# Un-serialize the backup with mbstream
mkdir mariadb_recovery
mbstream -x <mariadb.xb
# Prepare the recovery
mariadb-backup --prepare --target-dir=./mariadb_recovery
```

More information about the process in the following fosdem presentation: [mariabackup restic](https://archive.fosdem.org/2022/schedule/event/mariadb_backup_restic/attachments/slides/5135/export/events/attachments/mariadb_backup_restic/slides/5135/mariabackup_restic.pdf){target=_blank}
