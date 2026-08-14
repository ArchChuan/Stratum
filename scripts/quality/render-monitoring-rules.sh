#!/usr/bin/env bash
#
# 规则双栈渲染器：从单一规则源 monitoring/remote/rules 渲染两种产物，避免规则双维护。
#
#   remote-test  渲染 PrometheusRule CRD（远端 kps 权威格式），
#                deploy-remote-monitoring.sh 与 guard 共用同一实现。
#   local        渲染本地 standalone 规则（prometheus.yml rule_files），
#                逐文件输出 .yml，environment 由 remote-test 替换为 production，
#                以匹配本地 prometheus.yml external_labels.environment。
#
# 产物 commit 进 git；--check 校验渲染结果与 commit 产物一致，防止漂移。
# 规则源 monitoring/remote/rules/*.yaml 零改动（首行 groups: 是两种格式的公共头）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RULES_SRC="${ROOT}/monitoring/remote/rules"
REMOTE_OUT="${ROOT}/monitoring/remote/generated/stratum-prometheus-rules.yaml"
LOCAL_OUT_DIR="${ROOT}/monitoring/local/rules"

usage() {
    cat >&2 <<EOF
Usage: $(basename "$0") <remote-test|local> [--check] [--to <path>]

  remote-test  渲染 PrometheusRule CRD（默认写 ${REMOTE_OUT}）
  local        渲染本地 standalone 规则，remote-test -> production（默认写 ${LOCAL_OUT_DIR}/）
  --check      渲染到临时位置，与 commit 产物逐字节比对，不一致退出非零
  --to <path>  写产物到指定路径（deploy 用），与 --check 互斥
EOF
    exit 2
}

render_remote() {
    local output="$1"
    {
        printf '%s\n' 'apiVersion: monitoring.coreos.com/v1' 'kind: PrometheusRule' 'metadata:' \
            '  name: stratum-remote-rules' '  namespace: monitoring' '  labels:' '    release: kps' \
            'spec:' '  groups:'
        local rule_file
        for rule_file in "${RULES_SRC}"/*.yaml; do
            # 缩进后清理行尾空格：规则源空行经 sed 加 2 空格前缀会成行尾空格，
            # pre-commit trailing-whitespace 会拒绝；k8s YAML 空行无语义，清理无副作用。
            tail -n +2 "${rule_file}" | sed 's/^/  /' | sed 's/[[:space:]]*$//'
        done
    } >"${output}"
}

render_local() {
    local outdir="$1"
    local rule_file
    for rule_file in "${RULES_SRC}"/*.yaml; do
        sed 's/remote-test/production/g' "${rule_file}" \
            >"${outdir}/$(basename "${rule_file}" .yaml).yml"
    done
}

main() {
    [[ $# -ge 1 ]] || usage
    local target="$1"
    shift
    local check=false
    local to=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --check) check=true ;;
            --to) to="$2"; shift ;;
            *) usage ;;
        esac
        shift
    done
    if [[ "${check}" == true && -n "${to}" ]]; then
        usage
    fi

    case "${target}" in
        remote-test) ;;
        local) ;;
        *) usage ;;
    esac

    if [[ "${check}" == true ]]; then
        if [[ "${target}" == "remote-test" ]]; then
            local tmp
            tmp="$(mktemp)"
            trap 'rm -f "${tmp}"' EXIT
            render_remote "${tmp}"
            if ! git -C "${ROOT}" diff --no-index --exit-code "${REMOTE_OUT}" "${tmp}" \
                >/dev/null 2>&1; then
                echo "rendered remote rules differ from committed ${REMOTE_OUT}; run: $(basename "$0") remote-test" >&2
                exit 1
            fi
        else
            local tmpdir
            tmpdir="$(mktemp -d)"
            trap 'rm -rf "${tmpdir}"' EXIT
            render_local "${tmpdir}"
            if ! diff -r -q "${LOCAL_OUT_DIR}" "${tmpdir}" >/dev/null 2>&1; then
                echo "rendered local rules differ from committed ${LOCAL_OUT_DIR}/; run: $(basename "$0") local" >&2
                exit 1
            fi
        fi
        exit 0
    fi

    if [[ "${target}" == "remote-test" ]]; then
        if [[ -n "${to}" ]]; then
            render_remote "${to}"
        else
            mkdir -p "$(dirname "${REMOTE_OUT}")"
            render_remote "${REMOTE_OUT}"
        fi
    else
        if [[ -n "${to}" ]]; then
            render_local "${to}"
        else
            mkdir -p "${LOCAL_OUT_DIR}"
            render_local "${LOCAL_OUT_DIR}"
        fi
    fi
}

main "$@"
