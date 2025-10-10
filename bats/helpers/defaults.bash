########################################################################
: "${RDD_INSTANCE=bats}"
export RDD_INSTANCE

: "${RDD_TRACE:=false}"
: "${RDD_NAMESPACE:=default}"

using_windows_exe() {
    true # TODO: WSL testing, later.
}
