# K3s Demo Deployment

This guide deploys Stratum as a public HTTP demo on one cloud host without a
domain name. K3s Traefik exposes the public port 6879 through its `web2`
entrypoint while retaining the standard `web` entrypoint on port 80.

## Host Baseline

Recommended:

- 4 vCPU
- 8 GiB RAM
- 80 GiB SSD
- Ubuntu 22.04 or 24.04
- public IPv4

Open only these public inbound ports:

- TCP 22 for SSH from your operator IP
- TCP 6879 for the public HTTP demo

Do not expose the backend, PostgreSQL, Redis, NATS, Milvus, etcd, or MinIO
ports publicly.

## Public URL

Create the GitHub Production Environment variable `PUBLIC_BASE_URL`:

```text
http://<public-ip>:6879
```

The deployment rejects DNS names, other ports, paths, credentials, and a
trailing slash. The observed production path is:

```text
public <public-ip>:6879 -> K3s ServiceLB 6879 -> Traefik web2 -> hostless Ingress
```

The remote HTTP Ingress also remains attached to `web` so host-local port 80
continues to work. Do not assume that public 6879 is translated to host port 80;
verify the ServiceLB port and entrypoint mapping on the target cluster.

Configure the GitHub OAuth App callback URL to exactly:

```text
http://<public-ip>:6879/api/auth/github/callback
```

Without that external OAuth setting, the frontend and health endpoint can
work while login still fails.

## Bootstrap

Run on the host:

```bash
sudo scripts/bootstrap-k3s.sh
```

The bootstrap script also installs cert-manager for the future HTTPS profile.
The current remote HTTP overlay does not request or use a certificate.

## Secrets

Create the runtime secret in the target namespace:

```bash
export POSTGRES_PASSWORD_VALUE="change-this-demo-postgres-password"
export GITHUB_CLIENT_ID_VALUE="change-this-demo-github-client-id"
export GITHUB_CLIENT_SECRET_VALUE="change-this-demo-github-client-secret"
export JWT_PRIVATE_KEY_PEM_VALUE="$(cat /tmp/stratum-jwt-private-key.pem)"
export MINIO_ROOT_PASSWORD_VALUE="change-this-demo-minio-root-password"
kubectl create namespace stratum --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic stratum-secrets \
  -n stratum \
  --from-literal=POSTGRES_PASSWORD="${POSTGRES_PASSWORD_VALUE}" \
  --from-literal=GITHUB_CLIENT_ID="${GITHUB_CLIENT_ID_VALUE}" \
  --from-literal=GITHUB_CLIENT_SECRET="${GITHUB_CLIENT_SECRET_VALUE}" \
  --from-literal=JWT_PRIVATE_KEY_PEM="${JWT_PRIVATE_KEY_PEM_VALUE}" \
  --from-literal=MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD_VALUE}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Do not commit generated secret YAML.

`GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, and `JWT_PRIVATE_KEY_PEM` are optional for a static frontend smoke test, but login and authenticated demo flows require the GitHub OAuth and JWT values. LLM API keys are configured per tenant after login; the Helm deployment still exposes an optional legacy `OPENAI_API_KEY` reference, but the current backend configuration does not read it.

## Configure Values

The CD workflow renders the HTTPS demo base plus the remote HTTP overlay:

```bash
helm template stratum ./helm \
  -f helm/values-demo.yaml \
  -f helm/values-demo-remote-http.yaml \
  --set-string config.frontendUrl=http://203.0.113.10:6879 \
  --set-string config.githubCallbackUrl=http://203.0.113.10:6879/api/auth/github/callback
```

Do not add the real public IP to either values file. The workflow validates and
injects `PUBLIC_BASE_URL`; image repositories and immutable digests are also
injected by the workflow.

## In-Cluster Dependencies

The demo Helm values deploy these dependencies inside the same K3s namespace:

- PostgreSQL as `stratum-postgresql`
- Redis as `stratum-redis`
- NATS JetStream as `stratum-nats`
- etcd, MinIO, and Milvus standalone for vector search
- Opik 2.1.32 in a separate `opik` namespace (MySQL, Redis, ZooKeeper,
  ClickHouse, and the Opik backend); bundled MinIO is disabled because the
  trace evidence path does not require object storage
- an in-cluster OTLP Collector in `stratum`, forwarding traces to the private
  Opik ingestion endpoint

All dependency Services are `ClusterIP`. Do not expose them through the cloud security group or Ingress.

## Deploy

```bash
helm upgrade --install stratum ./helm \
  -f helm/values-demo.yaml \
  -f helm/values-demo-remote-http.yaml \
  --set-string config.frontendUrl="$PUBLIC_BASE_URL" \
  --set-string config.githubCallbackUrl="$PUBLIC_BASE_URL/api/auth/github/callback" \
  --set-string config.secureCookies=false \
  -n stratum --wait --timeout=10m
```

Normal production deployment runs through `.github/workflows/deploy.yml` so
image digests, secret checksums, the pinned Opik 2.1.32 release, collector
readiness, rollout gates, and public verification are not skipped. The manual
command is for rendering and operator diagnosis; deploy Opik first when
reproducing the workflow manually:

```bash
helm repo add opik https://comet-ml.github.io/opik
helm repo update
helm upgrade --install opik opik/opik --version 2.1.32 \
  --namespace opik --create-namespace \
  -f helm/opik/values-demo.yaml --atomic --wait --timeout=20m
kubectl apply -f k8s/opik-otel-collector.yaml
kubectl rollout status deployment/opik-otel-collector -n stratum --timeout=5m
```

Stratum uses `http://opik-backend.opik.svc.cluster.local:8080` for
diagnostic reads. The collector sends OTLP traces to the corresponding
`/v1/private/otel` endpoint; neither endpoint is published through the
public Ingress.

## Verify

```bash
kubectl get pods -n stratum
kubectl get pods -n opik
kubectl get pvc -n stratum
kubectl get pvc -n opik
kubectl get ingress -n stratum -o wide
kubectl get endpoints stratum stratum-frontend -n stratum
kubectl port-forward service/stratum-frontend 18080:80 -n stratum
curl --fail --silent --show-error --max-time 15 \
  http://127.0.0.1:18080/api/health >/dev/null
curl --fail --silent --show-error --max-time 15 -I "$PUBLIC_BASE_URL/"
curl --fail --silent --show-error --max-time 15 \
  "$PUBLIC_BASE_URL/api/health" >/dev/null

kubectl rollout status deployment/opik-backend -n opik --timeout=10m
kubectl rollout status deployment/opik-otel-collector -n stratum --timeout=5m
```

Run the port-forward in a separate terminal. The internal check isolates the
frontend Service and its backend proxy from the public forwarding and Ingress
path; the public check must independently return HTTP 200.

Complete verification includes a browser GitHub login and callback. Do not log
authorization codes, tokens, cookies, PII, or upstream response bodies.

For the managed platform assistant, run the repository verifier after the
deployment rollout:

```bash
bash scripts/e2e/platform-assistant-remote-verify.sh "$PUBLIC_BASE_URL" root@demo-host
```

The verifier checks the public member boundary, Agent diagnostics, Opik and
collector readiness, proposal schema compatibility, and aggregate execution
prerequisites. Its `configuredChain` result has three states: `passed`,
`failed`, and `prerequisite_missing`. A failed check exits nonzero.
`prerequisite_missing` remains an explicit incomplete result and lists only
missing prerequisite categories. When a legitimate administrator session is
available, supply its bearer value through the process-only
`PLATFORM_ASSISTANT_ADMIN_BEARER` environment variable; never place it in a
command argument, file, or log. The verifier does not promote members, change
tenant settings, or create resources.

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
- HTTP does not encrypt browser traffic or session material in transit. This is
  an accepted constraint for the current host, not a replacement for HTTPS.

## Migrate To HTTPS

After assigning a domain and certificate, stop applying
`values-demo-remote-http.yaml`, set the HTTPS public URLs in the deployment
environment, and use the existing `values-demo.yaml` TLS configuration. Secure
cookies must be restored to `true`; do not disable the deployment safety checks.
