# PATH_BATS_ROOT, PATH_BATS_LOGS, and PATH_BATS_HELPERS are already set by load.bash

PATH_REPO_ROOT=$(absolute_path "${PATH_BATS_ROOT}/..")

inside_repo_clone() {
    [[ -d "${PATH_REPO_ROOT}/pkg/rancher-desktop-daemon" ]]
}

# Convert 'export KEY="C:\win\path"' to 'export KEY="/mnt/c/wsl/path"'.
win_to_wsl_exports() {
    tr -d "\r" | while IFS= read -r line; do
        if [[ "${line}" =~ ^(export\ [A-Z_]+=)\"(.+)\"$ ]]; then
            printf '%s"%s"\n' "${BASH_REMATCH[1]}" "$(wslpath -u "${BASH_REMATCH[2]}")"
        fi
    done
}

# Get instance paths from rdd (single source of truth).
# shellcheck disable=SC1090
if is_windows; then
    source <("${PATH_REPO_ROOT}/bin/rdd.exe" svc paths --shell | win_to_wsl_exports)
else
    source <("${PATH_REPO_ROOT}/bin/rdd" svc paths --shell)
fi
