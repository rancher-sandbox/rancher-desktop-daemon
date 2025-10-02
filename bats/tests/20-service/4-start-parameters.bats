load '../../helpers/load'

# Controller testing using CRD presence validation

assert_rdd_controllers() {
    run -0 rdd ctl get CustomResourceDefinition notaries.rdd.rancherdesktop.io
    assert_output --partial "notaries.rdd.rancherdesktop.io"

    run -0 rdd ctl get CustomResourceDefinition configmapreplicasets.rdd.rancherdesktop.io
    assert_output --partial "configmapreplicasets.rdd.rancherdesktop.io"
}

assert_app_controllers() {
    run -0 rdd ctl get CustomResourceDefinition demos.app.rancherdesktop.io
    assert_output --partial "demos.app.rancherdesktop.io"
}

refute_rdd_controllers() {
    run -1 rdd ctl get CustomResourceDefinition notaries.rdd.rancherdesktop.io
    assert_output --partial "NotFound"

    run -1 rdd ctl get CustomResourceDefinition configmapreplicasets.rdd.rancherdesktop.io
    assert_output --partial "NotFound"
}

refute_app_controllers() {
    run -1 rdd ctl get CustomResourceDefinition demos.app.rancherdesktop.io
    assert_output --partial "NotFound"
}

assert_only_rdd_controllers() {
    assert_rdd_controllers
    refute_app_controllers
}

assert_only_app_controllers() {
    assert_app_controllers
    refute_rdd_controllers
}

assert_all_controllers() {
    assert_rdd_controllers
    assert_app_controllers
}

assert_no_controllers() {
    # Essential APIs should work but no controllers
    run -0 rdd ctl get namespaces
    refute_rdd_controllers
    refute_app_controllers
}

@test 'test instance with no controllers parameter' {
    run -0 rdd svc delete
    run -0 rdd svc create --controllers=""
    run -0 rdd svc start
    assert_no_controllers
}

@test 'create instance with specific controllers parameter' {
    run -0 rdd svc delete
    run -0 rdd svc create --controllers="rdd"

    # Verify parameters were saved
    ARGS_JSON="${PATH_APP_HOME}/args.json"
    assert_file_exist "$ARGS_JSON"
    assert_file_contains "$ARGS_JSON" '"--controllers"'
    assert_file_contains "$ARGS_JSON" '"rdd"'
}

@test 'start instance uses saved parameters by default' {
    run -0 rdd svc start
    assert_only_rdd_controllers
}

@test 'start instance with parameter override' {
    run -0 rdd svc stop
    run -0 rdd svc start --controllers="*"
    assert_all_controllers
}

@test 'start instance with controller parameter override' {
    run -0 rdd svc stop
    run -0 rdd svc start --controllers="app"
    assert_only_app_controllers
}

@test 'start instance returns to default parameters after override' {
    run -0 rdd svc stop
    # Start with default parameters (should use saved rdd controllers)
    run -0 rdd svc start
    assert_only_rdd_controllers
}
