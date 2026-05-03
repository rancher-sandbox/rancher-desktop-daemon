// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package kuberlr

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	componentbasever "k8s.io/component-base/version"
)

// envSkipResolver tells Resolve to short-circuit. Exec sets it on the
// kubectl child process so a downloaded kubectl that re-execs us through
// a shim cannot recurse back into version resolution.
const envSkipResolver = "RDD_KUBECTL_RESOLVED"

// skipResolver short-circuits Resolve for the rest of this process.
// Same-process toggle; envSkipResolver covers the cross-process recursion
// guard that Exec sets on the kubectl child.
var skipResolver bool

// SkipResolver short-circuits Resolve for the rest of this process.
// rdd ctl calls this before kubectlAction because it always targets
// the embedded apiserver, whose version matches the embedded kubectl
// by construction — the probe would always fall through anyway.
func SkipResolver() {
	skipResolver = true
}

// serverProbeTimeout caps the discovery call. A reachable cluster answers
// in under 100 ms; an unreachable one would otherwise stall every kubectl
// invocation.
const serverProbeTimeout = 2 * time.Second

// Resolve returns the path to a kubectl binary compatible with the cluster
// the user's kubeconfig points at. An empty path means "use the embedded
// kubectl"; a non-empty path means "exec this binary instead". args holds
// the kubectl-style arguments the caller will pass through; Resolve binds
// kubectl's connection-override flags (via genericclioptions.ConfigFlags)
// so the version probe targets the same cluster the kubectl child will
// contact. The deprecated --username and --password basic-auth flags
// stay unbound on the resolver side; see parseKubeConfigFlags for the
// rationale and the rest of the flag surface.
//
// When the cluster probe fails for any reason — unreachable, missing or
// malformed kubeconfig, unparseable server version — or when the embedded
// kubectl is within ±1 minor of the server, Resolve returns "". Resolve
// also returns "" without probing for invocations the embedded kubectl
// can answer on its own (config, completion, options, help, --help, -h,
// version --client) so client-only commands skip the network round trip
// when the cluster is unreachable.
//
// TODO(offline): when no cached kubectl matches and the network is down,
// Resolve currently fails with the download error. Add an opt-out for
// download attempts (config flag or RDD_KUBECTL_OFFLINE) and fall back to
// the cached binary closest to the server's minor.
func Resolve(ctx context.Context, args []string) (string, error) {
	if skipResolver || os.Getenv(envSkipResolver) != "" {
		return "", nil
	}
	if isClientOnly(args) {
		return "", nil
	}
	embedded, err := embeddedVersion()
	if err != nil {
		// `go run ./cmd/rdd kubectl ...` and IDE debug builds skip the
		// Makefile's -ldflags -X, so componentbasever returns the
		// in-tree default `v0.0.0-master+$Format:%H$`, which fails
		// semver parsing. Fall through to the embedded kubectl rather
		// than break every dev invocation.
		logrus.WithError(err).Debug("kubectl resolver: embedded version not parseable; using embedded kubectl")
		return "", nil
	}
	server, ok := serverVersion(args)
	if !ok {
		return "", nil
	}
	if compatible(embedded, server) {
		return "", nil
	}
	return ensureCached(ctx, server)
}

// compatible reports whether a kubectl of client.Minor can drive a server
// of server.Minor under the standard Kubernetes skew of ±1 minor. A
// different Major rules out compatibility outright.
func compatible(client, server semver.Version) bool {
	if client.Major != server.Major {
		return false
	}
	diff := int64(client.Minor) - int64(server.Minor)
	return diff >= -1 && diff <= 1
}

// embeddedVersion reads the kubectl version baked into the rdd binary
// from k8s.io/component-base/version, populated by -X linker flags in
// the Makefile. Declared as a var so unit tests can swap it in to
// exercise the parse-failure fall-through that `go run` and IDE debug
// builds hit at runtime.
var embeddedVersion = func() (semver.Version, error) {
	return semver.ParseTolerant(componentbasever.Get().GitVersion)
}

// serverVersion contacts the cluster named by args (and the user's
// kubeconfig) and parses its reported version. ok is false on every
// failure path — missing kubeconfig, malformed config, unreachable
// cluster, unparseable version — because each is a legitimate reason
// to skip the resolver and fall through to the embedded kubectl. The
// three routine paths log at debug; the unparseable-version path logs
// at warn because an apiserver answering /version with garbage is the
// kind of surprise an operator wants to see at the default log level.
func serverVersion(args []string) (semver.Version, bool) {
	cfg, err := loadClientConfig(args)
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cannot load kubeconfig; using embedded kubectl")
		return semver.Version{}, false
	}
	cfg.Timeout = serverProbeTimeout
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cannot build discovery client; using embedded kubectl")
		return semver.Version{}, false
	}
	info, err := client.ServerVersion()
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cluster probe failed; using embedded kubectl")
		return semver.Version{}, false
	}
	v, err := semver.ParseTolerant(info.GitVersion)
	if err != nil {
		logrus.WithError(err).Warnf("kubectl resolver: cannot parse server version %q; using embedded kubectl", info.GitVersion)
		return semver.Version{}, false
	}
	return v, true
}

// loadClientConfig builds a rest.Config from KUBECONFIG plus the kubectl
// connection-override flags in args. The resolver and the kubectl child
// must aim at the same cluster so the version skew check matches the
// cluster kubectl actually contacts; reusing kubectl's own flag binding
// (genericclioptions.ConfigFlags) keeps them aligned by construction.
func loadClientConfig(args []string) (*rest.Config, error) {
	return parseKubeConfigFlags(args).ToRawKubeConfigLoader().ClientConfig()
}

// parseKubeConfigFlags binds genericclioptions.NewConfigFlags(true) to
// a pflag FlagSet, parses args, and returns the populated flags. The
// bound surface is whatever NewConfigFlags(true).AddFlags exposes —
// the kubectl connection-override flags (--kubeconfig, --context,
// --server/-s, --cluster, --user, --token, --certificate-authority,
// --client-certificate, --client-key, --tls-server-name,
// --insecure-skip-tls-verify, --request-timeout, --as, --as-group,
// --as-uid, --as-user-extra, --cache-dir, --disable-compression,
// --namespace/-n). Unknown flags (kubectl subcommands and their own
// flags) and positional arguments pass through silently.
//
// The deprecated --username and --password flags stay unbound. Binding
// them via WithDeprecatedPasswordFlag would make ToRawKubeConfigLoader
// return the interactive variant that prompts on stdin when the
// kubeconfig context lacks credentials — the wrong behavior for a
// background version probe. The kubectl child still receives those
// flags and processes them per its own binding; the asymmetry only
// affects the resolver's probe, which falls through silently to the
// embedded kubectl on probe failure either way.
//
// parseKubeConfigFlags exists as a separate helper so unit tests can
// verify the binding without reaching for a real cluster.
func parseKubeConfigFlags(args []string) *genericclioptions.ConfigFlags {
	fs := pflag.NewFlagSet("rdd-kubectl", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	cf := genericclioptions.NewConfigFlags(true)
	cf.AddFlags(fs)
	_ = fs.Parse(args)
	return cf
}

// clientOnlySubcommands lists kubectl subcommands that never contact
// a cluster. config and completion manipulate local state only;
// kustomize renders local manifests; plugin lists local plugins on
// PATH; options prints kubectl's global flags; help prints help text.
var clientOnlySubcommands = map[string]bool{
	"completion": true,
	"config":     true,
	"kustomize":  true,
	"plugin":     true,
	"options":    true,
	"help":       true,
}

// isClientOnly reports whether args describe a kubectl invocation
// the resolver can skip without losing correctness. Empty args, the
// help flags, "version --client", and the subcommands in
// clientOnlySubcommands all qualify; everything else falls through
// to the probe so the version skew check still runs.
//
// The walk binds genericclioptions.ConfigFlags (kubectl's connection
// overrides) and the bare-bool kubectl globals (--help/-h, --client,
// --warnings-as-errors) into a per-call pflag FlagSet. Klog flags
// (--v, --vmodule, --log_dir, …) intentionally stay unbound so their
// values do not leak into klog's process-global state; pflag's
// UnknownFlags handling consumes the assumed value of any spaced
// unknown flag, so `--v 4 config view` correctly leaves [config,
// view] as positionals.
//
// Bool-parse errors short-circuit pflag's parser: a malformed value
// like `--client=garbage version` halts parse mid-args, leaves no
// positionals, and lands in the empty-positionals branch below
// (return true). This deviates from a strict conservative bias, but
// kubectl rejects the same malformed flag in the embedded path
// before contacting any cluster, so no silent mismatched execution
// follows.
//
// Conservative bias: when isClientOnly cannot identify the
// invocation, prefer to probe; misclassifying as client-only would
// skip the version skew check and silently use a mismatched embedded
// kubectl.
func isClientOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	fs := pflag.NewFlagSet("rdd-kubectl", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	cf := genericclioptions.NewConfigFlags(true)
	cf.AddFlags(fs)

	var help, client, warningsAsErrors, matchServerVersion bool
	fs.BoolVarP(&help, "help", "h", false, "")
	fs.BoolVar(&client, "client", false, "")
	fs.BoolVar(&warningsAsErrors, "warnings-as-errors", false, "")
	fs.BoolVar(&matchServerVersion, "match-server-version", false, "")

	_ = fs.Parse(args)

	if help {
		return true
	}
	positionals := fs.Args()
	if len(positionals) == 0 {
		return true
	}
	subcommand := positionals[0]
	if subcommand == "version" {
		// `kubectl version` (no --client) probes the server for its
		// version. Treat that as cluster-bound so the resolver picks
		// a version-matched binary to do the probe.
		return client
	}
	return clientOnlySubcommands[subcommand]
}
