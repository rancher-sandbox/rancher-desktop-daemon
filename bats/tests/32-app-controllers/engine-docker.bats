# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Engine controller tests — verify that the engine controller mirrors Docker
# containers, images, and volumes into Container, Image, and Volume
# resources, and that deletions and spec.state changes are forwarded
# to Docker.

local_setup_file() {
    # The Docker socket access pattern used by these tests is not yet wired
    # up for Windows/WSL2.
    skip_on_windows
    # Deliberately skip setup_rdd_control_plane here: `rdd set` internally
    # calls ensureServiceRunning, which is exactly the CLI path we want to
    # exercise — the engine controller is part of the default controller
    # set, so no explicit --controllers selection is needed.
    rdd svc delete
    rdd set running=true
    run -0 rdd svc paths docker_socket
    export DOCKER_HOST="unix://${output}"
    # Mirror resources live in App.spec.namespace. Override RDD_NAMESPACE
    # to whatever the App was created with so the test queries the same
    # namespace the engine controller uses, regardless of CRD defaults.
    RDD_NAMESPACE=$(rdd ctl get app app -o jsonpath='{.spec.namespace}')
    export RDD_NAMESPACE
}

# --- Startup ---

@test "ContainerNamespace moby exists" {
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/moby --timeout=10s
}

# --- Image mirroring ---

@test "docker pull creates Image resource" {
    docker pull busybox
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=busybox:latest" --timeout=30s
}

@test "docker rmi of one tag removes only the tag mirror" {
    # Docker's untag event carries the image ID hash, not the tag
    # name, so the engine cannot match the event payload against
    # status.repoTag directly. reconcileImageByID re-inspects the
    # image and prunes Image mirrors whose tags are no longer present
    # in Docker's current RepoTags.
    docker tag busybox:latest busybox:alias
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=busybox:alias" --timeout=30s

    # Sanity check: the original tag is still mirrored.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=busybox:latest" -o name
    assert_output

    docker rmi busybox:alias
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=busybox:alias" --timeout=30s

    # busybox:latest must remain because the image still has that tag.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=busybox:latest" -o name
    assert_output
}

@test "tagging a dangling image removes the dangling mirror" {
    # Create a dangling image by pinning it via a running container and
    # then removing its only tag with --force. Docker keeps the image
    # (the container still references it), so the UnTag path produces
    # a dangling Image mirror rather than deleting the mirror outright.
    # Tagging the dangling image then must prune the dangling mirror
    # and leave only the new tagged one — the symmetric direction of
    # the untag test above.
    docker pull alpine:latest
    run -0 docker inspect alpine:latest --format '{{.Id}}'
    alpine_id=${output}

    # The pin must be a running container: in this VM's Docker,
    # rmi --force will fully remove an image whose only references are
    # stopped containers, leaving nothing for the dangling-mirror path
    # to observe.
    docker run -d --name dangling-pin alpine:latest sleep inf

    # Remove the only tag; the running container keeps the image alive.
    docker rmi --force alpine:latest

    # The dangling mirror has no RepoTag — query by status.id instead.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.id=${alpine_id}" --timeout=30s

    # Sanity check: exactly one mirror exists for this image and it has
    # no RepoTag (the dangling mirror).
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.id=${alpine_id}" \
        -o jsonpath='{.items[*].status.repoTag}'
    refute_output

    # Re-tag the image with a fresh alias. ActionTag routes through
    # reconcileImageByID, which creates a new tagged mirror and prunes
    # the now-stale dangling mirror.
    docker tag "${alpine_id}" dangling-tag-test:v1
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=dangling-tag-test:v1" --timeout=30s

    # The dangling mirror must be gone: the only mirror for this image
    # is the new tagged one.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.id=${alpine_id}" \
        -o jsonpath='{.items[*].status.repoTag}'
    assert_output "dangling-tag-test:v1"

    # Cleanup.
    docker rm --force dangling-pin
    docker rmi dangling-tag-test:v1
}

# --- Container lifecycle mirroring ---

@test "docker run creates Container resource with status=running" {
    run_e -0 docker run -d --name test-lifecycle busybox sleep inf
    cid=${output}

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=10s

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.name}'
    assert_output "test-lifecycle"

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.spec.state}'
    assert_output "unknown"
}

@test "docker stop updates Container status to exited" {
    run -0 docker inspect test-lifecycle --format '{{.Id}}'
    cid=${output}

    docker stop test-lifecycle
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
}

@test "docker rm deletes Container resource" {
    run -0 docker inspect test-lifecycle --format '{{.Id}}'
    cid=${output}

    docker rm --force test-lifecycle
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s
}

# --- Volume mirroring ---

# Volume mirror names are derived from the Docker name via SHA-256 hashing
# (see volumeMirrorName in sync_volumes.go), so tests look up Volumes by
# status.name through the .status.name selectable field rather than by
# metadata.name.

@test "docker volume create creates Volume resource" {
    docker volume create test-vol

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=test-vol" --timeout=30s
}

@test "docker volume rm deletes Volume resource" {
    docker volume rm test-vol
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=test-vol" --timeout=30s
}

@test "volume name with uppercase and underscore is mirrored" {
    # Docker permits characters (uppercase, underscore) that are
    # invalid in RFC 1123 subdomain names. volumeK8sName hashes the
    # Docker name into a valid subdomain; the original is preserved
    # in status.name and queryable via the field selector.
    docker volume create My_Vol_Ume
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=My_Vol_Ume" --timeout=30s
    docker volume rm My_Vol_Ume
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=My_Vol_Ume" --timeout=30s
}

# --- Deletion via the API removes the Docker object ---

@test "deleting Container resource removes Docker container" {
    run_e -0 docker create --name test-delete busybox sleep inf
    cid=${output}

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s

    rdd ctl delete container "${cid}" --namespace="${RDD_NAMESPACE}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s

    run -1 docker inspect test-delete
}

@test "deleting an in-use Image keeps the finalizer until the container is removed" {
    run_e -0 docker run -d --name test-inuse busybox sleep inf
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    # Resolve the Image mirror name from its repoTag.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=busybox:latest" -o name
    assert_output
    image_ref=${output}

    # Docker refuses to remove an image referenced by a running
    # container, so the mirror stays in Terminating with the finalizer
    # intact until the container is gone.
    rdd ctl delete "${image_ref}" --namespace="${RDD_NAMESPACE}" --wait=false

    # Both reads are race-free: deletionTimestamp lands synchronously
    # with the DELETE response, and the finalizer cannot be removed
    # while the container still references the image.
    run -0 rdd ctl get "${image_ref}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.deletionTimestamp}'
    assert_output
    run -0 rdd ctl get "${image_ref}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.finalizers[0]}'
    assert_output "engine.rancherdesktop.io/mirror"

    # Remove the container so Docker permits the image removal. The
    # next reconcile's finalizer retry succeeds and the Image mirror
    # is finally collected.
    docker rm --force test-inuse
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        "${image_ref}" --timeout=30s
}

# --- Container state transitions via spec ---

@test "patching spec.state=created stops Docker container" {
    run_e -0 docker run -d --name test-state busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    rdd ctl patch container "${cid}" --namespace="${RDD_NAMESPACE}" \
        --type=merge -p '{"spec":{"state":"created"}}'

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    run -0 docker inspect test-state --format '{{.State.Status}}'
    assert_output "exited"
}

@test "patching spec.state=running restarts Docker container" {
    run -0 docker inspect test-state --format '{{.Id}}'
    cid=${output}

    rdd ctl patch container "${cid}" --namespace="${RDD_NAMESPACE}" \
        --type=merge -p '{"spec":{"state":"running"}}'

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    run -0 docker inspect test-state --format '{{.State.Status}}'
    assert_output "running"
}

@test "patching spec.state=created stops a paused container" {
    # Docker's ContainerStop handles paused containers natively, so
    # the reconciler must dispatch ContainerStop whenever the desired
    # state differs from the actual state rather than only when the
    # container is actively running.
    run -0 docker inspect test-state --format '{{.Id}}'
    cid=${output}

    # Park the reconciler on spec.state=unknown before pausing. With
    # spec.state=running, the reconciler would race us by auto-unpausing
    # (desired=running, actual=paused → ContainerUnpause) before our
    # wait can observe status=paused.
    rdd ctl patch container "${cid}" --namespace="${RDD_NAMESPACE}" \
        --type=merge -p '{"spec":{"state":"unknown"}}'

    docker pause test-state
    rdd ctl wait --for=jsonpath='{.status.status}'=paused \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    rdd ctl patch container "${cid}" --namespace="${RDD_NAMESPACE}" \
        --type=merge -p '{"spec":{"state":"created"}}'

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    run -0 docker inspect test-state --format '{{.State.Status}}'
    assert_output "exited"
}

@test "patching spec.state=running unpauses a paused container" {
    # Docker rejects ContainerStart on a paused container with
    # "container is paused, unpause before starting". The reconciler
    # must dispatch ContainerUnpause when desired=running and
    # actual=paused, symmetric with the ContainerStop path above.
    run_e -0 docker run -d --name test-unpause busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    docker pause test-unpause
    rdd ctl wait --for=jsonpath='{.status.status}'=paused \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    run -0 docker inspect test-unpause --format '{{.State.Status}}'
    assert_output "paused"

    rdd ctl patch container "${cid}" --namespace="${RDD_NAMESPACE}" \
        --type=merge -p '{"spec":{"state":"running"}}'

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    run -0 docker inspect test-unpause --format '{{.State.Status}}'
    assert_output "running"

    docker rm --force test-unpause
}

# --- Cleanup on shutdown ---

@test "stopping VM removes all mirror resources" {
    # Make sure we have at least one resource to verify cleanup.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/moby --timeout=10s

    rdd set running=false

    run -0 rdd ctl get containers --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get images --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get volumes --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

@test "rdd set running=false returns promptly when already stopped" {
    # rdd set running=false waits for the App's Running condition to
    # go to False, not for ContainerEngineReady. When the VM is
    # already stopped, the wait must complete immediately rather than
    # hang on the already-False engine condition.
    rdd set --timeout=10s running=false
}

@test "VM start recreates ContainerNamespace/moby after cleanup" {
    # The "stopping VM removes all mirror resources" test above swept
    # ContainerNamespace/moby along with the rest of the mirrors, so
    # restarting the VM must recreate it. This bridges the teardown
    # above to the ContainerNamespace delete test below, which needs
    # the namespace to exist before it can delete it.
    rdd set running=true
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/moby --timeout=10s
}

@test "deleting ContainerNamespace/moby completes without a finalizer hang" {
    # moby ContainerNamespace has no mirror finalizer, so a user delete
    # must return promptly rather than get trapped in Terminating.
    rdd ctl delete ContainerNamespace/moby --namespace="${RDD_NAMESPACE}" --timeout=10s
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

# --- containerd backend ---

@test "containerd backend reports ContainerEngineReady=NotApplicable and skips mirroring" {
    # Stop first so there is no stale True/Connected from moby to
    # satisfy the CLI wait below before the engine reconciler has run.
    rdd set running=false

    # Start with containerd. rdd set waits for ContainerEngineReady=True,
    # which the engine reconciler satisfies immediately with reason
    # NotApplicable because engine mirroring only supports the moby
    # backend.
    rdd set containerEngine.name=containerd running=true

    run -0 rdd ctl get app app \
        -o jsonpath='{.status.conditions[?(@.type=="ContainerEngineReady")].reason}'
    assert_output "NotApplicable"

    # No mirror resources should exist in containerd mode.
    run -0 rdd ctl get containers --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get images --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get volumes --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

# --- Docker context management ---

# docker_context_dir returns the ~/.docker/contexts/meta/<hash> directory for
# the named context. Docker derives the sub-directory from sha256(name).
docker_context_dir() {
    local name="$1"
    local hash
    # sha256sum on Linux, shasum on macOS
    if command -v sha256sum &>/dev/null; then
        hash=$(printf '%s' "${name}" | sha256sum | awk '{print $1}')
    else
        hash=$(printf '%s' "${name}" | shasum -a 256 | awk '{print $1}')
    fi
    echo "${HOME}/.docker/contexts/meta/${hash}"
}

@test "moby engine creates Docker context for the instance" {
    # Restart with moby (the containerd test above may have left the engine in
    # containerd mode).
    rdd set running=true containerEngine.name=moby
    rdd ctl wait --for=condition=ContainerEngineReady \
        app/app --timeout=30s

    local context_name="rancher-desktop-${RDD_INSTANCE}"
    local meta_file
    meta_file="$(docker_context_dir "${context_name}")/meta.json"

    assert_file_exists "${meta_file}"

    run -0 jq -r '.Name' "${meta_file}"
    assert_output "${context_name}"

    run -0 rdd service paths docker_socket
    local socket_path=${output}
    run -0 jq -r '.Endpoints.docker.Host' "${meta_file}"
    assert_output "unix://${socket_path}"
}

@test "moby engine sets currentContext when no healthy context exists" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"

    # Save and clear any existing currentContext so the probe finds no
    # healthy context and promotes ours. Restored in teardown.
    local saved_context
    saved_context=$(jq -r '.currentContext // empty' "${HOME}/.docker/config.json" 2>/dev/null || true)

    # Clear the current context and restart the engine so the probe runs fresh.
    jq 'del(.currentContext)' "${HOME}/.docker/config.json" >"${HOME}/.docker/config.json.tmp" &&
        mv "${HOME}/.docker/config.json.tmp" "${HOME}/.docker/config.json"

    rdd set running=false
    rdd set running=true containerEngine.name=moby
    rdd ctl wait --for=condition=ContainerEngineReady app/app --timeout=30s

    # The probe goroutine runs asynchronously; give it a moment.
    try --max 6 --delay 1 -- \
        bash -c "jq -r '.currentContext' '${HOME}/.docker/config.json' | grep -qx '${context_name}'"

    run -0 jq -r '.currentContext' "${HOME}/.docker/config.json"
    assert_output "${context_name}"

    # Restore the original context if there was one.
    if [[ -n "${saved_context}" ]]; then
        jq --arg ctx "${saved_context}" '.currentContext = $ctx' \
            "${HOME}/.docker/config.json" >"${HOME}/.docker/config.json.tmp" &&
            mv "${HOME}/.docker/config.json.tmp" "${HOME}/.docker/config.json"
    fi
}

@test "stopping moby engine removes Docker context and clears currentContext" {
    rdd set running=false

    local context_name="rancher-desktop-${RDD_INSTANCE}"
    run_e -0 docker_context_dir "${context_name}"
    local context_dir="${output}"

    # removeDockerContext runs asynchronously after the reconciler processes
    # the Running=False transition; poll until the directory is gone.
    try --max 10 --delay 1 -- test ! -d "${context_dir}"
    assert_dir_not_exists "${context_dir}"

    # currentContext should either be gone or point to something else.
    run jq -r '.currentContext // empty' "${HOME}/.docker/config.json"
    refute_output "${context_name}"
}
