#!/usr/bin/env bash
set -euo pipefail

# Patches an already-running operator Deployment (e.g. one deployed via the
# charts/humio-operator Helm chart, or hack/reload-kind.sh) to run under
# Delve for remote debugging, in place. Does not modify the Helm chart or
# any tracked manifests. It's an imperative, reversible patch of the live
# object. See docs/debugging-with-delve.md.
#
# Usage: RELEASE=humio-operator NAMESPACE=logscale IMG=controller:debug \
#          hack/patch-operator-for-debug.sh

RELEASE="${RELEASE:-humio-operator}"
NAMESPACE="${NAMESPACE:-humio-operator-system}"
CONTAINER="${CONTAINER:-humio-operator}"
IMG="${IMG:-controller:debug}"
DELVE_PORT="${DELVE_PORT:-2345}"

echo "==> Patching deployment/${RELEASE} (container ${CONTAINER}) in namespace ${NAMESPACE} to run under Delve"
echo "    image=${IMG} port=${DELVE_PORT}"

container_index=$(kubectl get deployment "${RELEASE}" -n "${NAMESPACE}" -o json \
  | jq --arg c "${CONTAINER}" '.spec.template.spec.containers | map(.name) | index($c)')

if [[ "${container_index}" == "null" ]]; then
  echo "ERROR: container '${CONTAINER}' not found in deployment/${RELEASE} -n ${NAMESPACE}" >&2
  exit 1
fi

current_command=$(kubectl get deployment "${RELEASE}" -n "${NAMESPACE}" -o json \
  | jq -r --argjson idx "${container_index}" '.spec.template.spec.containers[$idx].command // [] | join(" ")')
if [[ "${current_command}" == "/dlv" ]]; then
  echo "ERROR: deployment/${RELEASE} container '${CONTAINER}' already runs under /dlv - re-running would wrap it a second time." >&2
  echo "       Revert first (helm upgrade / hack/reload-kind.sh) before patching again." >&2
  exit 1
fi

existing_args=$(kubectl get deployment "${RELEASE}" -n "${NAMESPACE}" -o json \
  | jq --arg c "${CONTAINER}" '[.spec.template.spec.containers[] | select(.name == $c) | (.args // [])[]]')

patch=$(jq -n \
  --arg container "${CONTAINER}" \
  --arg img "${IMG}" \
  --arg port "${DELVE_PORT}" \
  --argjson existingArgs "${existing_args}" \
  '{
    spec: {
      template: {
        spec: {
          containers: [
            {
              name: $container,
              image: $img,
              command: ["/dlv"],
              args: (["exec", "/manager", "--headless", ("--listen=:" + $port), "--api-version=2", "--accept-multiclient", "--continue", "--"] + $existingArgs),
              ports: [{containerPort: ($port | tonumber), name: "delve"}],
              securityContext: {
                capabilities: {add: ["SYS_PTRACE"]},
                seccompProfile: {type: "Unconfined"}
              },
              resources: {
                limits: {cpu: "500m", memory: "768Mi"},
                requests: {cpu: "100m", memory: "256Mi"}
              }
            }
          ]
        }
      }
    }
  }')

kubectl patch deployment "${RELEASE}" -n "${NAMESPACE}" --type=strategic -p "${patch}"

# Liveness/readiness probes would kill the pod the moment execution pauses
# at a breakpoint. Drop them if present. Each is patched independently and
# allowed to fail so the script stays idempotent (e.g. probes already gone
# on a re-run).
echo "==> Removing liveness/readiness probes on ${CONTAINER}, if present"
kubectl patch deployment "${RELEASE}" -n "${NAMESPACE}" --type=json \
  -p "[{\"op\": \"remove\", \"path\": \"/spec/template/spec/containers/${container_index}/livenessProbe\"}]" \
  >/dev/null 2>&1 || true
kubectl patch deployment "${RELEASE}" -n "${NAMESPACE}" --type=json \
  -p "[{\"op\": \"remove\", \"path\": \"/spec/template/spec/containers/${container_index}/readinessProbe\"}]" \
  >/dev/null 2>&1 || true

echo "==> Waiting for rollout"
kubectl rollout status deployment/"${RELEASE}" -n "${NAMESPACE}" --timeout=120s

cat <<EOF
==> Done. Forward the debug port and attach:
    kubectl port-forward -n ${NAMESPACE} deploy/${RELEASE} ${DELVE_PORT}:${DELVE_PORT}

To revert, re-run whatever deployed the original spec (e.g. 'helm upgrade
--install ...' or hack/reload-kind.sh). This script only patches the live
object, it never touches chart/kustomize sources.
EOF
