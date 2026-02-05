load '../../helpers/load'

# Controller testing using CRD presence validation

CRD_GROUP=customresourcedefinition.apiextensions.k8s.io # spellchecker:ignore

get_controllers() {
    run -0 rdd ctl get CustomResourceDefinition --output=name
    # If `$output` is empty, `refute_lines` will fail because `$lines` is unset.
    if [[ -z "${output}" ]]; then
        run -0 echo '<no output>'
    fi
}

check_rdd_controllers() {
    local assert=$1
    "${assert}_line" "${CRD_GROUP}/notaries.rdd.rancherdesktop.io"
    "${assert}_line" "${CRD_GROUP}/configmapreplicasets.rdd.rancherdesktop.io"
}

check_app_controllers() {
    local assert=$1
    "${assert}_line" "${CRD_GROUP}/demos.app.rancherdesktop.io"
}

assert_only_rdd_controllers() {
    get_controllers
    check_rdd_controllers assert
    check_app_controllers refute
}

assert_only_app_controllers() {
    get_controllers
    check_app_controllers assert
    check_rdd_controllers refute
}

assert_all_controllers() {
    get_controllers
    check_rdd_controllers assert
    check_app_controllers assert
}

assert_no_controllers() {
    # Essential APIs should work but no controllers
    run -0 rdd ctl get namespaces
    get_controllers
    check_rdd_controllers refute
    check_app_controllers refute
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
    assert_file_exist "${ARGS_JSON}"
    assert_file_contains "${ARGS_JSON}" '"--controllers"'
    assert_file_contains "${ARGS_JSON}" '"rdd"'
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
