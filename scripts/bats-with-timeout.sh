#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Run a command with a timeout. If it times out, capture diagnostic state
# (process tree, open fds, kernel wait channels, goroutine dumps) before
# killing it. The bundle is written to the current instance's log_dir so
# collect-bats-logs.sh picks it up with the other logs.
#
# Usage: bats-with-timeout.sh <seconds> <label> <command> [args...]

set -o errexit -o nounset -o pipefail

timeout_seconds=$1
label=$2
shift 2

# Compute log_dir without calling rdd (it may be hung). Must match
# pkg/instance/instance.go LogDir().
instance_name="rancher-desktop-${RDD_INSTANCE:-2}"
case "$(uname -s)" in
    Linux)   log_dir="${HOME}/.local/state/${instance_name}" ;;
    Darwin)  log_dir="${HOME}/Library/Logs/${instance_name}" ;;
    MINGW*|MSYS*|CYGWIN*) log_dir="${LOCALAPPDATA:-${HOME}/AppData/Local}/${instance_name}-logs" ;;
    *)       log_dir="${HOME}/${instance_name}-logs" ;;
esac
mkdir -p "${log_dir}"
bundle_file="${log_dir}/support-bundle.log"

is_interesting_process() {
    case "$1" in
        bash|sh|bats|rdd|rdd.exe|qemu*|lima*|hostagent|*-controller|*-controller.exe)
            return 0
            ;;
    esac
    return 1
}

# SIGQUIT triggers Go's runtime goroutine dump to stderr.
dump_goroutines() {
    while read -r pid comm; do
        case "$comm" in
            rdd|rdd.exe|*-controller|*-controller.exe|lima-guestagent|hostagent)
                echo "SIGQUIT -> pid=${pid} comm=${comm}"
                kill -QUIT "$pid" 2>/dev/null || true
                ;;
        esac
    done < <(ps -axo pid=,ucomm= 2>/dev/null || ps -eo pid=,ucomm= 2>/dev/null || true)
}

dump_linux_proc() {
    for pid_dir in /proc/[0-9]*; do
        [ -d "$pid_dir" ] || continue
        pid=${pid_dir##*/}
        comm=$(tr -d '\0' <"$pid_dir/comm" 2>/dev/null || echo "?")
        if ! is_interesting_process "$comm"; then
            continue
        fi
        echo
        echo "--- pid=${pid} comm=${comm} ---"
        echo "state: $(grep -m1 ^State "$pid_dir/status" 2>/dev/null || echo ?)"
        echo "wchan: $(cat "$pid_dir/wchan" 2>/dev/null || echo ?)"
        echo "cmdline: $(tr '\0' ' ' <"$pid_dir/cmdline" 2>/dev/null || echo ?)"
        echo "fds:"
        ls -l "$pid_dir/fd/" 2>/dev/null | sed 's/^/  /' | head -30 || true
    done
}

dump_macos_ps() {
    # macOS has no /proc; use ps and lsof instead.
    # ucomm gives the basename (comm gives the full path on macOS).
    local pids=()
    while read -r pid comm; do
        if is_interesting_process "$comm"; then
            pids+=("$pid")
        fi
    done < <(ps -axo pid=,ucomm= 2>/dev/null || true)

    for pid in "${pids[@]}"; do
        echo
        echo "--- pid=${pid} ---"
        ps -p "$pid" -o pid,stat,wchan,command 2>/dev/null | sed 's/^/  /' || true
        if command -v lsof >/dev/null 2>&1; then
            echo "fds (lsof):"
            lsof -p "$pid" 2>/dev/null | head -30 | sed 's/^/  /' || true
        fi
        # `sample` captures user-space call stacks on macOS without attaching.
        if command -v sample >/dev/null 2>&1; then
            echo "sample (1s):"
            sample "$pid" 1 -mayDie 2>/dev/null | head -40 | sed 's/^/  /' || true
        fi
    done
}

dump_sockets() {
    if command -v ss >/dev/null 2>&1; then
        echo "=== Open sockets (ss -tupn) ==="
        ss -tupn 2>&1 | head -50 || true
    elif command -v lsof >/dev/null 2>&1; then
        echo "=== Open sockets (lsof -iP) ==="
        lsof -iP 2>&1 | head -50 || true
    elif command -v netstat >/dev/null 2>&1; then
        echo "=== Open sockets (netstat -an) ==="
        netstat -an 2>&1 | head -50 || true
    fi
}

dump() {
    {
        echo "=== Support bundle for ${label} at $(date -Iseconds 2>/dev/null || date) ==="
        echo "uname: $(uname -a)"

        echo
        echo "=== ps ==="
        # ps auxf is Linux-only; fall back to plain aux on macOS/BSD.
        ps auxf 2>/dev/null || ps aux 2>&1 || true

        echo
        echo "=== Go processes: SIGQUIT dump ==="
        # Goroutine stacks go to each process's stderr (typically a log file).
        # We note the pids here; the dumps land in the collected rdd.stderr logs.
        dump_goroutines

        echo
        echo "=== Per-process state ==="
        if [ -d /proc ]; then
            dump_linux_proc
        else
            dump_macos_ps
        fi

        echo
        dump_sockets

        echo
        echo "=== End of support bundle ==="
    } >>"${bundle_file}" 2>&1
}

# Start the command in the background.
"$@" &
cmd_pid=$!

# Poll for the deadline or command completion. Must run in the main shell
# so we can reliably wait for both the dump and the command to finish
# before exiting (a backgrounded watchdog would be killed mid-dump when
# the main shell exits).
deadline=$(($(date +%s) + timeout_seconds))
while kill -0 "${cmd_pid}" 2>/dev/null; do
    if [ "$(date +%s)" -ge "${deadline}" ]; then
        echo "bats-with-timeout: ${label} exceeded ${timeout_seconds}s, capturing support bundle" >&2
        dump
        echo "bats-with-timeout: sending SIGTERM to ${cmd_pid}" >&2
        kill -TERM "${cmd_pid}" 2>/dev/null || true
        # Give it 30s to shut down gracefully, then SIGKILL.
        kill_deadline=$(($(date +%s) + 30))
        while kill -0 "${cmd_pid}" 2>/dev/null; do
            if [ "$(date +%s)" -ge "${kill_deadline}" ]; then
                echo "bats-with-timeout: sending SIGKILL to ${cmd_pid}" >&2
                kill -KILL "${cmd_pid}" 2>/dev/null || true
                break
            fi
            sleep 1
        done
        break
    fi
    sleep 1
done

exit_code=0
wait "${cmd_pid}" || exit_code=$?
exit "${exit_code}"
