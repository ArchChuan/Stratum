#!/usr/bin/env bash

set -euo pipefail

GUEST_ATTEMPTS=5
HTTP_TIMEOUT_SEC=15
SSH_TIMEOUT_SEC=15

usage() {
    echo "usage: $0 <public-base-url> <ssh-target>" >&2
    exit 1
}

fail() {
    printf '{"configuredChain":"failed","check":"%s"}\n' "$1" >&2
    exit 1
}

[[ $# -eq 2 ]] || usage
base_url="${1%/}"
ssh_target="$2"
[[ "$base_url" =~ ^https?://[^/]+$ ]] || fail "public_base_url"
[[ "$ssh_target" =~ ^[A-Za-z0-9._-]+@?[A-Za-z0-9._-]+$ ]] || fail "ssh_target"

work_dir=$(mktemp -d)
chmod 700 "$work_dir"
trap '/usr/bin/rm -rf -- "$work_dir"' EXIT
response_file="$work_dir/response.json"
touch "$response_file"
chmod 600 "$response_file"

http_status() {
    local method="$1" url="$2" body="${3:-}"
    local args=(--silent --show-error --max-time "$HTTP_TIMEOUT_SEC" --output "$response_file"
        --write-out '%{http_code}' --request "$method")
    if [[ -n "$body" ]]; then
        args+=(--header 'Content-Type: application/json' --data "$body")
    fi
    curl "${args[@]}" "$url"
}

authorized_get_status() {
    local url="$1" bearer="$2"
    printf 'header = "Authorization: Bearer %s"\n' "$bearer" |
        curl --config - --silent --show-error --max-time "$HTTP_TIMEOUT_SEC" --output "$response_file" \
            --write-out '%{http_code}' "$url"
}

authorized_post_status() {
    local url="$1" bearer="$2" body="$3"
    printf 'header = "Authorization: Bearer %s"\n' "$bearer" |
        curl --config - --silent --show-error --max-time "$HTTP_TIMEOUT_SEC" --output "$response_file" \
            --write-out '%{http_code}' --header 'Content-Type: application/json' --data "$body" "$url"
}

status=$(http_status GET "$base_url/api/health") || fail "public_health"
[[ "$status" == "200" ]] || fail "public_health"
: >"$response_file"

member_bearer=""
for ((attempt = 1; attempt <= GUEST_ATTEMPTS; attempt++)); do
    status=$(http_status POST "$base_url/api/auth/guest" '{}') || fail "guest_login"
    [[ "$status" == "201" ]] || fail "guest_login"
    member_bearer=$(jq -er '.access_token | select(type == "string" and length > 0)' "$response_file") ||
        fail "guest_login_contract"
    : >"$response_file"
done

status=$(authorized_get_status "$base_url/api/agents/stratum-platform-assistant" "$member_bearer") ||
    fail "managed_agent"
[[ "$status" == "200" ]] || fail "managed_agent"
jq -e '.id == "stratum-platform-assistant" and .systemPrompt == ""' "$response_file" >/dev/null ||
    fail "managed_agent_prompt_boundary"
: >"$response_file"

status=$(authorized_get_status "$base_url/api/agents/executions" "$member_bearer") || fail "agent_executions"
[[ "$status" == "200" ]] || fail "agent_executions"
: >"$response_file"
member_bearer=""

ssh_readonly() {
    ssh -o BatchMode=yes -o ConnectTimeout="$SSH_TIMEOUT_SEC" "$ssh_target" "$1"
}

opik_ready=$(ssh_readonly \
    "kubectl get deployment/opik-backend -n opik -o jsonpath='{.status.readyReplicas}'") || fail "opik_backend"
[[ "$opik_ready" =~ ^[1-9][0-9]*$ ]] || fail "opik_backend"
collector_ready=$(ssh_readonly \
    "kubectl get deployment/opik-otel-collector -n stratum -o jsonpath='{.status.readyReplicas}'") ||
    fail "otel_collector"
[[ "$collector_ready" =~ ^[1-9][0-9]*$ ]] || fail "otel_collector"

remote_psql() {
    local sql="$1"
    ssh_readonly "kubectl exec -n stratum deployment/stratum-postgresql -- psql -U stratum -d stratum -Atqc \"$sql\""
}

column_count=$(remote_psql \
    "SELECT count(DISTINCT column_name) FROM information_schema.columns WHERE table_name='resource_change_proposals' AND column_name IN ('baseline_projection','edit_count');") || fail "proposal_columns"
[[ "$column_count" == "2" ]] || fail "proposal_columns"

admin_count=$(remote_psql \
    "SELECT count(*) FROM public.tenant_members WHERE role IN ('owner', 'admin');") || fail "tenant_admin_aggregate"
[[ "$admin_count" =~ ^[0-9]+$ ]] || fail "tenant_admin_aggregate"
provider_count=$(remote_psql \
    "SELECT count(*) FROM public.tenants t CROSS JOIN LATERAL jsonb_object_keys(COALESCE(t.settings->'llm_api_keys','{}'::jsonb)) provider WHERE t.deleted_at IS NULL;") || fail "tenant_provider_aggregate"
[[ "$provider_count" =~ ^[0-9]+$ ]] || fail "tenant_provider_aggregate"

missing=()
((admin_count > 0)) || missing+=("tenant_admin")
((provider_count > 0)) || missing+=("tenant_provider")
if ((${#missing[@]} > 0)); then
    missing_json=$(printf '%s\n' "${missing[@]}" | jq -Rsc 'split("\n")[:-1]')
    jq -cn --argjson missing "$missing_json" \
        '{configuredChain:"prerequisite_missing",missing:$missing}'
    exit 0
fi

admin_bearer="${PLATFORM_ASSISTANT_ADMIN_BEARER:-}"
if [[ -z "$admin_bearer" ]]; then
    jq -cn '{configuredChain:"prerequisite_missing",missing:["admin_session"]}'
    exit 0
fi

execution_body='{"query":"请只进行只读系统状态诊断，不要创建、修改或删除任何资源。","options":{"maxSteps":4}}'
status=$(authorized_post_status "$base_url/api/agents/stratum-platform-assistant/execute" "$admin_bearer" \
    "$execution_body") ||
    fail "configured_chain"
admin_bearer=""
[[ "$status" == "200" ]] || fail "configured_chain"
jq -e '.error == null or .error == ""' "$response_file" >/dev/null || fail "configured_chain"
: >"$response_file"
jq -cn '{configuredChain:"passed",missing:[]}'
