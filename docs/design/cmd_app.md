# Application Commands

The application commands use short option names for usability. Unlike the `rdctl` tool from "Rancher Desktop 1" they will use e.g. `--cpus` instead of `--virtual-machine.number-cpus`.

## `rdd create`


## `rdd start`

```bash
rdd ctl patch app rancher-desktop --type=merge -p '{"spec":{"running":true}}'
```

## `rdd stop`

```bash
rdd ctl patch app rancher-desktop --type=merge -p '{"spec":{"running":false}}'
```


## `rdd delete`

Delete the `App` and all owned objects, like the `LimaVM` and the `K3sVersions`, etc.

Equivalent to

```bash
rdd ctl delete namespace rancher-desktop
```

## `rdd reset`

Set `App.status` to `stopped` and delete the `LimaVM`, but keep the `App` object. The VM will be recreated when the `spec.status` is set back to `running`.

## `rdd shell`

```bash
rdd lima shell rd "$@"
```

## `rdd run`

Set up `PATH` to start with `~/.rd$RDD_INSTANCE` and set the docker and kube contexts to `rancher-desktop` before running the command.

Since there are no environment variables for the contexts, it will have to set `DOCKER_HOST` and `KUBECONFIG` instead.

For example `rdd run docker images` will effectively execute:

```bash
export PATH="$HOME/.rd2/bin:$PATH"
export DOCKER_HOST="unix://$HOME/.rd2/docker.sock"
export KUBECONFIG="$HOME/.rd2/kube.config"
docker images
```

## `rdd shell-profile`

It prints a list of shell commands to STDOUT to put the `~/.rd2/bin` directory on the `PATH` and load completions for `rdd` and any bundled utilities (`docker`, `helm`, ...).

```console
$ rdd shell-profile bash --path --completions
export PATH="$HOME/.rd2/bin:$PATH"
source <(rdd completion bash)
source <(docker completions bash)
source <(helm completions bash)
...
```

This is also the command the "path management" inserts into the users shell profile, e.g.

```bash
source <(~/.rd2/bin/rdd shell-profile bash --path)
```

