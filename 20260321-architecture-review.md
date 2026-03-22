# Architecture Review: rancher-desktop-daemon

Date: 2026-03-21

## Scope

This review covers the repository architecture as implemented in the current tree, with emphasis on:

- control-plane bootstrap and lifecycle
- embedded vs external controller composition
- discovery and cross-process coordination
- controller boundaries and domain layering
- testability and operational risk

I reviewed the design docs, the bootstrap paths in `cmd/` and `pkg/service/`, representative controllers, and the current test surface.

## Executive Summary

The repo has a coherent core idea: ship Rancher Desktop 2's backend as a single Go control plane built on Kubernetes API machinery, then model platform behaviors as controllers. That is a strong architectural direction for a stateful desktop product. The control plane bootstrap is reasonably clean, the controller-runtime integration is solid, and the Lima controller shows that the model can carry real host-side behavior.

The main architectural weaknesses are not in the controller pattern itself. They are in the seams around it:

1. instance selection is process-global ambient state
2. embedded and external managers coordinate through a fail-open discovery path
3. discovery assumes localhost process topology and stores process-local endpoints in cluster state
4. controller composition depends heavily on `init()` side effects and blank imports
5. the test surface is thin around the integration seams, and some envtest-based coverage currently depends on live network access

Overall assessment: good direction, workable current implementation, but several bootstrap and composition decisions will become expensive if the project keeps adding controller groups, external processes, or alternate deployment modes.

## Architecture Snapshot

The current architecture is:

- `rdd service serve` starts kine/SQLite, builds the embedded API server chain, writes kubeconfig, initializes discovery, then starts a shared controller-runtime manager for enabled controllers (`pkg/service/cmd/service.go`).
- Controllers implement a common `base.Controller` interface and register globally through package side effects (`pkg/controllers/base/controller.go`).
- The embedded binary includes all controller groups via blank imports in the service package (`pkg/service/cmd/service.go`).
- Separate external controller binaries reuse a common bootstrap path from `pkg/external/main.go`, fetch kubeconfig from `rdd`, consult discovery, and start their own shared manager if needed.
- Runtime discovery is implemented by storing controller-manager metadata in a ConfigMap in `rdd-system`, including local health, metrics, and passthrough endpoints (`pkg/service/controllers/discovery.go`).
- The most mature domain flow is the Lima VM controller, which reconciles CRDs to host-side Lima state and uses webhooks plus on-disk sentinels to bridge Kubernetes state and filesystem state (`pkg/controllers/lima/limavm/controllers/limavm_controller.go`).

## Findings

### 1. Instance selection is implemented as ambient process-global state

Severity: High

The instance model is built around `sync.OnceValue` globals in `pkg/instance/instance.go`, so the chosen instance becomes immutable after first access. That forces `cmd/rdd/main.go` to rewrite `os.Args` before Cobra runs just to get `--instance` into the environment early enough.

Evidence:

- `cmd/rdd/main.go:28-69`
- `pkg/instance/instance.go:16-114`

Why this matters:

- command behavior becomes order-dependent
- library code cannot safely be reused in-process with multiple instances
- test setup is harder because state is cached globally
- the CLI has to work around architecture instead of the other way around

Recommendation:

Replace the global instance singleton with an explicit `InstanceContext` or `Paths` object passed from command bootstrap into service/controller code. Keep a compatibility helper only at the outer edge if needed.

### 2. External controller startup is fail-open on discovery errors, which can create duplicate control loops

Severity: High

External controllers decide whether to start by checking discovery. If that check errors, they log and start anyway.

Evidence:

- `pkg/external/main.go:62-71`
- `pkg/external/main.go:232-255`

Why this matters:

- a transient API server or discovery failure can start an external controller even when the embedded manager already owns that controller
- that creates split-brain reconciliation risk around CRDs, webhooks, and owned resources
- the repo explicitly supports embedded and external modes, so this seam is business-critical

Recommendation:

Fail closed for ownership decisions. If discovery is unavailable, block with timeout and a clear error instead of starting optimistically. If the project wants strong single-writer guarantees, add a lease or leader-election-style ownership record per controller group.

### 3. Discovery stores process-local localhost endpoints in cluster state

Severity: Medium

Discovery serializes `http://localhost:<port>` health, metrics, and passthrough endpoints into the cluster ConfigMap, and liveness is inferred by probing those local addresses.

Evidence:

- `pkg/service/controllers/discovery.go:32-47`
- `pkg/service/controllers/discovery.go:105-111`
- `pkg/service/controllers/discovery.go:345-357`

Why this matters:

- cluster metadata is coupled to host-local process topology
- the design strongly prefers same-host execution today, but this makes alternate topologies much harder later
- it mixes two concerns in one mechanism: cluster-visible service registration and machine-local IPC routing
- passthrough routing becomes dependent on local reverse proxy assumptions rather than a proper service abstraction

Recommendation:

Split discovery into:

- cluster-visible controller ownership/state
- local transport registration for health/passthrough routing

If remote execution is not a near-term goal, that can still be modeled behind an interface so the current localhost implementation remains the default backend rather than the architecture itself.

### 4. Controller composition relies heavily on `init()` registration and blank imports

Severity: Medium

Controllers are discovered through package side effects. The embedded service imports controller packages solely to trigger registration, and external binaries do the same per API group.

Evidence:

- `pkg/controllers/base/controller.go:91-118`
- `pkg/service/cmd/service.go:55-65`
- `cmd/app-controller/main.go:10-15`
- `cmd/rdd-controller/main.go:10-15`

Why this matters:

- the actual runtime graph is not explicit in one place
- adding or removing a controller is partly a build-graph exercise instead of a typed composition decision
- it is easy to get surprising behavior from import ordering, dead code, or accidental registration
- static analysis and targeted testing are harder than with explicit registries

Recommendation:

Move toward explicit controller set construction per binary, even if each controller still exposes a small factory. That preserves the current modularity but makes composition visible, testable, and versionable.

### 5. The test strategy is integration-leaning but still too brittle at the seams

Severity: Medium

The repo has a good BATS integration harness, but only six Go `_test.go` files were present in the tree I reviewed, and the envtest-backed tests currently fail offline because they try to download controller-tools release metadata at runtime.

Evidence:

- Go tests present:
  - `pkg/controllers/base/finalizer_test.go`
  - `pkg/controllers/rdd/notary/validation_test.go`
  - `pkg/controllers/lima/limavm/validation_test.go`
  - `pkg/service/controllers/discovery_test.go`
  - `pkg/util/logfile/logfile_test.go`
  - `pkg/util/tail/tail_test.go`
- BATS coverage exists across CLI, service, builtin, rdd, app, lima, and containers under `bats/tests/`
- `go test ./...` with `GOCACHE=/tmp/rdd-gocache` failed in:
  - `pkg/controllers/base`
  - `pkg/service/controllers`
  because envtest attempted to fetch `https://raw.githubusercontent.com/kubernetes-sigs/controller-tools/HEAD/envtest-releases.yaml`

Why this matters:

- the highest-risk architecture is in bootstrap, discovery, and ownership coordination
- those seams need deterministic, local-first automated tests
- network-coupled tests create noisy failures in CI and local development

Recommendation:

Vendor or preinstall envtest assets in a reproducible way and add more narrow tests around:

- controller ownership arbitration
- discovery register/unregister behavior
- startup/shutdown race handling
- controller enablement parsing and composition

## Strengths

### The core control-plane direction is sound

Using Kubernetes API machinery plus controller-runtime is a good fit for a stateful desktop backend. The bootstrap path in `pkg/service/cmd/service.go` is understandable, and the server chain is assembled in a disciplined way instead of being spread across many packages.

### Shared controller-runtime infrastructure is a real asset

`SharedControllerManager` centralizes CRD installation, webhook certificate handling, health probes, passthrough registration, and controller startup. That keeps controller packages small and lets the repo evolve controller groups without repeating a lot of boilerplate.

Relevant code:

- `pkg/service/controllers/manager.go:91-282`

### The Lima controller demonstrates the intended model well

The Lima VM controller is the clearest proof that the architectural model works. It coordinates CRDs, webhooks, owner references, status conditions, filesystem state, and process lifecycle without collapsing everything into CLI scripts or ad hoc imperative glue.

Relevant code:

- `pkg/controllers/lima/limavm/controllers/limavm_controller.go:146-348`

## Maturity Notes

Not every controller group is at the same maturity level. The `app` controller is currently very thin and mostly status-oriented rather than being the strong top-level orchestration boundary implied by the design docs.

Evidence:

- `pkg/controllers/app/app/controllers/app_controller.go:36-85`

That is fine for an early or mid-stage codebase, but it means the repo's deepest architectural investment today is really in service bootstrap plus Lima, not yet in a rich application orchestration layer.

## Recommended Next Moves

1. Remove ambient instance state from the core packages and pass instance context explicitly.
2. Make external-controller ownership decisions fail closed and add a stronger single-writer mechanism.
3. Separate cluster discovery from local transport registration.
4. Replace side-effect registration with explicit controller set assembly per binary.
5. Harden the test harness so envtest is local and reproducible, then add seam-focused tests around discovery and startup races.

## Verification Notes

I ran:

```bash
GOCACHE=/tmp/rdd-gocache go test ./...
```

Result:

- many packages built successfully
- `pkg/controllers/lima/limavm`, `pkg/controllers/rdd/notary`, `pkg/util/logfile`, and `pkg/util/tail` passed
- `pkg/controllers/base` and `pkg/service/controllers` failed because envtest attempted to download release metadata from GitHub, which was unavailable in this environment

That means the review conclusions are based on code inspection plus a partial local test run, not a fully green test suite.
