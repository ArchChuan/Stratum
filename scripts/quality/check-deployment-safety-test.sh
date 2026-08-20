#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/deploy.yml"
CI_WORKFLOW="${ROOT}/.github/workflows/ci.yml"
HELM_DEPLOYMENT="${ROOT}/helm/templates/deployment.yaml"
HELM_CONFIGMAP="${ROOT}/helm/templates/configmap.yaml"
PROD_VALUES="${ROOT}/helm/values-prod.yaml"
DEMO_VALUES="${ROOT}/helm/values-demo.yaml"
DEMO_LOCAL_VALUES="${ROOT}/helm/values-demo-local.yaml"
REMOTE_HTTP_VALUES="${ROOT}/helm/values-demo-remote-http.yaml"
POSTGRES_DOCKERFILE="${ROOT}/docker/postgres-zhparser.Dockerfile"
OPIK_VALUES="${ROOT}/helm/opik/values-demo.yaml"
OPIK_COLLECTOR="${ROOT}/k8s/opik-otel-collector.yaml"
PLATFORM_ASSISTANT_REMOTE_VERIFY="${ROOT}/scripts/e2e/platform-assistant-remote-verify.sh"
FEISHU_ADAPTER_MANIFEST="${ROOT}/monitoring/remote/resources/feishu-alert-adapter.yaml"
FEISHU_ADAPTER_DOCKERFILE="${ROOT}/docker/feishu-alert-adapter.Dockerfile"
REMOTE_MONITORING_DEPLOY="${ROOT}/scripts/deploy-remote-monitoring.sh"
RECONCILE_WORKFLOW="${ROOT}/.github/workflows/reconcile-monitoring.yml"

require() {
    local pattern="$1" description="$2"
    if ! grep -Eq -- "${pattern}" "${WORKFLOW}"; then
        echo "deployment safety contract missing: ${description}" >&2
        exit 1
    fi
}

require_job() {
    local job="$1" pattern="$2" description="$3" job_block
    job_block=$(sed -n "/^  ${job}:/,/^  [[:alnum:]_-]\+:$/p" "${WORKFLOW}")
    if ! grep -Eq -- "${pattern}" <<<"${job_block}"; then
        echo "deployment safety contract missing: ${description}" >&2
        exit 1
    fi
}

reject() {
    local pattern="$1" description="$2"
    if grep -Eq -- "${pattern}" "${WORKFLOW}"; then
        echo "deployment safety contract violated: ${description}" >&2
        exit 1
    fi
}

require_file() {
    local file="$1" pattern="$2" description="$3"
    if ! grep -Eq -- "${pattern}" "${file}"; then
        echo "deployment safety contract missing: ${description}" >&2
        exit 1
    fi
}

reject_file() {
    local file="$1" pattern="$2" description="$3"
    if grep -Eq -- "${pattern}" "${file}"; then
        echo "deployment safety contract violated: ${description}" >&2
        exit 1
    fi
}

require 'group:[[:space:]]*stratum-production' 'fixed production concurrency group'
require 'cancel-in-progress:[[:space:]]*false' 'non-cancelling active deployment'
require '^  build-backend:' 'parallel backend image build job'
require '^  build-frontend:' 'parallel frontend image build job'
require '^  build-feishu-adapter:' 'parallel Feishu adapter image build job'
require 'needs:[[:space:]]*\[candidate,[[:space:]]*build-backend,[[:space:]]*build-frontend,[[:space:]]*build-feishu-adapter\]' \
    'image build fan-in dependencies'

# Test dependency is satisfied at workflow level via workflow_run (deploy waits for CI)
# or at job level via needs:test; guard accepts whichever pattern is present.
has_workflow_run_ci=false
if grep -Eq 'workflow_run:' "${WORKFLOW}" && grep -Eq 'workflows:[[:space:]]*\[CI\]' "${WORKFLOW}"; then
    has_workflow_run_ci=true
    require 'WORKFLOW_CONCLUSION:[[:space:]]*\$\{\{ github\.event\.workflow_run\.conclusion \}\}' \
        'workflow_run conclusion binding'
    require '\$WORKFLOW_CONCLUSION.*==.*success' 'workflow_run CI success gate'
fi

for job_scope in build-backend:backend build-feishu-adapter:feishu-alert-adapter; do
    job=${job_scope%%:*}
    scope=${job_scope#*:}
    if [[ "${has_workflow_run_ci}" != "true" ]]; then
        require_job "${job}" '^    needs:[[:space:]]*test$' "${job} test dependency"
    fi
    require_job "${job}" "cache-from:[[:space:]]*type=gha,scope=${scope}" "${scope} cache import scope"
    require_job "${job}" "cache-to:[[:space:]]*type=gha,scope=${scope},mode=max" "${scope} cache export scope"
done
if [[ "${has_workflow_run_ci}" != "true" ]]; then
    require_job build-frontend '^    needs:[[:space:]]*test$' 'frontend test dependency'
fi
require_job build-frontend 'no-cache:[[:space:]]*true' 'frontend GHA cache disabled (BuildKit bug)'
require 'digest:[[:space:]]*\$\{\{ steps\.adapter-build\.outputs\.digest \}\}' \
    'adapter build digest job output'
require 'adapter-digest:[[:space:]]*\$\{\{ needs\.build-feishu-adapter\.outputs\.digest \}\}' \
    'adapter digest fan-in output'
require 'file:[[:space:]]*\./docker/feishu-alert-adapter\.Dockerfile' 'adapter image build missing'
require 'FEISHU_WEBHOOK_URL' 'Feishu secret injection missing'
require 'kubectl create namespace monitoring --dry-run=client -o yaml \| kubectl apply -f -' \
    'monitoring namespace idempotent apply missing'
require 'kubectl create secret generic stratum-monitoring-secrets' 'monitoring secret creation missing'
require 'FEISHU_WEBHOOK_URL:[[:space:]]*\$\{\{ secrets\.FEISHU_WEBHOOK_URL \}\}' \
    'Feishu webhook is not passed through the step environment'
require 'test -n "\$FEISHU_WEBHOOK_URL"' 'Feishu webhook presence validation missing'
require '--from-literal=FEISHU_WEBHOOK_URL="\$FEISHU_WEBHOOK_URL"' \
    'monitoring secret does not use the validated environment variable'
require 'kubectl create secret docker-registry aliyun-registry' 'monitoring registry pull secret creation missing'
require 'adapter_digest="\$\{\{ needs\.build-and-push\.outputs\.adapter-digest \}\}"' \
    'adapter digest is not consumed from the build job'
require '\$adapter_digest.*\^sha256:\[0-9a-f\]\{64\}\$' 'adapter digest validation missing'
require 'feishu_adapter=\$\(kubectl get deployment stratum-feishu-alert-adapter -n monitoring' \
    'deployment receipt does not read the deployed Feishu adapter image'
require 'images:\{backend:\$backend,frontend:\$frontend,feishu_adapter:\$feishu_adapter\}' \
    'deployment receipt does not bind the Feishu adapter image'

# Monitoring reconciliation runs from reconcile-monitoring.yml (after deploy via
# workflow_run); the adapter image it consumes must be immutable-digest pinned.
require_file "${RECONCILE_WORKFLOW}" 'adapter_image="\$\(kubectl get deployment stratum-feishu-alert-adapter -n monitoring' \
    'reconciliation does not read the deployed Feishu adapter image first'
require_file "${RECONCILE_WORKFLOW}" 'adapter_image.*@sha256:\[0-9a-f\]\{64\}\$' \
    'reconciliation does not resolve an immutable adapter digest'
require_file "${RECONCILE_WORKFLOW}" 'FEISHU_ADAPTER_IMAGE=\$adapter_image' \
    'adapter immutable digest is not passed to monitoring reconciliation'
require_file "${RECONCILE_WORKFLOW}" 'no deployed Feishu adapter found and no workflow_run context' \
    'reconciliation does not fail closed without an adapter digest'
require_file "${RECONCILE_WORKFLOW}" 'PATH="\$validator_dir:\$PATH" bash scripts/deploy-remote-monitoring\.sh' \
    'safe monitoring reconciliation entrypoint missing'
require_file "${RECONCILE_WORKFLOW}" 'prom/prometheus:v3\.8\.1' \
    'pinned Prometheus validation tool image missing'
require_file "${RECONCILE_WORKFLOW}" 'prom/alertmanager:v0\.33\.1' \
    'pinned Alertmanager validation tool image missing'
require 'Resolve and verify immutable candidate' 'pre-build deployment candidate gate'
require 'github\.event\.workflow_run\.head_sha' 'workflow-run head SHA binding'
require 'ref:[[:space:]]*\$\{\{ needs\.candidate\.outputs\.sha \}\}' 'candidate-pinned checkout'
require 'api\.github\.com/repos/.*/commits/main' 'fail-closed current main lookup'
require 'sha256:\[0-9a-f\]\{64\}' 'registry digest validation'
require '--set-string app\.image\.digest=' 'backend digest deployment'
require '--set-string frontend\.image\.digest=' 'frontend digest deployment'
require 'opik-2\.1\.32\.tgz' 'versioned Opik Helm chart artifact'
require 'sha256sum --check' 'Opik Helm chart checksum verification'
require 'helm upgrade --install opik /tmp/opik-2\.1\.32\.tgz' 'verified local Opik chart installation'
require 'helm upgrade --install cert-manager jetstack/cert-manager' 'cert-manager release installation'
require '--version v1\.21\.1' 'pinned cert-manager chart version'
require '--set crds\.enabled=true' 'cert-manager CRD installation'
require 'kubectl wait --for=condition=Established crd/certificates\.cert-manager\.io' \
    'Certificate CRD readiness wait'
require 'name:[[:space:]]*stratum-internal-root-bootstrap' 'internal root bootstrap issuer missing'
require 'namespace:[[:space:]]*cert-manager' 'internal CA Secret namespace missing'
require 'isCA:[[:space:]]*true' 'internal root certificate CA constraint missing'
require 'secretName:[[:space:]]*stratum-internal-root-ca' 'internal root CA Secret missing'
require 'name:[[:space:]]*stratum-internal-ca' 'internal workload ClusterIssuer missing'
require 'kubectl wait --for=condition=Ready clusterissuer/stratum-internal-ca' \
    'internal workload ClusterIssuer readiness wait'
require_file "${OPIK_COLLECTOR}" 'opentelemetry-collector-contrib@sha256:[0-9a-f]{64}' \
    'collector image digest pin'
require 'opik-backend\.opik\.svc\.cluster\.local:8080' 'in-cluster Opik API URL'
require 'Apply Opik OTLP collector' 'collector applied before Stratum release'
require 'rollout status deployment/opik-backend' 'Opik backend readiness wait'
reject 'continue-on-error:[[:space:]]*true' 'deployment errors are not suppressed'

for component in database redis nats etcd minio milvus; do
    require "--set-string ${component}\\.image\\.digest=" "${component} digest deployment"
done

require 'metrics-server/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/components\.yaml' \
    'version-pinned metrics-server manifest'
reject 'minio/minio:latest|/minio:latest' 'mutable MinIO latest tag'
reject 'metrics-server/releases/latest' 'mutable metrics-server latest manifest'
reject '\|\|[[:space:]]*true' 'suppressed deployment errors'
reject 'StrictHostKeyChecking=no' 'disabled SSH host verification'
reject 'insecure-skip-tls-verify|certificate-authority-data:/d' 'disabled Kubernetes API verification'

cert_manager_step_line=$(grep -n 'name: Install pinned cert-manager release' "${WORKFLOW}" | cut -d: -f1)
internal_ca_step_line=$(grep -n 'name: Bootstrap internal workload CA' "${WORKFLOW}" | cut -d: -f1)
helm_deploy_step_line=$(grep -n 'name: Helm deploy' "${WORKFLOW}" | cut -d: -f1)
if [[ -z "${cert_manager_step_line}" || -z "${internal_ca_step_line}" || -z "${helm_deploy_step_line}" ||
    ${cert_manager_step_line} -ge ${internal_ca_step_line} ||
    ${internal_ca_step_line} -ge ${helm_deploy_step_line} ]]; then
    echo 'deployment safety contract violated: cert-manager and internal CA must be ready before Stratum Helm deploy' >&2
    exit 1
fi

require_file "${REMOTE_MONITORING_DEPLOY}" '^set -euo pipefail$' \
    'monitoring deployment strict shell mode missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'source .*monitoring/remote/versions\.env' \
    'monitoring deployment version contract missing'
require_file "${REMOTE_MONITORING_DEPLOY}" '^umask 077$' 'private monitoring inventory umask missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'mktemp -d' 'private monitoring inventory directory missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'helm list --all-namespaces --output json' \
    'read-only Helm release inventory missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'helm get values' 'read-only Helm values inventory missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'prometheus-community/kube-prometheus-stack' \
    'pinned kube-prometheus-stack reconciliation missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'prometheus-community/prometheus-blackbox-exporter' \
    'pinned blackbox exporter reconciliation missing'
require_file "${REMOTE_MONITORING_DEPLOY}" '--atomic --wait --timeout 15m' \
    'atomic kube-prometheus-stack wait contract missing'
require_file "${REMOTE_MONITORING_DEPLOY}" '--atomic --wait --timeout 10m' \
    'atomic blackbox exporter wait contract missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'api/v1/targets' 'Prometheus target smoke check missing'
require_file "${REMOTE_MONITORING_DEPLOY}" 'api/v1/rules' 'Prometheus rule-health smoke check missing'
reject_file "${REMOTE_MONITORING_DEPLOY}" \
    'helm uninstall|kubectl delete .*(customresourcedefinition|crd|persistentvolumeclaim|pvc)|k8s/monitoring\.yaml' \
    'monitoring deployment contains destructive or stale operations'

OBSERVABILITY_DEPLOY="${ROOT}/scripts/deploy-observability-logging.sh"
LOGGING_MANIFEST="${ROOT}/k8s/logging.yaml"
require 'bash scripts/deploy-observability-logging\.sh' \
    'observability deploy script is not invoked by the workflow'
require_file "${OBSERVABILITY_DEPLOY}" '^set -euo pipefail$' \
    'observability deploy strict shell mode missing'
require_file "${OBSERVABILITY_DEPLOY}" 'kubectl apply -f .*k8s/logging\.yaml' \
    'logging manifests are not applied by the observability deploy'
require_file "${OBSERVABILITY_DEPLOY}" 'kubectl apply -f .*k8s/tracing\.yaml' \
    'tracing manifests are not applied by the observability deploy'
require_file "${OBSERVABILITY_DEPLOY}" 'checksum/config' \
    'config checksum rollout mechanism missing'
require_file "${OBSERVABILITY_DEPLOY}" 'rollout status daemonset/promtail' \
    'promtail rollout wait missing'
require_file "${OBSERVABILITY_DEPLOY}" 'promtail_files_active_total' \
    'promtail active-file verification missing'
require_file "${OBSERVABILITY_DEPLOY}" 'promtail_sent_bytes_total stayed 0' \
    'Loki push verification missing'
require_file "${LOGGING_MANIFEST}" 'type:[[:space:]]*Recreate' \
    'loki must use Recreate strategy (single-replica boltdb-shipper cannot roll in place)'
require_file "${LOGGING_MANIFEST}" 'mountPath:[[:space:]]*/var/loki/wal' \
    'loki WAL must be isolated per instance'
require_file "${LOGGING_MANIFEST}" 'name:[[:space:]]*wal' \
    'loki WAL emptyDir volume missing'

verify_step_line=$(grep -n 'name: Verify deployment' "${WORKFLOW}" | tail -1 | cut -d: -f1)
if [[ -z "${verify_step_line}" ]]; then
    echo 'deployment safety contract missing: healthy application rollout verification' >&2
    exit 1
fi
# Monitoring reconciliation must run only after the deploy workflow completes.
require_file "${RECONCILE_WORKFLOW}" 'workflow_run:' 'reconciliation workflow_run trigger missing'
require_file "${RECONCILE_WORKFLOW}" 'workflows:[[:space:]]*\[Build and Deploy\]' \
    'reconciliation does not follow the deploy workflow'
require_file "${RECONCILE_WORKFLOW}" 'types:[[:space:]]*\[completed\]' \
    'reconciliation does not await deploy completion'
require_file "${RECONCILE_WORKFLOW}" 'group:[[:space:]]*stratum-production' \
    'reconciliation bypasses the production concurrency group'
require_file "${RECONCILE_WORKFLOW}" 'cancel-in-progress:[[:space:]]*false' \
    'reconciliation cancels an active deployment'

require_file "${FEISHU_ADAPTER_MANIFEST}" 'readOnlyRootFilesystem:[[:space:]]*true' \
    'adapter filesystem hardening missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'automountServiceAccountToken:[[:space:]]*false' \
    'adapter service account token automount is not disabled'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'runAsNonRoot:[[:space:]]*true' 'adapter non-root policy missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'runAsUser:[[:space:]]*65532' 'adapter runtime UID missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'runAsGroup:[[:space:]]*65532' 'adapter runtime GID missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'capabilities:' 'adapter capability policy missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'drop:[[:space:]]*$' 'adapter dropped-capability list missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" '^[[:space:]]+- ALL$' 'adapter does not drop every Linux capability'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'allowPrivilegeEscalation:[[:space:]]*false' \
    'adapter privilege escalation policy missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'seccompProfile:' 'adapter seccomp policy missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'type:[[:space:]]*RuntimeDefault' 'adapter seccomp profile is not RuntimeDefault'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'livenessProbe:' 'adapter liveness probe missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'readinessProbe:' 'adapter readiness probe missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'resources:' 'adapter resource budget missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'terminationGracePeriodSeconds:[[:space:]]*30' \
    'adapter termination grace period missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'type:[[:space:]]*ClusterIP' 'adapter service is not explicitly ClusterIP'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'kind:[[:space:]]*ServiceMonitor' 'adapter scrape contract missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'release:[[:space:]]*kps' 'adapter ServiceMonitor release selector missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'image:[[:space:]]*__FEISHU_ADAPTER_IMAGE__' \
    'adapter manifest image placeholder missing'
require_file "${FEISHU_ADAPTER_MANIFEST}" 'secretKeyRef:' 'adapter webhook Secret reference missing'
require_file "${FEISHU_ADAPTER_DOCKERFILE}" '^FROM .*@sha256:' 'adapter build image is not digest pinned'
reject_file "${FEISHU_ADAPTER_DOCKERFILE}" '^FROM .*:latest' 'adapter uses a mutable latest base image'
require_file "${FEISHU_ADAPTER_DOCKERFILE}" '^FROM scratch$' 'adapter runtime stage is not minimal'
require_file "${FEISHU_ADAPTER_DOCKERFILE}" \
    '^COPY --from=builder /etc/ssl/certs/ca-certificates\.crt /etc/ssl/certs/ca-certificates\.crt$' \
    'adapter runtime CA certificates missing'
require_file "${FEISHU_ADAPTER_DOCKERFILE}" '^USER 65532:65532$' 'adapter image does not run as UID/GID 65532'
require_file "${FEISHU_ADAPTER_DOCKERFILE}" '^EXPOSE 8080$' 'adapter image port contract missing'
require_file "${FEISHU_ADAPTER_DOCKERFILE}" '^ENTRYPOINT \["/feishu-alert-adapter"\]$' \
    'adapter image entrypoint contract missing'

if grep -Eq 'gosec@latest|gosec .*\|\|[[:space:]]*true' "${CI_WORKFLOW}"; then
    echo 'deployment safety contract violated: security scanner is unpinned or non-blocking' >&2
    exit 1
fi
if ! grep -Eq 'gosec@v2\.25\.0' "${CI_WORKFLOW}"; then
    echo 'deployment safety contract violated: gosec version is not compatible with the CI Go toolchain' >&2
    exit 1
fi
if ! grep -Eq 'COVERAGE_TARGET:[[:space:]]*"80"' "${CI_WORKFLOW}" ||
    ! grep -Eq 'COVERAGE_BASELINE:[[:space:]]*"38\.0"' "${CI_WORKFLOW}" ||
    ! grep -Eq '::error::Coverage .*below enforced baseline' "${CI_WORKFLOW}" ||
    ! grep -Eq '^[[:space:]]*exit 1' "${CI_WORKFLOW}"; then
    echo 'deployment safety contract missing: enforced coverage baseline and explicit target' >&2
    exit 1
fi
if grep -Eq 'sslmode=disable' "${HELM_DEPLOYMENT}" "${PROD_VALUES}"; then
    echo 'deployment safety contract violated: production PostgreSQL TLS disabled' >&2
    exit 1
fi
if ! grep -Eq 'checksum/secret:' "${HELM_DEPLOYMENT}"; then
    echo 'deployment safety contract missing: Secret rollout checksum' >&2
    exit 1
fi
if ! grep -Eq 'secrets\.externalChecksum=' "${WORKFLOW}" ||
    ! grep -Eq 'kubectl get secret .*sha256sum' "${WORKFLOW}"; then
    echo 'deployment safety contract missing: external Secret rollout checksum' >&2
    exit 1
fi
require_file "${DEMO_VALUES}" 'frontendUrl:[[:space:]]*"https://' 'HTTPS demo frontend URL'
require_file "${DEMO_VALUES}" 'githubCallbackUrl:[[:space:]]*"https://' 'HTTPS demo OAuth callback URL'
require_file "${DEMO_VALUES}" 'secureCookies:[[:space:]]*"true"' 'HTTPS demo secure cookies'
require_file "${DEMO_VALUES}" 'environment:[[:space:]]*"production"' 'remote demo production app environment'
require_file "${DEMO_VALUES}" 'ginMode:[[:space:]]*"release"' 'remote demo Gin release mode'
require_file "${DEMO_VALUES}" 'router\.entrypoints:[[:space:]]*"websecure"' 'HTTPS demo secure entrypoint'
require_file "${DEMO_VALUES}" '^[[:space:]]+tls:' 'HTTPS demo TLS configuration'

require_file "${DEMO_LOCAL_VALUES}" 'frontendUrl:[[:space:]]*"http://localhost([:/"]|$)' \
    'localhost-only demo frontend URL'
require_file "${DEMO_LOCAL_VALUES}" 'githubCallbackUrl:[[:space:]]*"http://localhost([:/"]|$)' \
    'localhost-only demo OAuth callback URL'
require_file "${DEMO_LOCAL_VALUES}" 'environment:[[:space:]]*"demo"' 'local demo app environment'
require_file "${DEMO_LOCAL_VALUES}" 'ginMode:[[:space:]]*"debug"' 'local demo Gin debug mode'
reject_file "${DEMO_LOCAL_VALUES}" 'http://([0-9]{1,3}\.){3}[0-9]{1,3}' \
    'local demo contains a remote IP URL'

require_file "${REMOTE_HTTP_VALUES}" 'secureCookies:[[:space:]]*"true"' \
    'remote HTTPS profile enables secure cookies'
require_file "${REMOTE_HTTP_VALUES}" 'router\.entrypoints:[[:space:]]*"websecure"' \
    'remote HTTPS profile uses only the TLS websecure entrypoint'
require_file "${REMOTE_HTTP_VALUES}" 'router\.tls:[[:space:]]*"true"' \
    'remote HTTPS profile enables Traefik router TLS'
require_file "${REMOTE_HTTP_VALUES}" 'host:[[:space:]]*""' 'remote HTTPS profile uses a hostless Ingress'
require_file "${REMOTE_HTTP_VALUES}" 'secretName:[[:space:]]*stratum-ingress-tls' \
    'remote HTTPS profile references the stratum-ingress-tls Secret'
reject_file "${REMOTE_HTTP_VALUES}" 'frontendUrl:|githubCallbackUrl:|http://([0-9]{1,3}\.){3}[0-9]{1,3}' \
    'remote HTTPS profile hard-codes its public address'

require_file "${HELM_CONFIGMAP}" 'GIN_MODE:.*config\.ginMode' 'Gin mode ConfigMap entry'
require_file "${HELM_DEPLOYMENT}" 'name:[[:space:]]*APP_ENV' 'application environment injection'
require_file "${HELM_DEPLOYMENT}" 'name:[[:space:]]*GIN_MODE' 'Gin mode injection'

require 'validate-remote-http-base-url\.sh[[:space:]]+"\$PUBLIC_BASE_URL"' \
    'PUBLIC_BASE_URL validation before deployment'
require '-f[[:space:]]+helm/values-demo-remote-http\.yaml' 'remote HTTP Helm overlay deployment'
require '--set-string[[:space:]]+config\.frontendUrl="\$PUBLIC_BASE_URL"' 'public frontend URL injection'
require '--set-string[[:space:]]+config\.githubCallbackUrl="\$PUBLIC_BASE_URL/api/auth/github/callback"' \
    'public OAuth callback URL injection'
require 'kubectl get ingress -n stratum -o wide' 'deployed Ingress diagnostics'
require 'kubectl get endpoints stratum stratum-frontend -n stratum' 'service endpoint diagnostics'
require 'ss -H -ltnp.*sport = :443.*sport = :8443' \
    'host HTTPS edge listener diagnostics'
require 'https://127\.0\.0\.1:8443/api/health' 'host-local Traefik HTTPS health diagnostic'
require '--insecure' 'host-local HTTPS diagnostics tolerate self-signed certificates'
require '--header[[:space:]]+"Host:[[:space:]]*\$PUBLIC_AUTHORITY"[[:space:]]+https://127\.0\.0\.1:8443/api/health' \
    'host-local port 8443 public Host diagnostic'
require 'kubectl get service traefik -n kube-system -o wide' 'Traefik service exposure diagnostics'
require 'kubectl get service traefik -n kube-system -o json' 'Traefik service port mapping diagnostics'
require 'kubectl get deployment traefik -n kube-system -o json' 'Traefik entrypoint argument diagnostics'
require 'svccontroller\.k3s\.cattle\.io/svcname=traefik' 'Traefik ServiceLB diagnostics'
require 'kubectl port-forward service/stratum-frontend 18080:80' 'internal frontend verification tunnel'
require 'http://127\.0\.0\.1:18080/api/health' 'internal frontend health verification'
require '\.status == "ok" and \.service == "Stratum"' \
    'public backend health response contract verification'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--connect-timeout[[:space:]]+[0-9]+' \
    'SCWS download connection timeout'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--max-time[[:space:]]+[0-9]+' 'SCWS download total timeout'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--retry[[:space:]]+[1-9][0-9]*' 'SCWS download finite retries'
require_file "${POSTGRES_DOCKERFILE}" 'curl .*--retry-all-errors' 'SCWS download retry classification'
if [[ ! -f "${OPIK_VALUES}" || ! -f "${OPIK_COLLECTOR}" ]]; then
    echo 'deployment safety contract missing: pinned Opik values or collector manifest' >&2
    exit 1
fi
require_file "${OPIK_VALUES}" 'replicaCount:[[:space:]]*1' 'single-node Opik replicas'
require_file "${OPIK_VALUES}" 'persistence:' 'Opik persistent storage'
require_file "${OPIK_VALUES}" 'resources:' 'Opik resource limits'
require_file "${OPIK_COLLECTOR}" 'receivers:' 'collector OTLP receiver'
require_file "${OPIK_COLLECTOR}" 'otlphttp/opik:' 'collector Opik exporter'
require_file "${OPIK_COLLECTOR}" 'otlp/jaeger:' 'collector Jaeger exporter'
require_file "${OPIK_COLLECTOR}" 'exporters:[[:space:]]*\[otlp/jaeger,[[:space:]]*otlphttp/opik\]' \
    'collector fan-out to Jaeger and Opik'
require_file "${OPIK_COLLECTOR}" 'filter/drop_probes:' 'collector probe trace filter'
for route in readyz livez metrics; do
    require_file "${OPIK_COLLECTOR}" "http\.route.*\\\"/${route}\\\"" "collector drops /${route} traces"
done
require_file "${OPIK_COLLECTOR}" 'file_storage/queue:' 'collector persistent queue storage'
require_file "${OPIK_COLLECTOR}" 'storage:[[:space:]]*file_storage/queue' \
    'collector exporters use persistent queues'
require_file "${OPIK_COLLECTOR}" 'claimName:[[:space:]]*opik-otel-collector-queue' \
    'collector queue uses persistent volume storage'
require_file "${OPIK_COLLECTOR}" 'health_check:' 'collector readiness health check'
require_file "${OPIK_COLLECTOR}" 'image:[[:space:]]*otel/opentelemetry-collector-contrib@sha256:[0-9a-f]{64}' \
    'collector image is digest pinned'

for values_file in "${DEMO_VALUES}" "${DEMO_LOCAL_VALUES}"; do
    for key in opikUrl opikProject opikWorkspace tracePayloadEnabled tracePayloadEndpoint tracePayloadBucket tracePayloadUseTls; do
        if ! grep -Eq "^[[:space:]]+${key}:" "${values_file}"; then
            echo "deployment safety contract missing: ${key} in ${values_file}" >&2
            exit 1
        fi
    done
done

require_file "${DEMO_VALUES}" 'opikUrl:[[:space:]]*"http://opik-backend\.opik\.svc\.cluster\.local:8080"' \
    'Demo uses the in-cluster Opik API'

if [[ ! -f "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" ]]; then
    echo 'deployment safety contract missing: platform assistant remote verifier' >&2
    exit 1
fi
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/health' 'remote public health check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'public_health_contract' \
    'remote public health response contract'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'GUEST_ATTEMPTS=5' 'five bounded guest login attempts'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/agents/stratum-platform-assistant' \
    'managed Agent member read check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'systemPrompt' 'managed Agent prompt omission check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/agents/executions' 'member execution diagnostics check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'deployment/opik-backend' 'Opik backend readiness check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'deployment/opik-otel-collector' 'OTEL collector readiness check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'baseline_projection' 'proposal baseline column check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'edit_count' 'proposal edit count column check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'GROUP BY table_schema' \
    'proposal columns checked per tenant schema'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" '/api/auth/me' 'administrator bearer identity check'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" "role IN \('owner',[[:space:]]*'admin'\)" \
    'aggregate tenant administrator count'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" "table_name='providers'" \
    'aggregate configured provider count'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'stratum_diagnose_tenant' \
    'configured chain diagnostic tool evidence'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'diagnosticReport' \
    'configured chain structured diagnostic report'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'X-Request-ID' \
    'configured chain request trace correlation'
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'trace_id' \
    'configured chain Opik execution correlation'
for state in passed failed prerequisite_missing; do
    require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" "${state}" "configured chain ${state} state"
done
require_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'exit 1' 'failed remote verification exits nonzero'
reject_file "${PLATFORM_ASSISTANT_REMOTE_VERIFY}" 'echo.*(token|password|api[_-]?key)' \
    'remote verifier prints a credential'

if [[ -e "${ROOT}/.github/workflows/mirror.yml" ]]; then
    echo 'deployment safety contract violated: Gitee mirror workflow still exists' >&2
    exit 1
fi

if git -C "${ROOT}" grep -in gitee -- .github docs/deployment >/dev/null 2>&1; then
    echo 'deployment safety contract violated: Gitee references remain' >&2
    exit 1
fi

# Backend runtime image contents must stay in sync between the root Dockerfile
# (local builds via Makefile/docker-compose) and Dockerfile.ci (CD pipeline).
# 历史教训：88bdd632 只改根 Dockerfile、#295 只补 Dockerfile.ci 的二进制，
# db-migration hook 连续部署事故（缺二进制 / 缺 SQL 目录）；任一文件漏同步即失败。
for dockerfile in "${ROOT}/Dockerfile" "${ROOT}/Dockerfile.ci"; do
    require_file "${dockerfile}" 'fix-provider-keys' 'fix-provider-keys hook binary missing'
    require_file "${dockerfile}" 'COPY pkg/migration/sql' 'migration SQL files not copied into image'
    require_file "${dockerfile}" 'tzdata' 'tzdata not installed in image'
    require_file "${dockerfile}" 'chown appuser:appuser server' 'hook binaries not owned by appuser'
done

echo 'deployment safety contract tests passed'
