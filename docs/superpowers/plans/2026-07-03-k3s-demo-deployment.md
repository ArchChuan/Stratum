# K3s Demo Deployment Implementation Plan

> **Historical implementation record (implemented).** The current deployment contract lives in `helm/`, `.github/workflows/deploy.yml`, `scripts/bootstrap-k3s.sh`, `scripts/deploy-demo.sh`, and `docs/deployment/k3s-demo.md`. Unchecked boxes below preserve the original planning record and are not a current backlog.
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Stratum deployable as a low-cost public demo on a single-node K3s host through Helm.

**Architecture:** Helm becomes the only application deployment surface for the demo path. `values-demo.yaml` defines non-secret demo defaults, templates render frontend/backend ingress and configuration, and scripts document repeatable bootstrap/deploy commands for a single K3s host.

**Tech Stack:** K3s, Helm v3, Traefik Ingress, cert-manager, Kubernetes Secrets, PostgreSQL, Redis, NATS JetStream, Milvus standalone.

---

## File Structure

- Create `helm/values-demo.yaml`: demo environment values for images, ingress, secrets, resources, and in-cluster dependencies.
- Create `helm/templates/configmap.yaml`: backend non-secret environment variables.
- Create `helm/templates/secret.yaml`: optional non-production secret creation path; default should reference an existing secret.
- Create `helm/templates/ingress.yaml`: public HTTPS ingress for frontend and optional backend route.
- Create `helm/templates/hpa.yaml`: optional backend HorizontalPodAutoscaler, disabled for single-node demo by default.
- Create `helm/templates/pdb.yaml`: optional backend PodDisruptionBudget, disabled for single-node demo by default.
- Create `helm/templates/networkpolicy.yaml`: optional policy scaffold, disabled by default.
- Modify `helm/templates/deployment.yaml`: consume ConfigMap/Secret values, support database/Redis variables, add startup probe, and use demo-safe security context defaults.
- Modify `helm/templates/frontend-configmap.yaml`: keep `/api/` reverse proxy and make proxy timeouts configurable.
- Modify `helm/templates/frontend-deployment.yaml`: add security context and configurable probes where needed.
- Modify `helm/values.yaml`: add shared defaults for values consumed by new templates.
- Create `scripts/bootstrap-k3s.sh`: install K3s/Helm/cert-manager prerequisites and print next steps.
- Create `scripts/deploy-demo.sh`: run `helm lint`, `helm template`, then `helm upgrade --install`.
- Create `docs/deployment/k3s-demo.md`: operator guide from cloud host to HTTPS demo.

## Task 1: Helm Values Baseline

**Files:**

- Modify: `helm/values.yaml`
- Create: `helm/values-demo.yaml`

- [ ] **Step 1: Add shared defaults to `helm/values.yaml`**

Add these sections without removing existing values:

```yaml
nameOverride: ""
fullnameOverride: ""

imagePullSecrets: []

podAnnotations: {}

podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1000
  fsGroup: 1000

securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: false
  capabilities:
    drop:
      - ALL

app:
  startupProbe:
    enabled: true
    failureThreshold: 30
    periodSeconds: 5
    timeoutSeconds: 3

config:
  logLevel: "info"
  environment: "demo"
  otelServiceName: "stratum-ai"
  otelExporterType: "otlp"
  otelSamplingRatio: "0.1"
  postgresDsn: ""
  redisAddr: ""

secrets:
  create: false
  name: "stratum-secrets"
  data: {}

database:
  external: false
  host: "stratum-postgresql"
  port: 5432
  name: "stratum"
  user: "stratum"

redis:
  external: false
  host: "stratum-redis"
  port: 6379

autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 3
  targetCPUUtilizationPercentage: 70
  targetMemoryUtilizationPercentage: 80

podDisruptionBudget:
  enabled: false
  minAvailable: 1

networkPolicy:
  enabled: false

serviceMonitor:
  enabled: false

frontend:
  proxy:
    readTimeout: "60s"
    sendTimeout: "60s"
```

- [ ] **Step 2: Create `helm/values-demo.yaml`**

Use this exact initial content:

```yaml
global:
  imagePullPolicy: IfNotPresent

app:
  replicaCount: 1
  image:
    repository: registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-backend
    tag: demo
  service:
    type: ClusterIP
    port: 80
  resources:
    requests:
      memory: "512Mi"
      cpu: "250m"
    limits:
      memory: "1Gi"
      cpu: "1000m"

frontend:
  enabled: true
  replicaCount: 1
  image:
    repository: registry.cn-hangzhou.aliyuncs.com/stratum-demo/stratum-frontend
    tag: demo
  service:
    type: ClusterIP
    port: 80
  backendServiceName: stratum
  backendServicePort: 80
  resources:
    requests:
      cpu: "50m"
      memory: "64Mi"
    limits:
      cpu: "200m"
      memory: "128Mi"

config:
  port: "8080"
  logLevel: "info"
  environment: "demo"
  natsUrl: "nats://stratum-nats:4222"
  milvusHost: "stratum-milvus"
  milvusPort: "19530"
  otelCollectorEndpoint: "http://stratum-otel-collector:4317"
  otelServiceName: "stratum-ai"
  otelExporterType: "otlp"
  otelSamplingRatio: "0.1"

secrets:
  create: false
  name: "stratum-secrets"

database:
  external: false
  host: "stratum-postgresql"
  port: 5432
  name: "stratum"
  user: "stratum"

redis:
  external: false
  host: "stratum-redis"
  port: 6379

nats:
  enabled: true

milvus:
  enabled: true

observability:
  enabled: false
  otelCollectorEndpoint: "http://stratum-otel-collector:4317"

persistence:
  enabled: true
  storageClass: "local-path"
  size: 10Gi

ingress:
  enabled: true
  className: "traefik"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    traefik.ingress.kubernetes.io/router.entrypoints: "websecure"
    traefik.ingress.kubernetes.io/router.tls: "true"
  hosts:
    - host: demo.stratum.example
      paths:
        - path: /
          pathType: Prefix
          service: frontend
  tls:
    - secretName: stratum-demo-tls
      hosts:
        - demo.stratum.example

autoscaling:
  enabled: false

podDisruptionBudget:
  enabled: false

networkPolicy:
  enabled: false

serviceMonitor:
  enabled: false
```

- [ ] **Step 3: Render to expose missing templates**

Run:

```bash
helm template stratum ./helm -f helm/values-demo.yaml
```

Expected: FAIL before later tasks if templates reference missing fields, or PASS with no Ingress/ConfigMap until those templates are added.

- [ ] **Step 4: Commit values baseline**

```bash
git add helm/values.yaml helm/values-demo.yaml
git commit -m "feat(deployment): add demo helm values baseline"
```

## Task 2: Backend Configuration And Secret References

**Files:**

- Create: `helm/templates/configmap.yaml`
- Create: `helm/templates/secret.yaml`
- Modify: `helm/templates/deployment.yaml`

- [ ] **Step 1: Create backend ConfigMap template**

Create `helm/templates/configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "stratum.fullname" . }}-config
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
data:
  PORT: {{ .Values.config.port | quote }}
  LOG_LEVEL: {{ .Values.config.logLevel | quote }}
  ENVIRONMENT: {{ .Values.config.environment | quote }}
  NATS_URL: {{ .Values.config.natsUrl | quote }}
  MILVUS_HOST: {{ .Values.config.milvusHost | quote }}
  MILVUS_PORT: {{ .Values.config.milvusPort | quote }}
  OTEL_EXPORTER_OTLP_ENDPOINT: {{ .Values.observability.otelCollectorEndpoint | quote }}
  OTEL_SERVICE_NAME: {{ .Values.config.otelServiceName | quote }}
  OTEL_EXPORTER_TYPE: {{ .Values.config.otelExporterType | quote }}
  OTEL_SAMPLING_RATIO: {{ .Values.config.otelSamplingRatio | quote }}
  POSTGRES_HOST: {{ .Values.database.host | quote }}
  POSTGRES_PORT: {{ .Values.database.port | quote }}
  POSTGRES_DB: {{ .Values.database.name | quote }}
  POSTGRES_USER: {{ .Values.database.user | quote }}
  REDIS_ADDR: {{ printf "%s:%v" .Values.redis.host .Values.redis.port | quote }}
```

- [ ] **Step 2: Create optional Secret template**

Create `helm/templates/secret.yaml`:

```yaml
{{- if .Values.secrets.create }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ .Values.secrets.name | quote }}
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
type: Opaque
stringData:
  {{- range $key, $value := .Values.secrets.data }}
  {{ $key }}: {{ $value | quote }}
  {{- end }}
{{- end }}
```

- [ ] **Step 3: Update backend env in `helm/templates/deployment.yaml`**

Replace direct `value:` entries for backend config with `configMapKeyRef`, and add secret refs:

```yaml
env:
  - name: PORT
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: PORT
  - name: LOG_LEVEL
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: LOG_LEVEL
  - name: ENVIRONMENT
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: ENVIRONMENT
  - name: NATS_URL
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: NATS_URL
  - name: MILVUS_HOST
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: MILVUS_HOST
  - name: MILVUS_PORT
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: MILVUS_PORT
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    valueFrom:
      configMapKeyRef:
        name: {{ include "stratum.fullname" . }}-config
        key: OTEL_EXPORTER_OTLP_ENDPOINT
  - name: POSTGRES_PASSWORD
    valueFrom:
      secretKeyRef:
        name: {{ .Values.secrets.name | quote }}
        key: POSTGRES_PASSWORD
  - name: OPENAI_API_KEY
    valueFrom:
      secretKeyRef:
        name: {{ .Values.secrets.name | quote }}
        key: OPENAI_API_KEY
        optional: true
  - name: JWT_PRIVATE_KEY
    valueFrom:
      secretKeyRef:
        name: {{ .Values.secrets.name | quote }}
        key: JWT_PRIVATE_KEY
        optional: true
  - name: JWT_PUBLIC_KEY
    valueFrom:
      secretKeyRef:
        name: {{ .Values.secrets.name | quote }}
        key: JWT_PUBLIC_KEY
        optional: true
```

- [ ] **Step 4: Add startup probe**

In the backend container, after `readinessProbe`, add:

```yaml
{{- if .Values.app.startupProbe.enabled }}
startupProbe:
  httpGet:
    path: /health
    port: http
  periodSeconds: {{ .Values.app.startupProbe.periodSeconds }}
  timeoutSeconds: {{ .Values.app.startupProbe.timeoutSeconds }}
  failureThreshold: {{ .Values.app.startupProbe.failureThreshold }}
{{- end }}
```

- [ ] **Step 5: Render and lint**

Run:

```bash
helm lint ./helm
helm template stratum ./helm -f helm/values-demo.yaml
```

Expected: both commands complete without template errors.

- [ ] **Step 6: Commit backend config templates**

```bash
git add helm/templates/configmap.yaml helm/templates/secret.yaml helm/templates/deployment.yaml
git commit -m "feat(deployment): template backend config and secrets"
```

## Task 3: Public Ingress And Frontend Proxy

**Files:**

- Create: `helm/templates/ingress.yaml`
- Modify: `helm/templates/frontend-configmap.yaml`
- Modify: `helm/templates/frontend-deployment.yaml`

- [ ] **Step 1: Create Ingress template**

Create `helm/templates/ingress.yaml`:

```yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "stratum.fullname" . }}
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
  {{- with .Values.ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- if .Values.ingress.className }}
  ingressClassName: {{ .Values.ingress.className | quote }}
  {{- end }}
  {{- if .Values.ingress.tls }}
  tls:
    {{- toYaml .Values.ingress.tls | nindent 4 }}
  {{- end }}
  rules:
    {{- range .Values.ingress.hosts }}
    - host: {{ .host | quote }}
      http:
        paths:
          {{- range .paths }}
          - path: {{ .path | quote }}
            pathType: {{ .pathType | default "Prefix" }}
            backend:
              service:
                name: {{ include "stratum.ingressServiceName" (dict "root" $ "service" (.service | default "frontend")) }}
                port:
                  number: {{ include "stratum.ingressServicePort" (dict "root" $ "service" (.service | default "frontend")) }}
          {{- end }}
    {{- end }}
{{- end }}
```

- [ ] **Step 2: Add helper functions**

Append to `helm/templates/_helpers.tpl`:

```gotemplate
{{/*
Resolve ingress service names.
*/}}
{{- define "stratum.ingressServiceName" -}}
{{- $root := .root -}}
{{- if eq .service "backend" -}}
{{ include "stratum.fullname" $root }}
{{- else -}}
{{ include "stratum.fullname" $root }}-frontend
{{- end -}}
{{- end }}

{{/*
Resolve ingress service ports.
*/}}
{{- define "stratum.ingressServicePort" -}}
{{- $root := .root -}}
{{- if eq .service "backend" -}}
{{ $root.Values.app.service.port }}
{{- else -}}
{{ $root.Values.frontend.service.port }}
{{- end -}}
{{- end }}
```

- [ ] **Step 3: Make frontend proxy timeouts configurable**

In `helm/templates/frontend-configmap.yaml`, replace the hard-coded proxy timeout line with:

```nginx
            proxy_read_timeout {{ .Values.frontend.proxy.readTimeout }};
            proxy_send_timeout {{ .Values.frontend.proxy.sendTimeout }};
```

- [ ] **Step 4: Add frontend security context**

In `helm/templates/frontend-deployment.yaml`, under the frontend container, add:

```yaml
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
```

Do not set `readOnlyRootFilesystem: true` for nginx until the image has writable temp/cache paths configured.

- [ ] **Step 5: Render Ingress**

Run:

```bash
helm template stratum ./helm -f helm/values-demo.yaml | sed -n '/kind: Ingress/,+60p'
```

Expected: rendered Ingress routes `demo.stratum.example` to `stratum-frontend` on port `80` and includes TLS secret `stratum-demo-tls`.

- [ ] **Step 6: Commit ingress and frontend proxy updates**

```bash
git add helm/templates/ingress.yaml helm/templates/_helpers.tpl helm/templates/frontend-configmap.yaml helm/templates/frontend-deployment.yaml
git commit -m "feat(deployment): add demo ingress template"
```

## Task 4: Optional Scaling, Disruption, And Network Policy Templates

**Files:**

- Create: `helm/templates/hpa.yaml`
- Create: `helm/templates/pdb.yaml`
- Create: `helm/templates/networkpolicy.yaml`

- [ ] **Step 1: Create HPA template**

Create `helm/templates/hpa.yaml`:

```yaml
{{- if .Values.autoscaling.enabled }}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ include "stratum.fullname" . }}
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ include "stratum.fullname" . }}
  minReplicas: {{ .Values.autoscaling.minReplicas }}
  maxReplicas: {{ .Values.autoscaling.maxReplicas }}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetCPUUtilizationPercentage }}
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetMemoryUtilizationPercentage }}
{{- end }}
```

- [ ] **Step 2: Create PDB template**

Create `helm/templates/pdb.yaml`:

```yaml
{{- if .Values.podDisruptionBudget.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "stratum.fullname" . }}
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
spec:
  minAvailable: {{ .Values.podDisruptionBudget.minAvailable }}
  selector:
    matchLabels:
      {{- include "stratum.selectorLabels" . | nindent 6 }}
{{- end }}
```

- [ ] **Step 3: Create NetworkPolicy template**

Create `helm/templates/networkpolicy.yaml`:

```yaml
{{- if .Values.networkPolicy.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "stratum.fullname" . }}
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      {{- include "stratum.selectorLabels" . | nindent 6 }}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: {{ .Values.config.port }}
  egress:
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    - to:
        - podSelector: {}
      ports:
        - protocol: TCP
          port: 4222
        - protocol: TCP
          port: 19530
        - protocol: TCP
          port: 5432
        - protocol: TCP
          port: 6379
        - protocol: TCP
          port: 4317
{{- end }}
```

- [ ] **Step 4: Verify disabled-by-default behavior**

Run:

```bash
helm template stratum ./helm -f helm/values-demo.yaml | rg "HorizontalPodAutoscaler|PodDisruptionBudget|NetworkPolicy"
```

Expected: no matches because demo values disable these resources.

- [ ] **Step 5: Verify enabled rendering**

Run:

```bash
helm template stratum ./helm -f helm/values-demo.yaml \
  --set autoscaling.enabled=true \
  --set podDisruptionBudget.enabled=true \
  --set networkPolicy.enabled=true \
  | rg "HorizontalPodAutoscaler|PodDisruptionBudget|NetworkPolicy"
```

Expected: all three resource kinds are rendered.

- [ ] **Step 6: Commit optional policy templates**

```bash
git add helm/templates/hpa.yaml helm/templates/pdb.yaml helm/templates/networkpolicy.yaml
git commit -m "feat(deployment): add optional demo policy templates"
```

## Task 5: Bootstrap And Deploy Scripts

**Files:**

- Create: `scripts/bootstrap-k3s.sh`
- Create: `scripts/deploy-demo.sh`

- [ ] **Step 1: Create K3s bootstrap script**

Create `scripts/bootstrap-k3s.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root on the demo host" >&2
  exit 1
fi

export INSTALL_K3S_EXEC="${INSTALL_K3S_EXEC:---write-kubeconfig-mode 644}"

if ! command -v k3s >/dev/null 2>&1; then
  curl -sfL https://get.k3s.io | sh -
fi

if ! command -v helm >/dev/null 2>&1; then
  curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
fi

kubectl get nodes

if ! kubectl get namespace cert-manager >/dev/null 2>&1; then
  kubectl create namespace cert-manager
fi

helm repo add jetstack https://charts.jetstack.io
helm repo update
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --set crds.enabled=true \
  --wait \
  --timeout 5m

cat <<'YAML' | kubectl apply -f -
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    email: admin@example.com
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
      - http01:
          ingress:
            class: traefik
YAML

echo "K3s bootstrap complete. Replace admin@example.com in the ClusterIssuer before production use."
```

- [ ] **Step 2: Create demo deployment script**

Create `scripts/deploy-demo.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_NAME="${RELEASE_NAME:-stratum}"
NAMESPACE="${NAMESPACE:-stratum}"
VALUES_FILE="${VALUES_FILE:-${ROOT_DIR}/helm/values-demo.yaml}"

cd "${ROOT_DIR}"

helm lint ./helm
helm template "${RELEASE_NAME}" ./helm -f "${VALUES_FILE}" >/tmp/stratum-demo-rendered.yaml

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

helm upgrade --install "${RELEASE_NAME}" ./helm \
  --namespace "${NAMESPACE}" \
  -f "${VALUES_FILE}" \
  --wait \
  --timeout 10m

kubectl rollout status deployment/"${RELEASE_NAME}" -n "${NAMESPACE}" --timeout=180s
kubectl rollout status deployment/"${RELEASE_NAME}"-frontend -n "${NAMESPACE}" --timeout=180s
```

- [ ] **Step 3: Make scripts executable**

Run:

```bash
chmod +x scripts/bootstrap-k3s.sh scripts/deploy-demo.sh
```

- [ ] **Step 4: Run shell syntax checks**

Run:

```bash
bash -n scripts/bootstrap-k3s.sh
bash -n scripts/deploy-demo.sh
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit scripts**

```bash
git add scripts/bootstrap-k3s.sh scripts/deploy-demo.sh
git commit -m "feat(deployment): add k3s demo scripts"
```

## Task 6: Operator Documentation

**Files:**

- Create: `docs/deployment/k3s-demo.md`

- [ ] **Step 1: Create deployment guide**

Create `docs/deployment/k3s-demo.md`:

```markdown
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

- TCP 22 for SSH from your IP
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
kubectl create namespace stratum --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic stratum-secrets \
  -n stratum \
  --from-literal=POSTGRES_PASSWORD="${POSTGRES_PASSWORD_VALUE}" \
  --from-literal=OPENAI_API_KEY="${OPENAI_API_KEY_VALUE}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Do not commit generated secret YAML.

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
- `ingress.hosts[0].host`
- `ingress.tls[0].hosts[0]`

## Deploy

```bash
VALUES_FILE=/tmp/stratum-values-demo.yaml scripts/deploy-demo.sh
```

## Verify

```bash
kubectl get pods -n stratum
kubectl get ingress -n stratum
kubectl get certificate -n stratum
curl -I https://demo.stratum.example/
curl -fsS https://demo.stratum.example/api/health
```

## Known Demo Limits

- The deployment is not high availability.
- In-cluster storage depends on the single host disk.
- Milvus may require lowering memory pressure or moving to a larger host.
- HPA and PDB are disabled by default because there is only one node.
- NetworkPolicy is disabled by default until the selected CNI behavior is verified.

```

- [ ] **Step 2: Check docs for forbidden secret examples**

Run:

```bash
rg -n "sk-|AKIA|BEGIN PRIVATE KEY|password:|api_key:" docs/deployment/k3s-demo.md helm/values-demo.yaml
```

Expected: no matches.

- [ ] **Step 3: Commit docs**

```bash
git add docs/deployment/k3s-demo.md
git commit -m "docs(deployment): document k3s demo deployment"
```

## Task 7: Final Helm Verification

**Files:**

- Verify only; no expected file edits.

- [ ] **Step 1: Run Helm lint**

Run:

```bash
helm lint ./helm
```

Expected: `1 chart(s) linted, 0 chart(s) failed`.

- [ ] **Step 2: Render demo manifests**

Run:

```bash
helm template stratum ./helm -f helm/values-demo.yaml >/tmp/stratum-demo-rendered.yaml
```

Expected: command exits 0.

- [ ] **Step 3: Confirm public exposure is limited to Ingress**

Run:

```bash
rg -n "type: LoadBalancer|type: NodePort" /tmp/stratum-demo-rendered.yaml
```

Expected: no matches.

- [ ] **Step 4: Confirm no committed real secrets render by default**

Run:

```bash
rg -n "OPENAI_API_KEY:|POSTGRES_PASSWORD:|JWT_PRIVATE_KEY:" /tmp/stratum-demo-rendered.yaml
```

Expected: no `stringData` secret values because `secrets.create=false`; env secret references may appear.

- [ ] **Step 5: Confirm Ingress renders TLS**

Run:

```bash
rg -n "kind: Ingress|secretName: stratum-demo-tls|demo.stratum.example" /tmp/stratum-demo-rendered.yaml
```

Expected: all three strings are present.

- [ ] **Step 6: Commit any verification fixes**

If previous steps required fixes, commit only those files:

```bash
git status --short
git add helm docs/deployment scripts
git commit -m "fix(deployment): complete k3s demo helm rendering"
```

If no fixes were needed, do not create an empty commit.
