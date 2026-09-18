# Hermes Operator

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/paperclipinc/hermes-operator"><img src="https://goreportcard.com/badge/github.com/paperclipinc/hermes-operator" alt="Go Report Card"></a>
  <a href="https://github.com/paperclipinc/hermes-operator/actions/workflows/ci.yaml"><img src="https://github.com/paperclipinc/hermes-operator/actions/workflows/ci.yaml/badge.svg" alt="CI"></a>
  <a href="https://github.com/paperclipinc/hermes-operator/actions/workflows/e2e.yaml"><img src="https://github.com/paperclipinc/hermes-operator/actions/workflows/e2e.yaml/badge.svg" alt="E2E"></a>
  <a href="https://github.com/paperclipinc/hermes-operator/actions/workflows/conformance.yaml"><img src="https://github.com/paperclipinc/hermes-operator/actions/workflows/conformance.yaml/badge.svg" alt="Conformance"></a>
  <a href="https://github.com/paperclipinc/hermes-operator/releases/latest"><img src="https://img.shields.io/github/v/release/paperclipinc/hermes-operator" alt="Release"></a>
  <a href="#supported-kubernetes-versions"><img src="https://img.shields.io/badge/kubernetes-1.28--1.32-blue" alt="Kubernetes versions"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/paperclipinc/hermes-operator" alt="Go version"></a>
  <a href="https://api.securityscorecards.dev/projects/github.com/paperclipinc/hermes-operator"><img src="https://api.securityscorecards.dev/projects/github.com/paperclipinc/hermes-operator/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://artifacthub.io/packages/search?repo=hermes-operator"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/hermes-operator" alt="Artifact Hub"></a>
</p>

Kubernetes operator for [nousresearch/hermes-agent](https://github.com/nousresearch/hermes-agent): a Python-based self-improving multi-platform AI agent. Declarative spec,
opinionated security defaults, S3 backups, OCI-registry auto-update,
SSA-based GitOps coexistence, and a one-shot migration path from
openclaw-operator.

`hermes-operator` ships as v1.0.0 with [v1 stability commitments](docs/api-versioning.md)
in place from day one: no v0.x grind.

> Inspired by [openclaw-rocks/openclaw-operator](https://github.com/openclaw-rocks/openclaw-operator);
> openclaw lessons #437, #446, #433, #471, #479, #458, #469 (and many more)
> informed concrete guardrails baked into v1. See
> [docs/superpowers/specs/2026-05-12-hermes-operator-design.md](docs/superpowers/specs/2026-05-12-hermes-operator-design.md) §1.G3.

## Quickstart

```bash
# 1. Install the CRDs and operator via Helm (OCI chart; Helm 3.8+).
#    Omit --version for the latest release, or add --version X.Y.Z to pin.
helm install hermes-operator \
  oci://ghcr.io/paperclipinc/charts/hermes-operator \
  -n hermes-operator --create-namespace

# 2. Apply a minimal instance. The agent runs the upstream NousResearch/hermes-agent
#    s6 image (gateway + OpenAI-compatible API server), with /health on port 8443.
kubectl apply -n agents -f - <<'YAML'
apiVersion: hermes.agent/v1
kind: HermesInstance
metadata:
  name: my-hermes
spec:
  image:
    repository: ghcr.io/paperclipinc/hermes-agent
    tag: "v2026.9.14"
  # Point the gateway at an LLM provider and inject the key via spec.env.
  config:
    raw:
      model: gpt-4o-mini
      base_url: https://api.openai.com/v1
  env:
    - name: OPENAI_API_KEY
      valueFrom:
        secretKeyRef:
          name: hermes-llm
          key: apiKey
  storage:
    persistence:
      enabled: true
      size: 10Gi
YAML

# 3. Watch it converge.
kubectl get hi -n agents -w
# NAME        READY   PHASE   IMAGE                                AGE
# my-hermes   True    Ready   ghcr.io/paperclipinc/hermes-agent:v2026.9.14    30s
```

If you omit `spec.config.raw.model`, the operator injects a non-routable placeholder
so the gateway and API server still come up (and `/health` passes) without making
live LLM calls; inference then fails clearly until a real provider is set. Each
instance also gets an operator-managed random `api_server_key` (in its
`<name>-gateway-tokens` Secret) that authenticates the OpenAI-compatible
`/v1/...` API; `/health` is unauthenticated. See [Agent runtime](docs/runtime.md).

For more involved scenarios, see [`examples/`](examples/).

## Architecture

```mermaid
flowchart LR
  subgraph User
    GitOps[FluxCD / Argo]
    Kubectl[kubectl apply]
  end

  subgraph ControlPlane["Kubernetes control plane"]
    APIServer[(kube-apiserver)]
    HInstance["HermesInstance"]
    HSelfConfig["HermesSelfConfig"]
    HClusterDefaults["HermesClusterDefaults<br/>(singleton)"]
  end

  subgraph Operator["hermes-operator pod"]
    DefaulterWebhook[Defaulter]
    ValidatorWebhook[Validator]
    InstanceCtrl[HermesInstance<br/>controller]
    SelfConfigCtrl[HermesSelfConfig<br/>controller<br/>SSA: hermes.agent/selfconfig]
    ClusterDefaultsCtrl[ClusterDefaults<br/>controller]
  end

  subgraph Workload["agent workload (per HermesInstance)"]
    STS[StatefulSet]
    Svc[Service]
    NetPol[NetworkPolicy default-deny]
    PVC[PVC /opt/data]
    Honcho[Honcho Deploy<br/>profile store]
    CronJob[Backup CronJob]
  end

  S3[(S3-compatible<br/>backup target)]
  OCI[(OCI registry<br/>hermes-agent tags)]

  GitOps --> APIServer
  Kubectl --> APIServer
  APIServer <-->|admission| DefaulterWebhook
  APIServer <-->|admission| ValidatorWebhook
  APIServer --> HInstance
  APIServer --> HSelfConfig
  APIServer --> HClusterDefaults
  HInstance --> InstanceCtrl
  HSelfConfig --> SelfConfigCtrl
  HClusterDefaults --> ClusterDefaultsCtrl
  InstanceCtrl --> STS
  InstanceCtrl --> Svc
  InstanceCtrl --> NetPol
  InstanceCtrl --> PVC
  InstanceCtrl --> Honcho
  InstanceCtrl --> CronJob
  SelfConfigCtrl -.SSA patch.-> HInstance
  CronJob --> S3
  InstanceCtrl -.poll.-> OCI
```

The agent runs as a StatefulSet (single replica by default) under a default-
deny NetworkPolicy. The `HermesSelfConfig` controller uses Server-Side Apply
under field manager `hermes.agent/selfconfig`, so FluxCD/Argo can own the
parent `HermesInstance` for other fields without flap. `HermesClusterDefaults`
is a cluster-scoped singleton (name **must** be `cluster`) that fills `nil`
fields only: explicit values on the instance always win.

## Features

| Area | Feature | Notes |
|---|---|---|
| **Declarative** | Single `HermesInstance` CR drives the whole stack | StatefulSet, Service, PVC, NetworkPolicy, ConfigMap, PDB, HPA, ServiceMonitor, Honcho deploy, backup CronJob: all owned and reconciled. |
| **Declarative** | `HermesClusterDefaults` for cluster-wide defaults | Defaulting webhook fills `nil` fields only. |
| **Declarative** | `spec.skills` git-clone install | Each entry cloned into `~/.hermes/skills/<name>` by an init container on every pod (re)start, no custom image needed. See [Skills](#skills). |
| **Declarative** | `spec.workspace.initialFiles` mounted live | ConfigMap-backed files under `HERMES_HOME`, always reflecting the current spec — good fit for a GitOps-managed `SOUL.md`/`HERMES.md`. See [`docs/api-reference.md`](docs/api-reference.md#specworkspace). |
| **Adaptive** | `HermesSelfConfig` for audited agent-initiated mutations | SSA under field manager `hermes.agent/selfconfig`. Policy-gated by `spec.selfConfigure.protectedKeys`. |
| **Adaptive** | OCI-registry-driven auto-update | Channel-pinned polling, pre-update backup, probe-failure rollback. |
| **Secure** | Default-deny NetworkPolicy + per-gateway allow rules | Derived from `spec.gateways` and `spec.networking.egress`. |
| **Secure** | Hardened container security context | The upstream s6 runtime starts as root so `/init` (PID 1) can remap the in-image user to uid/gid 1000 and chown `/opt/data`, then every service drops to uid 1000 via `s6-setuidgid`. `allowPrivilegeEscalation=false`, `fsGroup=1000`, and seccomp `RuntimeDefault` remain; `runAsNonRoot`/read-only rootfs/drop-ALL-caps are not set (s6 needs `CHOWN`/`SETUID`/`SETGID`/`DAC_OVERRIDE`/`FOWNER` and a writable `/run`). Requires an SCC that permits a root-start container (e.g. `anyuid`); incompatible with OpenShift `restricted`/`restricted-v2`. See [Agent runtime](docs/runtime.md). |
| **Secure** | Optional Tailscale Serve sidecar | Per-instance MagicDNS hostname + Tailscale TLS cert, no LoadBalancer/Ingress. See [Tailscale Serve](#tailscale-serve). |
| **Secure** | Per-CRD validating + defaulting webhooks | Plus warnings on unknown config keys and unresolvable gateway tokens. |
| **Secure** | RBAC aggregation labels | `kubectl auth can-i create hermesinstances --as=jane` works out of the box. |
| **Secure** | Image signing + SBOM | Cosign keyless OIDC, SPDX SBOM on every release. |
| **Observable** | Prometheus metrics + ServiceMonitor | Per-controller, per-instance, per-subsystem. `metrics.secure` consistent. |
| **Observable** | [Grafana dashboard](docs/grafana/) | Ships as JSON. Variables: `namespace`, `instance`. |
| **Observable** | Exhaustive [condition catalogue](docs/conditions.md) | Every condition × every reason code, documented and stable. |
| **Multi-platform** | Telegram / Discord / Slack / WhatsApp / Signal gateways | First-class `spec.gateways.*` sections, secret-rotation-friendly. |
| **Upstream runtime** | Ships the supported NousResearch/hermes-agent s6 image | The published `ghcr.io/paperclipinc/hermes-agent` is built `FROM` the upstream image (pinned by digest). It bundles the gateway, dashboard, OpenAI-compatible API server, a Playwright/Chromium browser, node, ffmpeg, and all Python deps. No init-container venv build — the old `uv sync` / `init-apt`/`init-uv`/`init-pip` chain is gone. See [Agent runtime](docs/runtime.md). |
| **Upstream runtime** | FFmpeg, ripgrep, browser, node available out of the box | Bundled in the upstream hermes-agent image. |
| **Scalable** | Optional HPA via `spec.availability.hpa` | StatefulSet retained for identity through restarts. |
| **Scalable** | Optional `topologySpreadConstraints` | Sane defaults plus `spec.availability.topologySpreadConstraints` override. |
| **Resilient** | PodDisruptionBudget auto-managed when `replicas > 1` | |
| **Resilient** | Finalizer-driven backup-on-delete | `r.Patch` (JSON patch) for finalizer mutations, never `r.Update`. |
| **Resilient** | Zombie-process reaper | s6-overlay `/init` as PID 1 reaps zombies; `shareProcessNamespace: false` by default (its `/init` must be PID 1). |
| **Backup / Restore** | S3-compatible backups | Scheduled, on-delete, pre-update. `tar.zst` snapshots + `meta.json`. |
| **Backup / Restore** | Declarative one-shot restore | `spec.restoreFrom` is immutable once applied. |
| **Migration** | One-shot OpenClaw → Hermes migration | From sibling `OpenClawInstance` or S3 backup. Uses hermes-agent's importer. |
| **Profile store** | Optional Honcho companion | Deployment + Service + PVC + secret, fully managed. |
| **Gateway auth** | Per-platform `secretRef` for tokens | Rotate independently, audited via webhook warnings. |
| **Cloud-native** | Helm chart, OLM bundle, plain kustomize manifests | All three are first-class. CRDs templated under the Helm chart. |
| **Cloud-native** | Multi-arch (`amd64`+`arm64`), Cosign-signed, SBOM-attested | |
| **GitOps** | SSA-based SelfConfig coexists with Argo/Flux | No flap on shared instances. |
| **Stability** | v1.0 ships with [versioning](docs/api-versioning.md) + [deprecation](docs/deprecations.md) policies | Conversion-webhook scaffolding in place for future v2. |

## Tailscale Serve

Set `spec.tailscale.enabled=true` to expose the gateway on your private
tailnet. The operator injects a `tailscale` sidecar running
[Tailscale Serve](https://tailscale.com/kb/1312/serve): each instance gets its
own MagicDNS hostname (`https://<hostname>.<tailnet>.ts.net`) with a
Tailscale-issued TLS certificate, terminated in the sidecar and proxied to the
gateway over localhost. No LoadBalancer or Ingress is needed. The field is
additive: the existing Service is unchanged.

```yaml
spec:
  tailscale:
    enabled: true
    mode: serve          # only "serve" is implemented today
    hostname: my-hermes  # MagicDNS hostname; defaults to metadata.name
    authKey:
      secretRef:
        name: hermes-tailscale
        key: authKey
    # image.{repository,tag,pullPolicy} and resources are also available.
```

Requirements and notes:

- **Auth key.** `authKey.secretRef` is required and must reference a
  **reusable + ephemeral** [Tailscale auth key](https://tailscale.com/kb/1085/auth-keys).
  Ephemeral means the node auto-removes from the tailnet when the pod stops;
  reusable means the sidecar re-registers under the same stable hostname on
  restart. The validating webhook rejects `enabled=true` without a
  `secretRef` and warns when the Secret or key does not resolve.
- **Tailnet prerequisites.** MagicDNS and HTTPS certificates must be enabled
  on the tailnet: Serve waits for `TS_CERT_DOMAIN` and never becomes ready
  without them.
- **NetworkPolicy.** When the operator-managed NetworkPolicy is enabled, it
  gains UDP egress on 3478 (STUN) and 41641 (WireGuard) for direct
  connections. If the network blocks UDP, Tailscale falls back to DERP relays
  over TCP/443, which the policy already allows.
- **Reserved names.** User sidecars must not be named `tailscale`, and
  `extraVolumes` must not be named `tailscale-serve` or `tailscale-tmp`: the
  webhook rejects them.

The sidecar's wiring status is reported via the `TailscaleReady` condition.
See [`docs/api-reference.md`](docs/api-reference.md#spectailscale) for the
full field list.

## Worked example: self-configure

The agent can persist a learned skill, env var, config patch, workspace file,
or Honcho profile by creating a `HermesSelfConfig` in its namespace. The
operator validates against the parent instance's `selfConfigure.protectedKeys`
allowlist and applies via SSA:

```yaml
apiVersion: hermes.agent/v1
kind: HermesSelfConfig
metadata:
  name: install-finance-skill
  namespace: agents
spec:
  instanceRef: my-hermes
  addSkills:
    - source: "git+https://github.com/foo/finance-skill@v1.2.0"
  patchConfig:
    schedules:
      morning-brief: "0 8 * * *"
  addEnvVars:
    - name: FINANCE_TZ
      value: Europe/Berlin
```

Apply, then watch:

```bash
kubectl get hsc -n agents
# NAME                      PHASE     INSTANCE    AGE
# install-finance-skill     Applied   my-hermes   3s
```

The audit trail lives in `kubectl describe hsc install-finance-skill` and on
the instance via the per-field SSA field manager
`hermes.agent/selfconfig`: `kubectl get hi my-hermes -o jsonpath='{.metadata.managedFields}'`
shows exactly which fields the agent owns vs. Flux owns vs. you own.

See [`examples/`](examples/) for end-to-end recipes.

## Skills

`spec.skills` declares a list of skills to install; an init container
`git clone`s each one into `~/.hermes/skills/<name>` on every pod (re)start,
so a GitOps-managed skill list always matches the running instance — no
custom image, no rebuild.

```yaml
spec:
  skills:
    - source: "https://github.com/juliusbrussee/caveman"
    - source: "git+https://github.com/foo/finance-skill@v1.2.0"
```

- `source` is a git remote. A leading `git+` is accepted and stripped (kept
  for compatibility with the `HermesSelfConfig.addSkills` examples above,
  which already use that shape). Append `@<ref>` to pin a branch, tag, or
  commit; without it, the remote's default branch is used.
- The installed directory name is the last path segment of the remote, with
  a trailing `.git` stripped — `.../foo/finance-skill.git` becomes
  `~/.hermes/skills/finance-skill`.
- The repo must already be in the shape hermes-agent expects: a `SKILL.md`
  at its root. This clones the repo verbatim; it does not `pip`/`uv` install
  anything, despite `spec.skills` covering the same field `HermesSelfConfig`
  uses to record installs it made at runtime.
- Because the init container re-clones on every restart, an instance is
  self-healing but not a place for the agent to durably hand-edit an
  installed skill's files — those edits are lost on the next pod restart.
  Use `HermesSelfConfig.addWorkspaceFiles` for agent-authored files that
  should persist instead.
- Only public remotes are supported today; there is no per-skill credential
  field yet.

Verify what actually landed:

```bash
kubectl logs <instance>-0 -c init-skills -n <namespace>   # clone output
kubectl exec <instance>-0 -n <namespace> -- ls /opt/data/skills/
```

### Private skill repos

`spec.skills` itself has no credential field yet, so a private remote needs a
hand-written `initContainer` instead — same idea as `init-skills`, with auth
added. Both options below use only existing fields (`initContainers`,
`extraVolumes`), no code change required.

**HTTPS + token:**

```yaml
spec:
  initContainers:
    - name: install-private-skill
      image: alpine/git:2.47.1
      command: ["/bin/sh", "-c"]
      args:
        - |
          set -eu
          rm -rf /opt/data/skills/my-private-skill
          git clone --depth 1 "https://${GIT_USER}:${GIT_TOKEN}@github.com/foo/my-private-skill.git" /opt/data/skills/my-private-skill
      envFrom:
        - secretRef:
            name: private-skill-creds   # keys: GIT_USER, GIT_TOKEN
      volumeMounts:
        - name: data
          mountPath: /opt/data
```

**SSH deploy key:**

```yaml
spec:
  extraVolumes:
    - name: skill-ssh-key
      secret:
        secretName: private-skill-ssh-key
        defaultMode: 0400
  initContainers:
    - name: install-private-skill
      image: alpine/git:2.47.1
      command: ["/bin/sh", "-c"]
      args:
        - |
          set -eu
          export GIT_SSH_COMMAND="ssh -i /etc/skill-ssh/id_ed25519 -o StrictHostKeyChecking=no"
          rm -rf /opt/data/skills/my-private-skill
          git clone --depth 1 git@github.com:foo/my-private-skill.git /opt/data/skills/my-private-skill
      volumeMounts:
        - name: data
          mountPath: /opt/data
        - name: skill-ssh-key
          mountPath: /etc/skill-ssh
          readOnly: true
```

A future `spec.skills[].credentialsRef` could fold this into the declarative
list instead of a hand-written `initContainer` per private skill; not
implemented yet.

## Installing extra toolchains (Go, TypeScript, etc.)

Skills are text (a `SKILL.md` plus supporting files); a language toolchain is
a binary install and needs a different approach. The upstream hermes-agent
image already bundles node, ffmpeg, ripgrep, and a browser (see
[Features](#features)), but anything else — Go, a global npm package like
`typescript`, additional apt packages — has two options:

**Install once onto the PVC via `spec.initContainers` (no rebuild).** Since
`/opt/data` is the persistent volume, anything an init container writes there
survives pod restarts; guard the install with an existence check so it only
runs once, and extend `PATH` via `spec.env` so the running agent finds it:

```yaml
spec:
  initContainers:
    - name: install-go
      image: golang:1.23-alpine
      command: ["/bin/sh", "-c"]
      args:
        - |
          set -eu
          if [ ! -x /opt/data/toolchains/go/bin/go ]; then
            mkdir -p /opt/data/toolchains
            cp -r /usr/local/go /opt/data/toolchains/go
          fi
      volumeMounts:
        - name: data
          mountPath: /opt/data
    - name: install-typescript
      image: ghcr.io/paperclipinc/hermes-agent:v2026.9.14   # already has node/npm
      command: ["/bin/sh", "-c"]
      args:
        - |
          set -eu
          if [ ! -x /opt/data/toolchains/npm-global/bin/tsc ]; then
            npm install -g typescript --prefix /opt/data/toolchains/npm-global
          fi
      volumeMounts:
        - name: data
          mountPath: /opt/data
  env:
    - name: PATH
      value: "/opt/data/toolchains/go/bin:/opt/data/toolchains/npm-global/bin:/usr/local/bin:/usr/bin:/bin"
```

**Build a custom agent image instead** when a toolchain rarely changes and
you'd rather not depend on network access at pod-start time: `FROM
ghcr.io/paperclipinc/hermes-agent:<tag>`, install what you need, push to your
own registry, and point `spec.image.repository`/`tag` (or `digest`) at it.
More reliable per-start, at the cost of a rebuild every time the toolchain
needs to change — the opposite tradeoff from the init-container approach
above.

## Supported Kubernetes versions

| Operator | Kubernetes |
|---|---|
| v1.x | 1.28, 1.29, 1.30, 1.31, 1.32 |

We drop the oldest k8s minor when Kubernetes EOLs it, on the *next* operator
minor release. Patch releases never change the supported matrix.

## Distribution

| Channel | What |
|---|---|
| Helm (OCI) | `helm install hermes-operator oci://ghcr.io/paperclipinc/charts/hermes-operator` |
| OLM / OperatorHub | `kubectl operator install hermes-operator` (pending first OperatorHub release) |
| Plain manifests | `kubectl apply -f https://github.com/paperclipinc/hermes-operator/releases/latest/download/install.yaml` |
| Container image | `ghcr.io/paperclipinc/hermes-operator:v0.1.9` (multi-arch, Cosign-signed, SBOM attested) |

## Documentation

- [Design spec](docs/superpowers/specs/2026-05-12-hermes-operator-design.md): the canonical product/architecture doc.
- [API reference](docs/api-reference.md): every field on every CR.
- [Condition catalogue](docs/conditions.md): every status condition, reason code, troubleshooting hint.
- [API versioning policy](docs/api-versioning.md): what is and is not a breaking change.
- [Deprecation policy](docs/deprecations.md): the 3-step flow + active deprecations.
- [Roadmap](ROADMAP.md): shipped, planned, future, non-goals.
- [Examples](examples/): 9 worked YAML recipes.
- [Grafana dashboard](docs/grafana/): operator-overview dashboard JSON.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Pull requests follow
[Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`,
`docs:`, `ci:`, `chore:`, `refactor:`, `test:`); release-please drives the
release-PR loop from `feat:`/`fix:`.

## Security

See [`SECURITY.md`](SECURITY.md). Report vulnerabilities via the GitHub
security advisory flow; do not file public issues for security bugs.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
