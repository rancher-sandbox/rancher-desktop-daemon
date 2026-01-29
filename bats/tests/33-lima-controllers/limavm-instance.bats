# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# LimaVM instance tests verify that Lima instances are created on disk
# after template verification, and deleted when the LimaVM is deleted.

NAMESPACE="instance-test-ns"
VM_NAME="alpine-test"

# Use a real Alpine ISO template that instance.Create() can use.
# This is a minimal template with actual image URLs.
# vmType defaults to vz on macOS and qemu on Linux.
ALPINE_TEMPLATE='images:
- location: https://github.com/lima-vm/alpine-lima/releases/download/v0.2.47/alpine-lima-std-3.23.0-x86_64.iso
  arch: x86_64
  digest: sha512:c71e21dfb152642dd79af281497f86e7f690998997f787307978d83594e5e47addbc61e7d8ee405b0afc4230688de9eeb98fa44d6e74654e8d9d8b70151fb8da
- location: https://github.com/lima-vm/alpine-lima/releases/download/v0.2.47/alpine-lima-std-3.23.0-aarch64.iso
  arch: aarch64
  digest: sha512:44659e71c1e277361bc10ecc813fba799d0371c2bc291db811e05e43429fd31aaa7ebe185331d02dccadccddfe9d54184376cdceeb1c444b58e3e9a4e690ce33
mounts:
- location: "~"
containerd:
  system: false
  user: false'

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "${NAMESPACE}"
}

local_teardown_file() {
    # Clean up any remaining Lima instances
    for vm in "${VM_NAME}" "invalid-vm"; do
        if [[ -d "${LIMA_HOME}/${vm}" ]]; then
            rm -rf "${LIMA_HOME:?}/${vm}"
        fi
    done
}

local_setup() {
    skip_on_windows
}

create_limavm() {
    local name=$1
    local template=$2

    rdd ctl apply -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
spec:
  templateRef:
    name: ${template}
    namespace: ${NAMESPACE}
  running: false
EOF
}

lima_instance_exists() {
    local name=$1
    [[ -d "${LIMA_HOME}/${name}" ]]
}

assert_instance_created() {
    local name=$1
    run -0 rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].status}'
    assert_output "True"
}

assert_instance_create_failed() {
    local name=$1
    run -0 rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].status}'
    assert_output "False"
}

@test "create source template ConfigMap with Alpine ISO" {
    rdd ctl create configmap "alpine-source" --namespace "${NAMESPACE}" --from-literal="template=${ALPINE_TEMPLATE}"

    run -0 rdd ctl get configmap "alpine-source" --namespace "${NAMESPACE}" --output jsonpath='{.data.template}'
    assert_output --partial "alpine-lima"
}

@test "create LimaVM with Alpine template" {
    create_limavm "${VM_NAME}" "alpine-source"

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for template ConfigMap to be created" {
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}' \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout="30s"
}

@test "wait for InstanceCreated condition to be True" {
    try --max 60 --delay 1 -- assert_instance_created "${VM_NAME}"
}

@test "verify Lima instance directory exists" {
    try --max 30 --delay 1 -- lima_instance_exists "${VM_NAME}"
}

@test "verify Lima instance has lima.yaml file" {
    assert_file_exists "${LIMA_HOME}/${VM_NAME}/lima.yaml"
}

@test "verify Lima instance has basedisk" {
    assert_file_exists "${LIMA_HOME}/${VM_NAME}/basedisk"
}

@test "verify Lima instance has diffdisk" {
    assert_file_exists "${LIMA_HOME}/${VM_NAME}/diffdisk"
}

@test "verify InstanceCreated condition has correct reason" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].reason}'
    assert_output "Created"
}

@test "verify InstanceCreated condition has message" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].message}'
    # Message is "Lima instance created successfully" or "Lima instance exists"
    assert_output --partial "Lima instance"
}

@test "delete LimaVM resource" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}" --grace-period=0
}

@test "verify LimaVM is deleted" {
    run -1 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    assert_output --partial "not found"
}

@test "verify Lima instance is deleted from disk" {
    try --max 30 --delay 1 --until-fail -- lima_instance_exists "${VM_NAME}"
}

# Test that leftover instances from failed deletions are cleaned up

@test "create fake leftover Lima instance" {
    mkdir -p "${LIMA_HOME}/${VM_NAME}"
    echo "leftover: true" >"${LIMA_HOME}/${VM_NAME}/lima.yaml"
    echo "0.0.0" >"${LIMA_HOME}/${VM_NAME}/lima-version"
    assert_file_exists "${LIMA_HOME}/${VM_NAME}/lima.yaml"
}

@test "create LimaVM with leftover instance present" {
    create_limavm "${VM_NAME}" "alpine-source"
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for InstanceCreated after leftover cleanup" {
    try --max 60 --delay 1 -- assert_instance_created "${VM_NAME}"
}

@test "verify leftover was replaced with real instance" {
    # The fake leftover had "leftover: true", real instance should not
    run -0 cat "${LIMA_HOME}/${VM_NAME}/lima.yaml"
    refute_output --partial "leftover"
    # Real instance should have images from template
    assert_output --partial "alpine-lima"
}

@test "cleanup LimaVM after leftover test" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}" --grace-period=0
    try --max 30 --delay 1 --until-fail -- lima_instance_exists "${VM_NAME}"
}

# Test that invalid image URL causes InstanceCreated condition to be False

INVALID_TEMPLATE='images:
- location: https://invalid.example.com/nonexistent.iso
  arch: x86_64
- location: https://invalid.example.com/nonexistent.iso
  arch: aarch64'

@test "create ConfigMap with invalid image URL" {
    rdd ctl create configmap "invalid-image" --namespace "${NAMESPACE}" --from-literal="template=${INVALID_TEMPLATE}"
}

@test "create LimaVM with invalid image URL" {
    create_limavm "invalid-vm" "invalid-image"
    run -0 rdd ctl get limavm "invalid-vm" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/invalid-vm"
}

@test "wait for InstanceCreated condition to be False" {
    try --max 30 --delay 1 -- assert_instance_create_failed "invalid-vm"
}

@test "verify InstanceCreated condition has CreateFailed reason" {
    run -0 rdd ctl get limavm "invalid-vm" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].reason}'
    assert_output "CreateFailed"
}

@test "verify InstanceCreated condition message contains error details" {
    run -0 rdd ctl get limavm "invalid-vm" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].message}'
    assert_output --partial "invalid.example.com"
}

@test "cleanup LimaVM with invalid image" {
    rdd ctl delete limavm "invalid-vm" --namespace "${NAMESPACE}" --ignore-not-found --grace-period=0
}
