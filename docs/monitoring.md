# Monitoring

## Metrics

The [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack){target=_blank} helm chart pre-configures the following components:

  - Prometheus (Operator)
  - Grafana
  - Node Exporter
  - Alertmanager
  - kube-state-metrics

[![Cluster Compute Resources](images/cluster_compute_resources.png){: loading=lazy}](images/cluster_compute_resources.png)

Additionally the Alertmanager sends alerts via webhook to the [ntfy-alertmanager](https://hub.xenrox.net/~xenrox/ntfy-alertmanager/){target=_blank} which forwards it to a selfhosted [ntfy.sh](https://ntfy.sh/){target=_blank} instance.

[![ntfy alert](images/ntfy_alert.png){: loading=lazy}](images/ntfy_alert.png)

The encrypted config-file from the ntfy-alertmanager is basically this.

More information can be found in the [ntfy-alertmanager config documentation](https://git.xenrox.net/~xenrox/ntfy-alertmanager/tree/master/item/config.scfg){target=_blank}.

```ini
base-url https://ntfy-alertmanager.k8s.example.com
http-address :8080
log-level info
log-format text
alert-mode single
user <SuperSecureUser>
password <SuperSecurePassword>
labels {
    order "severity,instance"
    severity "critical" {
        priority 5
        tags "rotating_light"
        icon ""
    }
    severity "info" {
        priority 1
    }
}
resolved {
    tags "resolved,partying_face"
    priority 1
}
ntfy {
    topic https://ntfy.example.com/kubernetes-at-home
    access-token <SuperSecureAccessToken>
}
alertmanager {
    silence-duration 24h
    url http://kube-prometheus-stack-alertmanager.monitoring.svc.cluster.local:9093
}
cache {
    type disabled
    duration 24h
    cleanup-interval 1h
    redis-url redis://redis-master.databases.svc.cluster.local:6379/3
}
```

## Logging

Loki and promtail are used as pod log collector. The grafan helm repository provides multiple loki charts. `loki-stack` is outdated and shouldn't be used. The other interesting charts are grafana/loki (SSD Mode) and grafan/loki-distributed (microservice mode). More information on the modes (SSD vs. microservices): [https://grafana.com/docs/loki/latest/get-started/deployment-modes/](https://grafana.com/docs/loki/latest/get-started/deployment-modes/){target=_blank}. This repository uses the SSD mode.

[![Loki](images/logging.png){: loading=lazy}](images/logging.png)

## Kubernetes Events
