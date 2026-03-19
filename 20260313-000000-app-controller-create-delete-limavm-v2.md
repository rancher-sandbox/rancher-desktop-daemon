# Deep Review: app-controller-create-delete-limavm

| | |
|---|---|
| **Date** | 2026-03-13 |
| **Branch** | `app-controller-create-delete-limavm` |
| **Reviewers** | Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro |
| **Verdict** | **Rework** — deletion path leaks VM on disk, embedded template hardcodes developer-specific path |

## Consolidated Review

### Executive Summary

This branch wires the App controller to create a LimaVM and its input ConfigMap, propagate `spec.running`, and mirror status conditions back. The BATS/MSYS2 infrastructure changes and dependency bumps are clean.

**Structure:** 1 commit — App controller LimaVM lifecycle (`cf27c08`).

The controller has two critical bugs that prevent merging: the deletion path leaks the VM on disk, and the embedded template hardcodes a developer-specific path. **Go back for rework.**

### Critical Issues

1. **App deletion leaks VM on disk** — `app_controller.go:56-61` [Codex GPT 5.4, Gemini 3.1 Pro]

```go
if base.IsBeingDeleted(&app) {
    log.Info("App resource is being deleted, performing cleanup")
    if err := base.DeleteOwnedResources(ctx, r.Client, &app, r.Manager); err != nil {
        return ctrl.Result{}, err
    }
    return ctrl.Result{}, base.RemoveFinalizer(ctx, r.Client, &app)
}
```

`DeleteOwnedResources` strips the `rdd.rancherdesktop.io/cleanup` finalizer from owned resources before deleting them (`finalizer.go:134-150`). The LimaVM controller relies on this same finalizer to trigger `handleDeletion()`, which stops the VM process and removes instance files from disk. Stripping the finalizer first lets Kubernetes hard-delete the LimaVM object instantly, bypassing cleanup. The VM persists on disk with no controller to remove it.

Fix: Delete the LimaVM directly with `r.Delete(ctx, limaVM)` and requeue. Remove the App finalizer only after `r.Get` for the LimaVM returns `IsNotFound`, confirming the LimaVM controller completed its own teardown.

2. **Hardcoded developer home directory in embedded template** — `lima.yaml:37` [Claude Opus 4.6, Gemini 3.1 Pro]

```yaml
param:
  HOST_HOME: /Users/ninok
```

This file is embedded via `//go:embed`, so every binary built from this branch ships the wrong path. Breaks volume mounts and host-agent integrations on any machine except the original developer's.

Fix: Templatize this field (e.g., `{{.Home}}` like the `hostSocket` line already does) or remove it if the Lima guest agent discovers the host home at runtime.

3. **ConfigMap recreated on every status-triggered reconcile** — `app_controller.go:73-88` [Gemini 3.1 Pro]

```go
err := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputConfigMap)
if apierrors.IsNotFound(err) {
    // ... create inputConfigMap ...
    return ctrl.Result{}, nil  // <-- early return; status mirroring never reached
}
```

In steady state the input ConfigMap is deleted (line 121-126). When a LimaVM status update triggers the next reconcile, the controller finds the ConfigMap missing, recreates it, and returns early — skipping status mirroring (lines 136-151). The next reconcile deletes the ConfigMap again and proceeds to mirror. This doubles reconcile cycles and delays status propagation.

Not an infinite loop (as originally reported by Gemini), but real churn.

Fix: Check for the LimaVM first. If it exists, skip ConfigMap creation and proceed to status mirroring. Only create the ConfigMap when the LimaVM does not yet exist.

### Important Issues

1. **Input ConfigMap has no owner reference** — `app_controller.go:76-84` [Claude Opus 4.6]

```go
inputConfigMap = &corev1.ConfigMap{
    ObjectMeta: metav1.ObjectMeta{
        Name:      inputConfigMapName,
        Namespace: namespace,
    },
    // no owner reference set
}
```

If the App is deleted between ConfigMap creation (reconcile 1) and LimaVM creation (reconcile 2), `DeleteOwnedResources` cannot find this ConfigMap because `IsOwnedByUID` checks `ownerReferences`. It remains orphaned.

Fix: Call `ctrl.SetControllerReference(&app, inputConfigMap, r.Scheme)` before creating it, matching the pattern used for LimaVM at line 109.

2. **Mutable namespace field leaks resources** — `app_controller.go:71` [Codex GPT 5.4]

```go
namespace := app.GetResourceNamespace()
```

`app.spec.namespace` has no immutability constraint. Changing it after the LimaVM exists causes the controller to look in the new namespace, find nothing, and bootstrap a fresh ConfigMap there. The old LimaVM in the original namespace becomes orphaned. On App deletion, cleanup searches only the new namespace.

Fix: Add a CEL immutability rule on `spec.namespace` (e.g., `rule: "self == oldSelf"`, matching the pattern on `templateRef` in `LimaVMSpec`).

3. **ObservedGeneration stamps unobserved generation** — `app_controller.go:138-143` [Claude Opus 4.6, Codex GPT 5.4]

```go
statusChanged = apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
    Type:               cond.Type,
    Status:             cond.Status,
    Reason:             cond.Reason,
    Message:            cond.Message,
    ObservedGeneration: app.Generation,
}) || statusChanged
```

After an App spec update, a reconcile may run before the LimaVM controller processes the change. The App's conditions then claim the current generation was observed when the child status still reflects the prior generation.

Fix: Omit `ObservedGeneration` or propagate the LimaVM's own observed generation once it tracks one.

4. **Condition mirroring drops LastTransitionTime** — `app_controller.go:138-143` [Claude Opus 4.6]

The mirrored `metav1.Condition` omits `LastTransitionTime`. `apimeta.SetStatusCondition` fills zero-valued timestamps with `metav1.Now()`, so the App's conditions reflect when the App controller *copied* the condition, not when the LimaVM actually transitioned. This matters for the stale-status-after-restart fix, which compares `lastTransitionTime`.

Fix: Add `LastTransitionTime: cond.LastTransitionTime` to the struct literal.

### Suggestions

1. **Dead code: Recorder field and condition constants** — `app_controller.go:30-31,37` [Claude Opus 4.6]

```go
ConditionCreated = "Created"
ConditionRunning = "Running"
// ...
Recorder events.EventRecorder
```

The `setCondition` method that used `Recorder`, `ConditionCreated`, and `ConditionRunning` was removed by this change. All three are now unused.

Fix: Remove the dead field, import, and constants.

2. **Grammar error in comment** — `controller.go:63` [Claude Opus 4.6, Gemini 3.1 Pro]

"It also registers Lima types allows App controller to create and watch LimaVM resources."

Fix: "It also registers Lima types, allowing the App controller to create and watch LimaVM resources."

3. **Non-NotFound errors silently ignored on ConfigMap re-fetch** — `app_controller.go:121` [Claude Opus 4.6]

```go
if err := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputConfigMap); err == nil {
```

The `if err == nil` check silently skips ConfigMap deletion when Get fails for transient reasons. Low impact since the ConfigMap is inert, but inconsistent with error handling elsewhere.

Fix: Log non-NotFound errors for observability.

### Testing Assessment

No tests were added. Untested scenarios:

1. ConfigMap creation on first reconcile
2. LimaVM creation with correct owner reference and templateRef
3. Input ConfigMap deletion after LimaVM exists
4. `spec.running` propagation from App to LimaVM
5. Condition mirroring from LimaVM status to App status
6. App deletion cleanup (finalizer, VM cleanup on disk)
7. Idempotency: reconcile converges when everything matches desired state
8. App deleted between ConfigMap and LimaVM creation
9. Namespace change after initial bootstrap

Existing tests in `bats/tests/32-app-controllers/` (demo, passthrough) establish a convention this controller should follow.

### Documentation Assessment

- No architectural documentation describes the lifecycle flow between AppReconciler, the LimaVM webhook, and the LimaVMReconciler.
- The `lima.yaml` file carries no comment explaining what the hardcoded values represent.

---

## Agent Performance Retro

| Metric | Claude Opus 4.6 | Codex GPT 5.4 | Gemini 3.1 Pro |
|---|---|---|---|
| Duration (s) | 278 | 268 | 250 |
| Critical | 1 | 2 | 3 |
| Important | 4 | 3 | 0 |
| Suggestion | 2 | 0 | 1 |
| False positives | 0 | 0 | 1 (severity) |
| Unique insights | 4 | 3 | 1 |

*Agents ran concurrently; durations are approximate.*

**Codex GPT 5.4** provided the most value by identifying the highest-severity bug (finalizer stripping) and the confirmed namespace mutability issue. **Claude Opus 4.6** had the broadest coverage and zero false positives but missed the critical cross-controller bug. **Gemini 3.1 Pro** identified the reconcile ordering issue but overstated its severity as "infinite loop."

---

## Skill Improvement Recommendations

- Instruct agents to trace cross-controller interactions, not just the diff in isolation.
- Distinguish "full Kubernetes cluster concerns" from "embedded single-user kube-apiserver concerns."
- Require agents to show exact code paths for critical findings, reducing overstatements.
- Ask agents to rank untested scenarios by risk.

---

## Appendix: Original Reviews

*(Same content as the original report — omitted here for brevity.)*
