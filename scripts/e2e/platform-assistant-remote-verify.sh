#!/usr/bin/env bash

set -euo pipefail

GUEST_ATTEMPTS=5
HTTP_TIMEOUT_SEC=15
SSH_TIMEOUT_SEC=15
OPIK_ATTEMPTS=10
OPIK_POLL_DELAY_SEC=2

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
header_file="$work_dir/headers.txt"
touch "$response_file"
touch "$header_file"
chmod 600 "$response_file" "$header_file"

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
            --dump-header "$header_file" --write-out '%{http_code}' "$url"
}

authorized_post_status() {
    local url="$1" bearer="$2" body="$3"
    printf 'header = "Authorization: Bearer %s"\n' "$bearer" |
        curl --config - --silent --show-error --max-time "$HTTP_TIMEOUT_SEC" --output "$response_file" \
            --dump-header "$header_file" --write-out '%{http_code}' --header 'Content-Type: application/json' \
            --data "$body" "$url"
}

status=$(http_status GET "$base_url/api/health") || fail "public_health"
[[ "$status" == "200" ]] || fail "public_health"
jq -e '.status == "ok" and .service == "Stratum"' "$response_file" >/dev/null ||
    fail "public_health_contract"
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

incompatible_schema_count=$(remote_psql \
    "SELECT count(*) FROM public.tenants t LEFT JOIN (SELECT table_schema, count(DISTINCT column_name) FILTER (WHERE column_name IN ('baseline_projection','edit_count')) AS compatible_columns FROM information_schema.columns WHERE table_name='resource_change_proposals' GROUP BY table_schema) c ON c.table_schema='tenant_' || t.id::text WHERE t.deleted_at IS NULL AND COALESCE(c.compatible_columns,0) <> 2;") || fail "proposal_columns"
[[ "$incompatible_schema_count" == "0" ]] || fail "proposal_columns"

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
unset PLATFORM_ASSISTANT_ADMIN_BEARER
if [[ -z "$admin_bearer" ]]; then
    jq -cn '{configuredChain:"prerequisite_missing",missing:["admin_session"]}'
    exit 0
fi

status=$(authorized_get_status "$base_url/api/auth/me" "$admin_bearer") || fail "admin_session"
[[ "$status" == "200" ]] || fail "admin_session"
admin_tenant=$(jq -er '.tenant_id | select(type == "string")' "$response_file") || fail "admin_session"
admin_user=$(jq -er '.sub | select(type == "string")' "$response_file") || fail "admin_session"
admin_role=$(jq -er '.role | select(. == "owner" or . == "admin")' "$response_file") || fail "admin_session"
uuid_pattern='^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
[[ "$admin_tenant" =~ $uuid_pattern && "$admin_user" =~ $uuid_pattern ]] || fail "admin_session"
: >"$response_file"
admin_role=""

configured_identity_count=$(remote_psql \
    "SELECT count(*) FROM public.tenants t JOIN public.tenant_members m ON m.tenant_id=t.id WHERE t.id='${admin_tenant}' AND m.user_id='${admin_user}' AND m.role IN ('owner', 'admin') AND t.deleted_at IS NULL AND jsonb_object_length(COALESCE(t.settings->'llm_api_keys','{}'::jsonb)) > 0;") || fail "configured_identity"
if [[ "$configured_identity_count" != "1" ]]; then
    jq -cn '{configuredChain:"prerequisite_missing",missing:["tenant_admin_provider_pair"]}'
    exit 0
fi

execution_body='{"query":"请调用系统诊断工具检查 Agent 状态，只做只读诊断，不要创建、修改或删除任何资源。","options":{"maxSteps":4}}'
status=$(authorized_post_status "$base_url/api/agents/stratum-platform-assistant/execute" "$admin_bearer" \
    "$execution_body") ||
    fail "configured_chain"
[[ "$status" == "200" ]] || fail "configured_chain"
jq -e '
    (.error == null or .error == "") and
    any(.toolCalls[]?; .ToolName == "stratum_diagnose_tenant") and
    any(.artifacts[]?; .type == "diagnostic_report" and
        (.profileVersion | type == "string" and length > 0) and
        any(.diagnosticReport.steps[]?; .tool == "stratum_diagnose_tenant"))
' "$response_file" >/dev/null || fail "configured_chain"
request_trace=$(awk 'BEGIN{IGNORECASE=1} /^X-Request-ID:/ {gsub("\r", "", $2); print $2}' "$header_file" | tail -1)
[[ "$request_trace" =~ ^[0-9a-f-]{36}$|^[0-9a-f]{32}$ ]] || fail "configured_chain_trace"
: >"$response_file"
: >"$header_file"

opik_observed=false
for ((attempt = 1; attempt <= OPIK_ATTEMPTS; attempt++)); do
    status=$(authorized_get_status "$base_url/api/agents/executions?page=1&pageSize=20" "$admin_bearer") ||
        fail "configured_chain_opik"
    [[ "$status" == "200" ]] || fail "configured_chain_opik"
    if jq -e --arg trace "$request_trace" \
        'any(.executions[]?; .trace_id == $trace and .agent_id == "stratum-platform-assistant")' \
        "$response_file" >/dev/null; then
        opik_observed=true
        break
    fi
    sleep "$OPIK_POLL_DELAY_SEC"
done
admin_bearer=""
admin_tenant=""
admin_user=""
request_trace=""
[[ "$opik_observed" == "true" ]] || fail "configured_chain_opik"
: >"$response_file"
jq -cn '{configuredChain:"passed",missing:[]}'
