# K3s Demo Deployment

This guide deploys Stratum as a public HTTPS demo on one cloud host.

## Host Baseline

Recommended:

- 4 vCPU
- 8 GiB RAM
- 80 GiB SSD
- Ubuntu 22.04 or 24.04
- public IPv4

Open only these public inbound ports:

- TCP 22 for SSH from your operator IP
- TCP 80 for ACME HTTP-01
- TCP 443 for HTTPS

## DNS

Create an A record:

```text
demo.stratum.example -> 203.0.113.10
```

Use the real domain before requesting a Let's Encrypt certificate.

## Bootstrap

Run on the host:

```bash
sudo scripts/bootstrap-k3s.sh
```

Edit the `letsencrypt-prod` ClusterIssuer email after bootstrap:

```bash
kubectl edit clusterissuer letsencrypt-prod
```

## Secrets

Create the runtime secret in the target namespace:

```bash
export POSTGRES_PASSWORD_VALUE="change-this-demo-postgres-password"
export OPENAI_API_KEY_VALUE="change-this-demo-openai-api-key"
export GITHUB_CLIENT_ID_VALUE="change-this-demo-github-client-id"
export GITHUB_CLIENT_SECRET_VALUE="change-this-demo-github-client-secret"
export JWT_PRIVATE_KEY_PEM_VALUE="$(cat /tmp/stratum-jwt-private-key.pem)"
export MINIO_ROOT_PASSWORD_VALUE="change-this-demo-minio-root-password"
kubectl create namespace stratum --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic stratum-secrets \
  -n stratum \
  --from-literal=POSTGRES_PASSWORD="${POSTGRES_PASSWORD_VALUE}" \
  --from-literal=OPENAI_API_KEY="${OPENAI_API_KEY_VALUE}" \
  --from-literal=GITHUB_CLIENT_ID="${GITHUB_CLIENT_ID_VALUE}" \
  --from-literal=GITHUB_CLIENT_SECRET="${GITHUB_CLIENT_SECRET_VALUE}" \
  --from-literal=JWT_PRIVATE_KEY_PEM="${JWT_PRIVATE_KEY_PEM_VALUE}" \
  --from-literal=MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD_VALUE}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Do not commit generated secret YAML.

`OPENAI_API_KEY`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, and `JWT_PRIVATE_KEY_PEM` are optional for a static frontend smoke test, but login and authenticated demo flows require the GitHub OAuth and JWT values.

## Configure Values

Copy the demo values file and set image repositories, tags, and domain:

```bash
cp helm/values-demo.yaml /tmp/stratum-values-demo.yaml
```

Edit:

- `app.image.repository`
- `app.image.tag`
- `frontend.image.repository`
- `frontend.image.tag`
- `config.frontendUrl`
- `config.githubCallbackUrl`
- `ingress.hosts[0].host`
- `ingress.tls[0].hosts[0]`

## In-Cluster Dependencies

The demo Helm values deploy these dependencies inside the same K3s namespace:

- PostgreSQL as `stratum-postgresql`
- Redis as `stratum-redis`
- NATS JetStream as `stratum-nats`
- etcd, MinIO, and Milvus standalone for vector search

All dependency Services are `ClusterIP`. Do not expose them through the cloud security group or Ingress.

## Deploy

```bash
VALUES_FILE=/tmp/stratum-values-demo.yaml scripts/deploy-demo.sh
```

## Verify

```bash
kubectl get pods -n stratum
kubectl get pvc -n stratum
kubectl get ingress -n stratum
kubectl get certificate -n stratum
curl -I https://demo.stratum.example/
curl -fsS https://demo.stratum.example/api/health
```

## Backup And Restore Notes

Before deleting the host or reinstalling K3s, create a database dump:

```bash
kubectl exec -n stratum deployment/stratum-postgresql -- \
  pg_dump -U stratum stratum > /tmp/stratum-demo-postgres.sql
```

Restore into a fresh demo deployment with:

```bash
kubectl exec -i -n stratum deployment/stratum-postgresql -- \
  psql -U stratum stratum < /tmp/stratum-demo-postgres.sql
```

Milvus, NATS, Redis, etcd, and MinIO use PVCs on the single host. For a demo, preserve the host disk or snapshot the cloud disk before destructive operations.

## Known Demo Limits

- The deployment is not high availability.
- In-cluster storage depends on the single host disk.
- Milvus may require lowering memory pressure or moving to a larger host.
- HPA and PDB are disabled by default because there is only one node.
- NetworkPolicy is disabled by default until the selected CNI behavior is verified.
