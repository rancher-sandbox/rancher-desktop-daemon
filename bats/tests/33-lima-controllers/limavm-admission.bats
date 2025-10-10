load '../../helpers/load'

# LimaVM admission controller tests - tests webhook validation for cross-namespace
# uniqueness enforcement. LimaVM names must be unique across all namespaces
# because they correspond to actual VM instances on the host system.

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "test-ns1"
    rdd ctl create namespace "test-ns2"
    rdd ctl create namespace "test-ns3"
}

# create_limavm_yaml <name> <namespace> [labels]
# Example: create_limavm_yaml "my-vm" "test-ns1" "updated=true"
create_limavm_yaml() {
    local name=$1
    local namespace=$2
    local labels=${3:-}

    cat <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: ${name}
  namespace: ${namespace}
EOF

    if [[ -n ${labels} ]]; then
        cat <<EOF
  labels:
    ${labels}
EOF
    fi

    cat <<EOF
spec: {}
EOF
}

# Usage: apply_limavm <name> <namespace> [labels]
# Example: apply_limavm "my-vm" "test-ns1" "updated=true" "--dry-run=server"
apply_limavm() {
    local name=$1
    local namespace=$2
    local labels=${3:-}

    run -0 create_limavm_yaml "${name}" "${namespace}" "${labels}"
    rdd ctl apply -f - <<<"$output"
}

@test "webhook configuration has correct structure" {
    # Wait for the webhook configuration to be created
    try --max 20 --delay 3 -- rdd ctl get ValidatingWebHookConfiguration limavm-validator

    run -0 rdd ctl get ValidatingWebHookConfiguration limavm-validator -o jsonpath='{.webhooks[0]}'
    local json=$output

    run -0 jq_raw '.failurePolicy' "$json"
    assert_output "Fail"

    run -0 jq_raw ".name" "$json"
    assert_output "limavm.lima.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiGroups[0]' "$json"
    assert_output "lima.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiVersions[0]' "$json"
    assert_output "v1alpha1"

    run -0 jq_raw '.rules[0].resources[0]' "$json"
    assert_output "limavms"

    run -0 jq_raw '.rules[0].operations[]' "$json"
    assert_line "CREATE"
    assert_line "UPDATE"
}

@test "create LimaVM in first namespace succeeds" {
    # Create first LimaVM in test-ns1
    run -0 apply_limavm "my-vm" "test-ns1"
    assert_output --partial "my-vm created"

    # Verify the LimaVM was created
    run -0 rdd ctl get limavm "my-vm" -n "test-ns1" -o name
    assert_line "limavm.lima.rancherdesktop.io/my-vm"
}

@test "duplicate LimaVM name in different namespace is rejected" {
    # Try to create LimaVM with same name in test-ns2
    run -1 apply_limavm "my-vm" "test-ns2"
    assert_output --partial "Forbidden"
    assert_output --partial 'LimaVM name "my-vm" is already used in namespace "test-ns1"'
    assert_output --partial "LimaVM names must be unique across all namespaces"

    # Verify the LimaVM was NOT created in test-ns2
    run -1 rdd ctl get limavm my-vm -n test-ns2
    assert_output --partial "not found"
}

@test "duplicate LimaVM name in same namespace is updating the existing instance" {
    # Update the existing LimaVM in test-ns1 (same name, same namespace)
    run -0 apply_limavm "my-vm" "test-ns1" 'updated: "true"'
    assert_output --partial "my-vm configured"

    # Verify the label was added
    run -0 rdd ctl get limavm my-vm -n test-ns1 -o jsonpath='{.metadata.labels.updated}'
    assert_output "true"
}

@test "dry-run=server rejects duplicate names" {
    # Try to create duplicate name with dry-run=server
    run -0 create_limavm_yaml "my-vm" "test-ns2"
    run -1 rdd ctl apply --dry-run=server -f - <<<"$output"
    assert_output --partial "Forbidden"
    assert_output --partial 'LimaVM name "my-vm" is already used in namespace "test-ns1"'

    # Verify the resource was not created
    run -1 rdd ctl get limavm my-vm -n test-ns2
    assert_output --partial "not found"
}

@test "deletion allows recreation in different namespace" {
    # Delete the LimaVM from test-ns1
    run -0 rdd ctl delete limavm my-vm -n test-ns1
    assert_output --partial "deleted"

    # Verify deletion succeeded
    run -1 rdd ctl get limavm my-vm -n test-ns1
    assert_output --partial "not found"

    # Now we should be able to create a LimaVM with the same name in test-ns2
    run -0 apply_limavm "my-vm" "test-ns2"
    assert_output --partial "my-vm created"

    # Verify the LimaVM was created in test-ns2
    run -0 rdd ctl get limavm my-vm -n test-ns2
    assert_output --partial "my-vm"
}

@test "multiple unique VMs across namespaces are allowed" {
    # Create several LimaVMs with unique names across different namespaces

    run -0 apply_limavm "vm1" "test-ns1"
    assert_output --partial "vm1 created"

    run -0 apply_limavm "vm2" "test-ns2"
    assert_output --partial "vm2 created"

    run -0 apply_limavm "vm3" "test-ns3"
    assert_output --partial "vm3 created"

    # Verify all VMs exist
    run -0 rdd ctl get limavms -A
    assert_output --partial "vm1"
    assert_output --partial "vm2"
    assert_output --partial "vm3"
}
