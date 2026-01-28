load '../../helpers/load'

# Mock controller tests - using the mock controller, verify that the container
# and image controllers work as expected.

TEST_DATA_PATH="${PATH_BATS_ROOT}/../pkg/controllers/mock/testdata"

local_setup_file() {
    setup_rdd_control_plane "containers"
    echo "${PATH_LOGS}/mock-controller.log" >&3
    "mock-controller${EXE}" &>"${PATH_LOGS}/mock-controller.log" &
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"
}

local_teardown_file() {
    if [[ -f "${BATS_FILE_TMPDIR}/controller_pid" ]]; then
        read -r controller_pid <"${BATS_FILE_TMPDIR}/controller_pid"
        kill "${controller_pid}" 2>/dev/null || true
        wait "${controller_pid}" 2>/dev/null || true
    fi
}

@test "containers are created" {
    try --max 30 --delay 1 -- rdd ctl get namespace rdd-mocks -o name

    run -0 cat "${TEST_DATA_PATH}/containers.json"
    run -0 jq_output '.[].Id'
    containers=${output}

    while IFS= read -r container; do
        try --max 30 --delay 1 -- rdd ctl get container "${container}" -o name
        assert_line "container.containers.rancherdesktop.io/${container}"
    done <<<"${containers}"
}

@test "images are created" {
    try --max 30 --delay 1 -- rdd ctl get namespace rdd-mocks -o name

    run -0 cat "${TEST_DATA_PATH}/images.json"
    run -0 jq_output '.[].RepoTags.[]'
    images=${output}

    while IFS= read -r image; do
        try --max 30 --delay 1 -- rdd ctl get image --field-selector "status.repoTag=${image}" --output jsonpath='{.items[*].status.repoTag}'
        assert_line "${image}"
    done <<<"${images}"
}

@test "volumes are created" {
    try --max 30 --delay 1 -- rdd ctl get namespace rdd-mocks -o name

    run -0 cat "${TEST_DATA_PATH}/volumes.json"
    run -0 jq_output '.[].Name'
    volumes=${output}

    while IFS= read -r volume; do
        try --max 30 --delay 1 -- rdd ctl get volume "${volume}" -o name
        assert_line "volume.containers.rancherdesktop.io/${volume}"
    done <<<"${volumes}"
}
