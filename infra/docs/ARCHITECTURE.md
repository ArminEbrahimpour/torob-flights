# Infrastructure

```mermaid
flowchart TB
  NET{{torob-net}}
  PG[(PostgreSQL\nflight_services + ranker_db)] --- NET
  REDIS[(Redis)] --- NET
  MQ[(RabbitMQ)] --- NET
  ES[(Elasticsearch)] --- NET
  PROM[Prometheus] --- NET
  GRAF[Grafana] --- NET
  APPS[application compose projects] --- NET
```

Infra owns stateful service images, persistent volumes, health checks, the
shared Docker network, PostgreSQL database bootstrap, and monitoring defaults.
Application Compose projects are stateless and consume infra DNS names. Init
scripts run only when the PostgreSQL volume is created; password changes use
`ALTER ROLE` and never require deleting data.
