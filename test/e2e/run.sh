#!/usr/bin/env bash
# End-to-end validation of kuma-migrator output against real Kong Mesh control planes.
#
#   test/e2e/run.sh up      create clusters + CPs (Kubernetes via Helm, Universal in Docker)
#   test/e2e/run.sh test    migrate the fixtures and apply the output (default)
#   test/e2e/run.sh extract apply the input to the 2.14 CPs and check what extract writes (needs `up`)
#   test/e2e/run.sh questions  admission probes behind results/questions.md (needs `up`)
#   test/e2e/run.sh down    delete everything `up` created
#   test/e2e/run.sh all     up + test + down
#
# Environment:
#   E2E_PLATFORM   marina (default on macOS) | kind (default elsewhere, used in CI)
#   E2E_ENVS       "kubernetes universal" (default) — restrict to one
#   E2E_WORK       scratch dir for kumactl binaries, configs and migrate output
#   E2E_UCP_HOST   host the Universal CP ports are published on (default localhost)
#   KM_V2 / KM_V3  Kong Mesh versions (default 2.14.7 / 3.0.1)
#
# Results land in test/e2e/results/<target>-<env>.md. The script exits non-zero when a
# rejection is not listed in test/e2e/expected-rejections.txt.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
FIX=$HERE/fixtures
RESULTS=$HERE/results
EXPECTED=$HERE/expected-rejections.txt

KM_V2=${KM_V2:-2.14.7}
KM_V3=${KM_V3:-3.0.1}
if [[ "$(uname -s)" == Darwin ]]; then E2E_PLATFORM=${E2E_PLATFORM:-marina}; else E2E_PLATFORM=${E2E_PLATFORM:-kind}; fi
E2E_ENVS=${E2E_ENVS:-"kubernetes universal"}
E2E_WORK=${E2E_WORK:-${TMPDIR:-/tmp}/kuma-migrator-e2e}
E2E_UCP_HOST=${E2E_UCP_HOST:-localhost}
GATEWAY_API_VERSION=${GATEWAY_API_VERSION:-v1.3.0}

CLUSTER_V2=km214
CLUSTER_V3=km30
UCP_V2=e2e-ucp-v2
UCP_V3=e2e-ucp-v3
UCP_V2_PORT=25681
UCP_V3_PORT=35681

mkdir -p "$E2E_WORK" "$RESULTS"
log() { printf '\033[1m==> %s\033[0m\n' "$*" >&2; }

kctx() { # kubeconfig context for a cluster name
  if [[ $E2E_PLATFORM == kind ]]; then echo "kind-$1"; else echo "$1"; fi
}

docker_env() {
  if [[ $E2E_PLATFORM == marina ]]; then eval "$(marina docker-env)"; fi
}

kumactl_bin() { echo "$E2E_WORK/bin/$1/kumactl"; }
kumactl_cfg() { echo "$E2E_WORK/kumactl-$1.config"; }

# ── provisioning ──────────────────────────────────────────────────────────────

download_kumactl() {
  local v=$1 os arch dir
  dir=$E2E_WORK/bin/$v
  [[ -x $dir/kumactl ]] && return 0
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m); [[ $arch == x86_64 ]] && arch=amd64; [[ $arch == aarch64 ]] && arch=arm64
  log "downloading kumactl $v ($os-$arch)"
  mkdir -p "$dir/dl"
  curl -fsSL -o "$dir/dl/km.tgz" \
    "https://packages.konghq.com/public/kong-mesh-binaries-release/raw/names/kong-mesh-$os-$arch/versions/$v/kong-mesh-$v-$os-$arch.tar.gz"
  tar -xzf "$dir/dl/km.tgz" -C "$dir/dl" "kong-mesh-$v/bin/kumactl"
  mv "$dir/dl/kong-mesh-$v/bin/kumactl" "$dir/kumactl"
  rm -rf "$dir/dl"
}

create_cluster() {
  local name=$1
  if [[ $E2E_PLATFORM == kind ]]; then
    kind get clusters 2>/dev/null | grep -qx "$name" || kind create cluster --name "$name" --wait 120s
  else
    marina cluster list 2>/dev/null | grep -qw "$name" || marina cluster create "$name"
  fi
}

install_kong_mesh() { # cluster version
  local ctx; ctx=$(kctx "$1")
  kubectl --context "$ctx" apply --server-side -f \
    "https://github.com/kubernetes-sigs/gateway-api/releases/download/$GATEWAY_API_VERSION/experimental-install.yaml" >/dev/null
  # Kong Mesh 2.14 and 3.0 charts both default to controlPlane.mode=zone; a zone with no
  # Global is the single-zone deployment (3.0 removed `standalone`).
  helm upgrade --install kong-mesh kong-mesh/kong-mesh --version "$2" \
    --kube-context "$ctx" -n kong-mesh-system --create-namespace --wait --timeout 8m >/dev/null
  kubectl --context "$ctx" -n kong-mesh-system rollout status deploy/kong-mesh-control-plane --timeout 5m >/dev/null
}

start_universal_cp() { # name version port
  docker_env
  docker rm -f "$1" >/dev/null 2>&1 || true
  docker run -d --name "$1" -p "$3:5681" -e KUMA_MODE=zone -e KUMA_STORE_TYPE=memory \
    "kong/kuma-cp:$2" run >/dev/null
  local i token
  for i in $(seq 60); do
    token=$(docker exec "$1" /busybox/wget -qO- http://localhost:5681/global-secrets/admin-user-token 2>/dev/null |
      jq -r .data 2>/dev/null | base64 -d 2>/dev/null) && [[ -n $token ]] && break
    sleep 2
  done
  [[ -n ${token:-} ]] || { echo "Universal CP $1 did not come up" >&2; docker logs "$1" | tail -20 >&2; return 1; }
  local cfg; cfg=$(kumactl_cfg "$1"); rm -f "$cfg"
  "$(kumactl_bin "$2")" --config-file "$cfg" config control-planes add --name "$1" \
    --address "http://$E2E_UCP_HOST:$3" --auth-type=tokens --auth-conf "token=$token" --overwrite >/dev/null
}

cmd_up() {
  download_kumactl "$KM_V2"; download_kumactl "$KM_V3"
  [[ $E2E_PLATFORM == marina ]] && marina up >/dev/null
  if [[ " $E2E_ENVS " == *" kubernetes "* ]]; then
    helm repo add kong-mesh https://kong.github.io/kong-mesh-charts >/dev/null 2>&1 || true
    helm repo update kong-mesh >/dev/null
    create_cluster "$CLUSTER_V2"; create_cluster "$CLUSTER_V3"
    log "installing Kong Mesh $KM_V2 on $CLUSTER_V2 and $KM_V3 on $CLUSTER_V3"
    install_kong_mesh "$CLUSTER_V2" "$KM_V2" & p1=$!
    install_kong_mesh "$CLUSTER_V3" "$KM_V3" & p2=$!
    wait $p1; wait $p2
  fi
  if [[ " $E2E_ENVS " == *" universal "* ]]; then
    log "starting Universal CPs $KM_V2 (:$UCP_V2_PORT) and $KM_V3 (:$UCP_V3_PORT)"
    start_universal_cp "$UCP_V2" "$KM_V2" "$UCP_V2_PORT"
    start_universal_cp "$UCP_V3" "$KM_V3" "$UCP_V3_PORT"
  fi
}

cmd_down() {
  docker_env
  docker rm -f "$UCP_V2" "$UCP_V3" >/dev/null 2>&1 || true
  for c in "$CLUSTER_V2" "$CLUSTER_V3"; do
    if [[ $E2E_PLATFORM == kind ]]; then kind delete cluster --name "$c" >/dev/null 2>&1 || true
    else marina cluster delete "$c" >/dev/null 2>&1 || true; fi
  done
}

# ── apply helpers ─────────────────────────────────────────────────────────────

# apply_one <env> <target> <file> [real]  → prints "ok|<message>"; exit status 0 = accepted.
# Kubernetes: server-side dry-run, which runs the Kuma validating webhooks
# (sideEffects: None). Universal: a real apply against the throwaway memory-store CP
# (kumactl --dry-run is client-side only).
#
# APPLY_VERB=replace (Kubernetes only): the report lists the file as
# `kubectl replace -f <file> --save-config` (a document that rewrites an existing object).
# Validated with `kubectl replace --dry-run=server`. On v3 the 3.0 CP is seeded with the
# meshes only, so the object does not exist there: NotFound falls back to apply.
apply_one() {
  local env=$1 target=$2 file=$3 mode=${4:-dry} out
  if [[ $env == kubernetes ]]; then
    local ctx; ctx=$(kctx "$( [[ $target == v2 ]] && echo $CLUSTER_V2 || echo $CLUSTER_V3)")
    local verb=${APPLY_VERB:-apply}
    local args=(--context "$ctx" "$verb" -f "$file")
    [[ $mode == dry ]] && args+=(--dry-run=server)
    [[ -n ${APPLY_NS:-} ]] && args+=(-n "$APPLY_NS")
    if out=$(kubectl "${args[@]}" 2>&1); then echo "$out"; return 0; fi
    if [[ $verb == replace && $target == v3 && ( $out == *NotFound* || $out == *"not found"* ) ]]; then
      APPLY_VERB=apply apply_one "$env" "$target" "$file" "$mode"; return
    fi
    echo "$out"; return 1
  else
    local cp ver; if [[ $target == v2 ]]; then cp=$UCP_V2 ver=$KM_V2; else cp=$UCP_V3 ver=$KM_V3; fi
    out=$("$(kumactl_bin "$ver")" --config-file "$(kumactl_cfg "$cp")" apply -f "$file" 2>&1) &&
      { echo "$out"; return 0; } || { echo "$out"; return 1; }
  fi
}

# missing_namespace <ctx> <file>: true when the file holds a namespaced kind with no
# metadata.namespace. `kubectl apply` would put it in the caller's current namespace,
# which the Kuma webhooks skip unless it carries kuma.io/sidecar-injection, and where the
# CP gives the policy the workload-owner role (it then selects only that namespace).
missing_namespace() {
  local cache=$E2E_WORK/namespaced-$1 kind ns
  [[ -s $cache ]] || kubectl --context "$1" api-resources --namespaced=true --no-headers | awk '{print $NF}' >"$cache"
  kind=$(awk '/^kind:/{print $2; exit}' "$2")
  ns=$(awk '/^metadata:/{m=1; next} m && /^[^ ]/{m=0} m && /^  namespace:/{print $2; exit}' "$2")
  [[ -z $ns ]] && grep -qx "$kind" "$cache"
}

# Universal kumactl apply takes one resource per call for clear per-object errors.
split_docs() { # file outdir → prints part paths
  awk -v d="$2" 'BEGIN{n=0; f=sprintf("%s/part-%03d.yaml",d,n)} /^---[[:space:]]*$/{n++; f=sprintf("%s/part-%03d.yaml",d,n); next} {print > f}' "$1"
  ls "$2"/part-*.yaml
}

md_escape() { tr '\n' ' ' | sed 's/|/\\|/g; s/  */ /g' | cut -c1-600; }

# record <results-file> <phase> <relpath> <status> <message>
record() { local m=${5//$E2E_WORK/\$E2E_WORK}; printf '| %s | `%s` | %s | %s |\n' "$2" "$3" "$4" "$(echo "$m" | md_escape)" >>"$1"; }

is_expected() { # target env relpath
  [[ -f $EXPECTED ]] && grep -v '^#' "$EXPECTED" | awk -v t="$1" -v e="$2" -v p="$3" '$1==t && $2==e && $3==p {f=1} END{exit !f}'
}

UNEXPECTED=0

seed() { # env target
  local env=$1 target=$2 f part tmp
  tmp=$(mktemp -d)
  if [[ $env == kubernetes ]]; then
    for f in "$FIX"/seed/kubernetes/*.yaml; do apply_one "$env" "$target" "$f" real >/dev/null; done
    # 3.0 clusters do not have the input applied, so the meshes the output targets must exist.
    [[ $target == v3 ]] && apply_one "$env" "$target" "$FIX/seed/v3/kubernetes-meshes.yaml" real >/dev/null
  else
    for f in "$FIX"/seed/universal/*.yaml; do
      rm -f "$tmp"/part-*; for part in $(split_docs "$f" "$tmp"); do apply_one "$env" "$target" "$part" real >/dev/null; done
    done
  fi
  rm -rf "$tmp"
}

# apply the 2.x input to the 2.14 CP for real: it is the state the migration starts from.
apply_input() { # env results-file
  local env=$1 res=$2 base f rel out
  base=$FIX/$env
  log "applying $env input fixtures to $KM_V2"
  while IFS= read -r f; do
    rel=${f#$base/}
    if out=$(apply_one "$env" v2 "$f" real); then record "$res" input "$rel" accepted "$(echo "$out" | grep -i warn || true)"
    else record "$res" input "$rel" "**REJECTED**" "$out"
      is_expected v2-input "$env" "$rel" || UNEXPECTED=$((UNEXPECTED + 1)); fi
  done < <({ find "$base" -path '*/mesh/Mesh-*.yaml' | sort; find "$base" -name '*.yaml' ! -path '*/mesh/Mesh-*' | sort; })
}

# kind/name pairs of every document under a directory (Kubernetes or Universal format).
list_kind_names() {
  find "$1" -name '*.yaml' ! -path '*/_manual/*' -print0 | xargs -0 awk '
    FNR == 1 {flush()}
    /^---/ {flush(); next}
    /^(kind|type): / {k=$2}
    /^name: / {n=$2}
    /^metadata:/ {m=1; next}
    m && /^  name: / {n=$2}
    m && /^[^ ]/ {m=0}
    function flush() { if (k != "" && n != "") print k "/" n; k=""; n=""; m=0 }
    END {flush()}' | sort -u
}

# Run `extract` against the 2.14 CP holding the input and list the input objects it
# did not write. extract is what produces migrate's input in real use.
check_extract() { # env
  local env=$1 dir=$E2E_WORK/extract-$env res=$RESULTS/extract-$env.md missing skip
  rm -rf "$dir"
  log "extract from the $KM_V2 $env CP"
  if [[ $env == kubernetes ]]; then
    "$E2E_WORK/kuma-migrator" extract --kube-context "$(kctx $CLUSTER_V2)" --output-dir "$dir" >"$E2E_WORK/extract-$env.log" 2>&1
    skip="Dataplane AccessRoleBinding ZoneEgress ZoneIngress Zone Workload Secret GlobalSecret"
  else
    # extract passes $KUMACTL_CONFIG to every kumactl it spawns as --config-file.
    PATH="$(dirname "$(kumactl_bin "$KM_V2")"):$PATH" KUMACTL_CONFIG="$(kumactl_cfg "$UCP_V2")" \
      "$E2E_WORK/kuma-migrator" extract --kumactl-context "$UCP_V2" --output-dir "$dir" >"$E2E_WORK/extract-$env.log" 2>&1
    skip="AccessRoleBinding Zone Workload Secret GlobalSecret"
  fi
  {
    echo "# extract from Kong Mesh $KM_V2 ($env)"
    echo
    echo "Input objects applied to the CP that \`kuma-migrator extract\` did not write"
    echo "(kinds on the default skip list excluded: $skip)."
    echo
    echo "| Kind/name | Note |"
    echo "|---|---|"
  } >"$res"
  missing=$(comm -23 <(list_kind_names "$FIX/$env") <(list_kind_names "$dir"))
  local kn n=0
  for kn in $missing; do
    [[ " $skip " == *" ${kn%%/*} "* ]] && continue
    echo "| \`$kn\` | not extracted |" >>"$res"; n=$((n + 1))
  done
  ((n == 0)) && echo "| — | everything extracted |" >>"$res"
  { echo; echo "Extract log:"; echo; echo '```text'; sed -e 's/\x1b\[[0-9;]*m//g' -e "s|$E2E_WORK|\$E2E_WORK|g" "$E2E_WORK/extract-$env.log" | grep -v '^  →' ; echo '```'; } >>"$res"
}

# Files in Apply Checklist order: every `kubectl apply -f <dir>/`, `kubectl apply|replace -f
# <file>` and `kumactl apply -f <file>` line of the report's "## Apply Checklist" section,
# directories expanded to their files.
checklist_files() { # report
  sed -n '/^## Apply Checklist/,/^## /p' "$1" |
    grep -oE '^ *(kubectl|kumactl) (apply|replace) -f [^ ]+' | awk '{print $NF}' | grep -v '^http' |
    while IFS= read -r p; do
      if [[ -d $p ]]; then find "$p" -maxdepth 1 -name '*.yaml' | sort; else echo "$p"; fi
    done
}

# Files the Apply Checklist lists as `kubectl replace -f <file> --save-config`: documents that
# rewrite an existing object, validated with `kubectl replace` (see apply_one).
checklist_replace_files() { # report
  sed -n '/^## Apply Checklist/,/^## /p' "$1" |
    { grep -oE '^ *kubectl replace -f [^ ]+' || true; } | awk '{print $NF}'
}

run_target() { # env target
  local env=$1 target=$2
  local out=$E2E_WORK/out/$target-$env res=$RESULTS/$target-$env.md
  rm -rf "$out"
  log "migrate --to-latest $target ($env)"
  "$E2E_WORK/kuma-migrator" migrate --input-dir "$FIX/$env" --output-dir "$out" --to-latest "$target" >"$E2E_WORK/migrate-$target-$env.log" 2>&1

  {
    echo "# $target output on $env"
    echo
    echo "Kong Mesh $( [[ $target == v2 ]] && echo "$KM_V2" || echo "$KM_V3"), $env, platform \`$E2E_PLATFORM\`."
    [[ $env == kubernetes ]] && echo "Applied with \`kubectl apply --dry-run=server\` (runs the validating webhooks)." ||
      echo "Applied for real with \`kumactl apply\` against a memory-store CP."
    echo
    echo "| Phase | File | Result | Server message |"
    echo "|---|---|---|---|"
  } >"$res"

  if [[ $target == v2 ]]; then apply_input "$env" "$res"; check_extract "$env"; else seed "$env" v3; fi

  local f rel msg tmp part verb listed=$E2E_WORK/listed-$target-$env replaced=$E2E_WORK/replace-$target-$env
  checklist_files "$out/migration-report.md" >"$listed"
  checklist_replace_files "$out/migration-report.md" >"$replaced"
  log "applying $(wc -l <"$listed" | tr -d ' ') checklist files to $target $env"
  tmp=$(mktemp -d)
  while IFS= read -r f; do
    rel=${f#$out/}
    if [[ $env == universal ]]; then
      rm -f "$tmp"/part-*; local ok=1 all=""
      for part in $(split_docs "$f" "$tmp"); do
        if ! msg=$(apply_one "$env" "$target" "$part"); then ok=0; fi; all+="$msg "
      done
      msg=$all
    else
      local ctx; ctx=$(kctx "$( [[ $target == v2 ]] && echo $CLUSTER_V2 || echo $CLUSTER_V3)")
      verb=apply; grep -qxF "$f" "$replaced" && verb=replace
      if missing_namespace "$ctx" "$f"; then
        record "$res" namespace "$rel" "**NO NAMESPACE**" "namespaced kind without metadata.namespace; validated below with -n kong-mesh-system"
        is_expected "$target-ns" "$env" "$rel" || UNEXPECTED=$((UNEXPECTED + 1))
        if msg=$(APPLY_VERB=$verb APPLY_NS=kong-mesh-system apply_one "$env" "$target" "$f"); then ok=1; else ok=0; fi
      else
        if msg=$(APPLY_VERB=$verb apply_one "$env" "$target" "$f"); then ok=1; else ok=0; fi
      fi
    fi
    if ((ok)); then record "$res" apply "$rel" accepted "$(echo "$msg" | grep -i warn || true)"
    else record "$res" apply "$rel" "**REJECTED**" "$msg"
      is_expected "$target" "$env" "$rel" || UNEXPECTED=$((UNEXPECTED + 1)); fi
  done <"$listed"

  # Apply-tree files the checklist never mentions.
  while IFS= read -r f; do
    grep -qxF "$f" "$listed" || record "$res" coverage "${f#$out/}" "**NOT IN CHECKLIST**" ""
  done < <(find "$out" -name '*.yaml' ! -path '*/_manual/*' | sort)

  # _manual/: kinds the target removed. Expected to be rejected by the 3.0 CP.
  if [[ $target == v3 ]]; then
    while IFS= read -r f; do
      rel=${f#$out/}
      if msg=$(apply_one "$env" v3 "$f"); then
        record "$res" _manual "$rel" "**ACCEPTED (unexpected)**" "$msg"
      else record "$res" _manual "$rel" "rejected (expected)" "$msg"; fi
    done < <(find "$out" -path '*/_manual/*' -name '*.yaml' | sort)
  fi
  rm -rf "$tmp"

  {
    echo
    echo "## Rejections"
    echo
    grep -E '\*\*(REJECTED|NOT IN CHECKLIST|ACCEPTED|NO NAMESPACE)' "$res" || echo "None."
  } >>"$res.tmp"
  cat "$res.tmp" >>"$res"; rm -f "$res.tmp"
}

cmd_test() {
  log "building kuma-migrator"
  (cd "$ROOT" && go build -o "$E2E_WORK/kuma-migrator" .)
  download_kumactl "$KM_V2"; download_kumactl "$KM_V3"
  local env
  for env in $E2E_ENVS; do
    if [[ $env == universal ]]; then
      # Fresh memory stores so every run starts from the same state.
      start_universal_cp "$UCP_V2" "$KM_V2" "$UCP_V2_PORT"
      start_universal_cp "$UCP_V3" "$KM_V3" "$UCP_V3_PORT"
    fi
    seed "$env" v2
    run_target "$env" v2
    run_target "$env" v3
  done
  if ((UNEXPECTED > 0)); then
    log "$UNEXPECTED unexpected rejection(s) — see $RESULTS/"
    exit 1
  fi
  log "all rejections match $EXPECTED"
}

# extract check only: apply the input to the 2.14 CPs and run extract against them
# (results/extract-<env>.md). Needs `up`.
cmd_extract() {
  log "building kuma-migrator"
  (cd "$ROOT" && go build -o "$E2E_WORK/kuma-migrator" .)
  download_kumactl "$KM_V2"
  local env
  for env in $E2E_ENVS; do
    [[ $env == universal ]] && start_universal_cp "$UCP_V2" "$KM_V2" "$UCP_V2_PORT"
    seed "$env" v2
    apply_input "$env" "$E2E_WORK/input-$env.md"
    check_extract "$env"
  done
  if ((UNEXPECTED > 0)); then
    log "$UNEXPECTED fixture(s) rejected by $KM_V2 — see $E2E_WORK/input-*.md"
    exit 1
  fi
}

case "${1:-test}" in
  up) cmd_up ;;
  extract) cmd_extract ;;
  questions) source "$HERE/questions.sh"; cmd_questions ;;
  test) cmd_test ;;
  down) cmd_down ;;
  all) cmd_up; trap cmd_down EXIT; cmd_test ;;
  *) echo "usage: $0 [up|test|extract|questions|down|all]" >&2; exit 2 ;;
esac
