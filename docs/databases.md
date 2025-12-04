# Datenbanken

## MariaDB

Execute these commands in the shell of the MariaDB Container.

Logging into the database:

```bash linenums="0"
mariadb -u root --password=$(cat $MARIADB_ROOT_PASSWORD_FILE)
```

Show all databases:

```sql linenums="0"
SHOW databases;
```

Show permissions for user:

```sql linenums="0"
SHOW GRANTS FOR 'authelia'@'host';
```

Create database:

```sql linenums="0"
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

```sql linenums="0"
DROP USER 'authelia'@'%';
```

### Backup

The backup for MariaDB in this setup is handled using `mariadb-backup` and `restic`. mariadb-backup creates a [physical backup](https://www.managedserver.eu/backup-mysql-percona-and-mariadb-xbstream-and-mbstream-format/#Backup_fisici){target=_blank} and restic stores the file in a repository, providing compression and deduplication.

```shell title="Backup command" linenums="0"
mariadb-backup --host mariadb.databases.svc.cluster.local --user=root --password=$MARIADB_ROOT_PASSWORD --backup --target-dir=/backup --stream=xbstream > /backup/mariadb.xb
```

#### Restore Process

The restoration of backups involves retrieving backup snapshots from restic, deserializing, and preparing for database usage.

1. **List Backups**: Get available backups stored in the restic repository

```shell linenums="0"
restic snapshots --tag MariaDB
```

2. **Restore Snapshot**: Extract the latest snapshot to a target directory

```shell linenums="0"
restic restore latest --tag MariaDB --target .
```

3. **Unserialize the Backup**: Use `mbstream` to deserialize the backup file

```shell
mkdir mariadb_recovery
mbstream -x <mariadb.xb
```

4. **Prepare the Recovery**: Prepare the backup for use. If possible, use the same mariadb-backup version with which the backup was created

```shell linenums="0"
mariadb-backup --prepare --target-dir=./mariadb_recovery
```

More information about the process in the following fosdem presentation: [mariabackup restic](https://archive.fosdem.org/2022/schedule/event/mariadb_backup_restic/attachments/slides/5135/export/events/attachments/mariadb_backup_restic/slides/5135/mariabackup_restic.pdf){target=_blank}

#### Kubernetes CronJob for Automated Backups

A Kubernetes CronJob is used to automate the MariaDB backups. At first it creates the backup with mariadb-backup and then stores it in a restic repository. To ensure that the processes run sequential and not parallel, the backup creation runs as init container and afterwards restic as normal container. For best compatibility the mariadb-backup command must be the same version as the MariaDB server. So the init Container of the cronjob executes the mariadb-backup binary within the mariadb container. This way I always get the correct mariadb-backup version.

**Explanation:**

- **Service Account and Role**: A service account `mariadb-backup-serviceaccount` is used, bound with a role `mariadb-backup-role` that has necessary permissions to get the pod name and exec into the container

- **CronJob Configuration**: The CronJob is set to run every hour, except 2 o'clock in the night `"0 0-1,3-23 * * *"`

- **Backup Initialization**: `mariadb-backup` runs as an `initContainer` to ensure backups are taken before any other process begins

- **Restic**: `restic` stores the backup in a restic repository which is accessible via an s3 bucket

- **Storage**: A `PersistentVolumeClaim` (`longhorn-pvc-mariadb-backupvolume`) is used to store the current backup temporarily

The backup strategy is designed to ensure that `mariadb-backup` uses a version compatible with the running MariaDB server by executing it within the MariaDB container.

```yaml title="Shortened Kubernetes CronJob"
apiVersion: batch/v1
kind: CronJob
metadata:
  name: mariadb-backup
  namespace: databases
spec:
  schedule: "0 0-1,3-23 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: mariadb-backup-serviceaccount
          initContainers:
            - name: kubectl-mariadb-backup
              image: bitnami/kubectl:latest
              command:
                - /bin/sh
                - -c
                - >
                  POD_NAME=$(kubectl get pods -n databases -l app.kubernetes.io/name=mariadb -o jsonpath="{.items[0].metadata.name}") &&
                  kubectl exec -n databases $POD_NAME -- mariadb-backup --host 127.0.0.1 --user=root --password=$MARIADB_ROOT_PASSWORD --backup --stream=xbstream > /backup/mariadb.xb
          containers:
            - name: restic
              image: ghcr.io/restic/restic:0.17.3
              command:
                - /bin/sh
                - /usr/local/bin/backup.sh
              volumeMounts:
                - name: backup
                  mountPath: /backup
```

## Redis

| Database | Application |
| -------- | ----------- |
| 1        | authelia    |
| 2        | paperless   |

Get all keys from a database:

```bash linenums="0"
redis-cli -n 1 KEYS '*'
```

```bash
redis-cli SET server:name "redis server"
redis-cli GET server:name
```
