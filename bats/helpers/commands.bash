EXE=""
PLATFORM=$OS
if is_windows; then
    PLATFORM=linux
    if using_windows_exe; then
        EXE=".exe"
        PLATFORM=win32
    fi
fi

no_cr() {
    tr -d '\r'
}
ctrctl() {
    if using_docker; then
        docker "$@"
    else
        nerdctl "$@"
    fi
}
curl() {
    command "curl$EXE" "$@"
}
rdd() {
    local arg
    local args=("$@")
    local env
    local envs=()
    if is_windows; then
        args=()
        for arg in "$@"; do
            if [[ "${arg}" != "${arg#/mnt/}" ]]; then
                args+=("$(wslpath -w "$arg")")
            else
                args+=("$arg")
            fi
        done
        # Adjust WSLENV to include everything starting with RDD_
        mapfile -t envs < <({
            tr : '\n' <<<"$WSLENV"
            env | awk -F= '/^RDD_/ { print $1 }'
        } | sort -u)
        WSLENV=""
        for env in "${envs[@]}"; do
            WSLENV="${WSLENV}:${env}"
        done
        WSLENV=${WSLENV#:}

        export WSLENV
    fi
    "$PATH_REPO_ROOT/bin/rdd${EXE}" "${args[@]}"
}
