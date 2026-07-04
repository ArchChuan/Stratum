# K3s Demo Deployment Design

## Goal

Prepare Stratum for a low-cost public demo deployment on one cloud host using K3s and Helm.

This deployment is for demonstration and evaluation. It should be inexpensive, repeatable, HTTPS-enabled, and close enough to Kubernetes production practices that it can later migrate to managed Kubernetes or a multi-node cluster. It does not target high availability.

## Current State

The repository already has both `helm/` and `k8s/` deployment assets.

The Helm chart is the target deployment interface, but it is incomplete for a public demo:

- `helm/values-prod.yaml` describes Ingress, TLS, managed Postgres, Redis, autoscaling, and production image repositories.
- The existing Helm templates render backend and frontend deployments/services, plus frontend nginx config.
- The Helm chart does not currently render Ingress, backend ConfigMap, backend Secret references, HPA, PDB, NetworkPolicy, or dependency workloads.
- Some values in `values-prod.yaml` are not consumed by templates.

The raw `k8s/` manifests contain useful references, but they should not become the main deployment path:

- They include Ingress, ConfigMap, Secret example, HPA, PDB, NetworkPolicy, ResourceQuota, and security examples.
- They contain stale or environment-bound details such as old Milvus image versions, AWS service account annotation examples, Neo4j variables, and namespace inconsistencies.
- They are useful as migration references, not as files to copy blindly.

## Recommended Approach

Use a single-node K3s demo stack with Helm as the only application deployment surface.

The host runs:

- K3s
- Traefik Ingress from K3s by default
- cert-manager for Let's Encrypt certificates
- Stratum frontend
- Stratum backend
- PostgreSQL
- Redis
- NATS JetStream
- Milvus standalone and its required backing services

The default demo target is one cloud host with roughly 4 vCPU, 8 GiB RAM, and 80 GiB disk. A smaller 2 vCPU / 4 GiB host may work only if vector search and dependency resource limits are reduced.

## Public Traffic Flow

External traffic enters through HTTPS only:

```text
Browser
  -> domain DNS A record
  -> cloud host public IP
  -> Traefik Ingress
  -> frontend service
  -> frontend nginx
  -> backend service for /api/*
```

Only ports 80 and 443 should be opened to the public internet. Database, Redis, NATS, Milvus, etcd, and object storage endpoints remain cluster-internal.

## Helm Scope

Add a demo values file:

- `helm/values-demo.yaml`

The demo values file should contain non-secret environment-specific defaults:

- image repositories and tags
- replica counts
- resource requests and limits
- domain placeholder
- Ingress class and TLS issuer names
- persistence sizes
- dependency enable flags
- public demo feature toggles where needed

Extend templates so the chart can render the full demo deployment:

- backend ConfigMap
- backend Secret references
- Ingress
- optional HPA
- optional PDB
- optional NetworkPolicy
- optional ServiceMonitor when Prometheus Operator exists
- persistence/dependency configuration or explicit dependency integration

Secrets must not be committed. The chart should reference a pre-created secret by name, with a documented command to create it.

## Dependency Strategy

For the first demo pass, keep dependencies inside the K3s cluster to minimize monthly cost:

- PostgreSQL: in-cluster, persistent volume
- Redis: in-cluster, persistent volume if needed
- NATS: in-cluster with JetStream storage
- Milvus: standalone mode for demo

The Helm design should keep an escape hatch for managed services:

- `database.external`
- `redis.external`
- `nats.external` if needed later
- `milvus.external` if moved to a managed or separate vector service later

The immediate demo should not require managed RDS or managed Redis.

## Security Boundaries

The demo must still keep basic production hygiene:

- HTTPS is required for public access.
- No real secrets are committed.
- Public access is limited to HTTP/HTTPS ingress.
- Backend pods read sensitive values from Kubernetes Secrets.
- Database and internal services use ClusterIP only.
- Resource requests and limits are set for every workload.
- Liveness, readiness, and startup probes are defined where the service supports them.

NetworkPolicy can be added behind a feature flag and defaulted off for the first demo if the selected CNI does not enforce it reliably.

## Scripts And Documentation

Add scripts for repeatable setup:

- `scripts/bootstrap-k3s.sh`: install K3s prerequisites, K3s, Helm, cert-manager, and confirm cluster readiness.
- `scripts/deploy-demo.sh`: run Helm lint/template checks, then `helm upgrade --install` with `values-demo.yaml`.

Add operator documentation:

- `docs/deployment/k3s-demo.md`

The document should cover:

- cloud host baseline
- DNS setup
- firewall/security group ports
- K3s bootstrap
- secret creation
- image repository setup
- deployment command
- rollout verification
- smoke tests
- backup and restore notes
- known demo limitations

## Validation

The implementation is complete when these checks pass locally where possible:

- `helm lint ./helm`
- `helm template stratum ./helm -f helm/values-demo.yaml`
- YAML renders without missing values or invalid templates
- Kubernetes manifests do not include real secrets
- deployment documentation has a full path from fresh host to public HTTPS demo

On a real demo host, the acceptance checks are:

- namespace exists
- cert-manager is ready
- Helm release installs successfully
- frontend and backend pods become ready
- `/health` succeeds through the public domain
- frontend loads through HTTPS
- `/api/*` routes reach the backend through frontend nginx or Ingress as designed

## Non-Goals

This design does not include:

- high availability
- multi-node scheduling
- managed database migration
- production backup automation beyond documented manual backup commands
- GitOps with Argo CD
- service mesh
- canary releases
- full observability stack installation

Those can be added after the public demo baseline is reliable.

## Upgrade Path

The demo deployment should leave room for these future upgrades:

- replace in-cluster PostgreSQL and Redis with managed services
- move from single-node K3s to managed Kubernetes
- add Argo CD for GitOps
- enable HPA and PDB when there is more than one node
- enable NetworkPolicy after validating CNI behavior
- add object storage and automated backups
- split Milvus into a dedicated managed or separately provisioned service if memory pressure becomes a problem
