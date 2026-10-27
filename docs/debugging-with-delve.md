# Debugging the operator with Delve

This documents a dev-only workflow for attaching a remote debugger (GoLand,
VSCode, or plain `dlv connect`) to the humio-operator running in a real
Kubernetes cluster. It builds on a separate [Delve](https://github.com/go-delve/delve)-enabled
image and a kustomize overlay under `config/debug/`

## 1. Build the debug image

```shell
make docker-build-operator-debug IMG=<registry>/humio-operator:debug
```

This builds `Dockerfile.operator.debug`, which compiles the manager without
stripped symbols and with optimizations/inlining disabled
(`-gcflags="all=-N -l"`), and bakes in a `dlv` binary alongside it.

## 2. Push and deploy

```shell
make docker-push IMG=<registry>/humio-operator:debug
make deploy-debug IMG=<registry>/humio-operator:debug
```

`deploy-debug` deploys `config/debug`, which is `config/default` (CRDs, RBAC,
manager) patched so the `manager` container's entrypoint is:

```
/dlv exec /manager --headless --listen=:2345 --api-version=2 --accept-multiclient --continue -- --enable-leader-election
```

The `--continue` flag means the manager starts reconciling immediately on
pod start by attaching to a live process rather than pausing the execution in order
to wait for a debugger. The container also gets the `SYS_PTRACE` capability and an
`Unconfined` seccomp profile, both of which Delve needs to control the
manager process, and a bumped memory limit (Delve + an unoptimized binary
use noticeably more RAM than the production 30Mi limit).

For a local kind cluster, skip the registry round-trip:

```shell
docker build -f Dockerfile.operator.debug -t controller:debug .
kind load docker-image controller:debug --name kind
make deploy-debug IMG=controller:debug
```

> **Don't run this alongside a Helm-deployed operator in the same
> namespace/cluster!** You'd end up with two operators reconciling the same
> resources and fighting over leader election. Run `make undeploy` (or
> uninstall the Helm release) first.

## Alternative: patching an already-deployed operator

If the operator is already running in your cluster via the Helm chart
(`charts/humio-operator`) or any other mechanism, not `make deploy-debug`, you don't need to uninstall it and switch to `config/debug`. Instead,
patch the live Deployment in place with `hack/patch-operator-for-debug.sh`:

```shell
docker build -f Dockerfile.operator.debug -t controller:debug .
kind load docker-image controller:debug --name <cluster>

RELEASE=humio-operator NAMESPACE=<namespace> IMG=controller:debug \
  hack/patch-operator-for-debug.sh
```

This never edits the Helm chart or any tracked manifest. It's an
imperative `kubectl patch` against the live object, so it's fully
reversible by re-running whatever deployed the original spec (`helm
upgrade --install ...`, `hack/reload-kind.sh`, etc.). It applies the same
changes as the `config/debug` patch (dlv entrypoint, `SYS_PTRACE` +
`Unconfined` seccomp, bumped memory) and additionally removes any
liveness/readiness probes on the container, since a paused/breakpointed
process won't respond to health checks and would otherwise get killed by
the kubelet.

Env vars (all optional, shown with their defaults):

| Var           | Default                    | Meaning                                   |
|---------------|-----------------------------|--------------------------------------------|
| `RELEASE`     | `humio-operator`           | Deployment name to patch                   |
| `NAMESPACE`   | `humio-operator-system`     | Namespace the Deployment lives in          |
| `CONTAINER`   | `humio-operator`            | Container name inside the pod spec         |
| `IMG`         | `controller:debug`          | Debug image to switch the container to     |
| `DELVE_PORT`  | `2345`                      | Port Delve listens on                      |

The script refuses to run against a Deployment it has already patched
(detected by checking whether the container's `command` is already
`/dlv`) so it can't double-wrap the entrypoint. Revert first if you need
to re-patch (e.g. after changing `IMG`).

## 3. Reach the debug port

`config/debug` intentionally does **not** create a `NodePort`/`LoadBalancer`
Service for port 2345. Delve's headless API is unauthenticated, so exposing
it outside the cluster network would let anyone who can reach it take full
control of the operator process. The supported way to reach it is
`kubectl port-forward`, which tunnels through the API server rather than
relying on cluster/node networking, so it works identically no matter what
the cluster actually is: kind (on Colima, Docker Desktop, or anywhere else),
minikube, k3d, or a remote managed cluster like EKS/GKE/AKS.

```shell
kubectl wait -n humio-operator-system --for=condition=Ready pod -l control-plane=controller-manager --timeout=120s
kubectl port-forward -n humio-operator-system deploy/humio-operator-controller-manager 2345:2345
```

## 4. Attach a debugger

The container builds from `/workspace` (see `Dockerfile.operator.debug`), so
your IDE needs to map that path back to your local checkout or breakpoints
will silently never bind.

**GoLand**: create a *Go Remote* run configuration with host `localhost`,
port `2345`.

**VSCode**: add to `.vscode/launch.json`:

```json
{
  "name": "Attach to operator (Delve)",
  "type": "go",
  "request": "attach",
  "mode": "remote",
  "host": "127.0.0.1",
  "port": 2345,
  "substitutePath": [
    { "from": "${workspaceFolder}", "to": "/workspace" }
  ]
}
```

**Plain CLI**: `dlv connect localhost:2345`.

Once attached, set breakpoints in `internal/controller/...` and trigger a
reconcile (e.g. edit a `HumioCluster` resource) to hit them.

## Debugging the webhook operator

The webhook operator (`cmd/webhook-operator`) has no kustomize deployment -
`config/default` only deploys the main manager, so it's only ever deployed
via the `charts/humio-operator` Helm chart, as the `<release>-webhook`
Deployment. There's no `config/debug` overlay for it; patching the live
Deployment with `hack/patch-webhook-for-debug.sh` is the only way to attach
Delve, not just an alternative:

```shell
docker build -f Dockerfile.webhook.debug -t webhook-operator:debug .
kind load docker-image webhook-operator:debug --name <cluster>

RELEASE=humio-operator-webhook NAMESPACE=<namespace> IMG=webhook-operator:debug \
  hack/patch-webhook-for-debug.sh
```

`Dockerfile.webhook.debug` builds `cmd/webhook-operator/main.go` the same way
`Dockerfile.operator.debug` builds the manager: unstripped, with
optimizations/inlining disabled, with `dlv` baked in alongside it, on alpine
rather than the production `distroless/static` base so there's a shell and
`ca-certificates` for troubleshooting via `kubectl exec`.

`hack/patch-webhook-for-debug.sh` applies the same changes as
`hack/patch-operator-for-debug.sh` (dlv entrypoint, `SYS_PTRACE` +
`Unconfined` seccomp, bumped memory, dropped liveness/readiness probes) but
targets the `humio-operator-webhook` container and wraps `/webhook-operator`
instead of `/manager`. It leaves the chart's `tmp-tls` volume mount alone, so
the webhook's TLS serving certs still land in the same place.

Env vars (all optional, shown with their defaults):

| Var           | Default                       | Meaning                                   |
|---------------|--------------------------------|--------------------------------------------|
| `RELEASE`     | `humio-operator-webhook`      | Deployment name to patch                   |
| `NAMESPACE`   | `humio-operator-system`        | Namespace the Deployment lives in          |
| `CONTAINER`   | `humio-operator-webhook`      | Container name inside the pod spec         |
| `IMG`         | `webhook-operator:debug`       | Debug image to switch the container to     |
| `DELVE_PORT`  | `2345`                         | Port Delve listens on                      |

Reach and attach the same way as the manager (step 3/4 above), but
port-forward the webhook Deployment instead:

```shell
kubectl port-forward -n humio-operator-system deploy/humio-operator-webhook 2345:2345
```

If you're debugging both the manager and the webhook operator at once,
port-forward each to a different local port (e.g. `2345` and `2346`) since
they'd otherwise collide on your machine.
