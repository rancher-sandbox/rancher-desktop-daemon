# Rancher Desktop Containers API

> [!CAUTION]
> The Rancher Desktop Containers API is still in the concept stage and the details
will need to be ironed out.

The Rancher Desktop Containers API is a mostly read-only reflection of the
running container engine objects; unless otherwise noted, any modification will
be rejected by the controllers.

All objects are in the `containers.rancherdesktop.io` API group.

When running `containerd`, the containerd namespace is listed as the `namespace`
label rather than re-using the Kubernetes namespace.  When running `dockerd`,
namespaces are not supported and we always use `moby` as the value for that label.

This is mainly for use by the Rancher Desktop front end; all other users are
strongly urged to use the relevant CLI or other API instead.

## Containers

`Container` objects reflect the running containers.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Container
metadata:
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3 # container ID
  namespace: default
  labels:
    name: magical_gates
    namespace: k8s.io # containerd namespace, or `moby` if using dockerd
spec:
  path: /bin/sh
  args: [-c, 'sleep inf']
  image: "sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b"
  ports:
    - name: 80/tcp
      bindings:
      - hostIp: 0.0.0.0
        hostPort: 32768
      - hostIp: '::'
        hostPort: 32768
  labels:
    org.opensuse.base.vendor: openSUSE Project
  state: Running # Desired state
status:
  status: Running
  pid: 5059
  exitCode: 0
  error: ""
  createdAt: "2025-11-22T00:34:07.153640108Z" # Time
  startedAt: "2025-12-09T22:05:27.774478174Z"
  finishedAt: "2025-11-29T00:35:49.155454569Z"
  conditions:
  - type: Running
    status: True
  - type: Paused
    status: False
  - type: Restarting
    status: False
  - type: OOMKilled
    status: False
  - type: Dead
    status: False
```

If a `Container` object exists that does not actually exist in the container
engine, it is automatically deleted.  Therefore, creating `Container` objects
directly is not effective; to create containers, use `ContainerRequest`.

### Container actions

We will need to support a variety of actions on containers:

#### Create container

To create a container, create a `ContainerRequest` object:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ContainerRequest
metadata:
  generateName: whatever
  namespace: rdd-system
  labels:
    name: magical_gates
    namespace: k8s.io # containerd namespace
spec:
  # See Container.spec
status:
  # Resulting .metadata.name, which is the container ID.  It must be in the
  # same Kubernetes namespace as the ContainerRequest.
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3
  conditions:
  - type: Finished
    status: True
```

The `ContainerRequest` will be deleted soon after the `Finished` condition
transitions to `True`.

If multiple `ContainerRequest` objects exist at the same time for the same
contained `name`/`namespace` pair, the result is undefined.

#### Change container state
Set `.spec.state` to match how Kubernetes resources normally work.

#### Fetch container logs
The `@kubernetes/client-node` package has some hand-written code to deal with
logs; maybe we can make a copy of that (with a different endpoint) for this?

#### Exec (shell) in container
Same as logs; there's some special code in `@kubernetes/client-node` that we may
be able to fork.

#### Delete container
Create a `ContainerRequest` with `.spec.state` set to `deleted`.
We can't just delete the Kubernetes object, because it will be recreated as a
reflection of the containerd object.

Once the container has been removed from the container engine, the Kubernetes
object will be removed via normal reflection.

## Images

`Image` objects reflect images in the container engine.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Image
metadata:
  name: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b' # Image ID, colon replaced with dot.
  namespace: rdd-system # not the containerd namespace
spec:
  repoDigests:
  - registry.opensuse.org/opensuse/leap@sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b
  createdAt: "2025-11-17T03:14:16Z"
  architecture: arm64
  os: linux
  size: 45150437
  labels:
    org.opensuse.base.vendor: openSUSE Project
```

`ImageTag` objects are names for each `Image`.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImageTag
metadata:
  generateName: registry.opensuse.org-opensuse-leap-latest-
  namespace: rdd-system
  labels:
    name: 'registry.opensuse.org/opensuse/leap:latest'
    namespace: moby # containerd namespace
spec:
  # refers to `Image` objects, which are not namespaced.
  imageRef: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b'
  push: false # If set to true, the image is pushed; see below.
status:
  conditions:
  - type: PullStarted
    status: True
  - type: Ready
    status: True
  - type: Pushed
    status: False
```

This is split into separate objects so that listing images by tag is easier,
assuming we never want to list images with no tags.

### Image Actions

#### Fetch image
Create an `ImageTag` object, but omit the `imageRef`.  The reconciler will pull
the corresponding image and fill in the `imageRef` once available.

#### Build image
Not sure; do something with the `Resource` API maybe?

We may need an `ImageBuildRequest` job-thing or something?

#### Push image
Set `.spec.push` to `true`.  The image will be pushed by `.metadata.labels.name`.
After the push, `.status[?(@.type=="Pushed")]` will be set to `True`, and
`.spec.push` will be set to `false`.

#### Scan image
We will need a new object type for this; maybe something like
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImageScanRequest
metadata:
  generateName: imageScan-
  namespace: rdd-system # not containerd namespace
spec:
  # refers to `Image` objects, which are not namespaced.
  imageRef: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b'
status:
  conditions:
  - type: Finished
    status: True
  result:
    # Just dump the Trivy result JSON here (without converting to YAML).
```

#### Untag image
Update the `ImageTag` with the `.spec.imageRef` set to `deleted`; once processed,
the `ImageTag` will be removed to reflect the container engine state.

## Volumes

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Volume
metadata:
  generateName: volume-name- # Based on the containerd name
  namespace: default # Not related to containerd namespace
  labels:
    name: volume-name
    namespace: k8s.io # containerd namespace, or `moby` if using dockerd
spec:
  createdAt: "2025-11-17T03:14:16Z"
  driver: local
  mountpoint: /var/lib/docker/volumes/volume-name/_data
  labels: {}
  scope: local
  options: {}
```

### Volume Actions

#### Create volume
Create a `VolumeRequest`:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Volume
metadata: # same as Volume.metadata
spec: # same as Volume.spec
status:
  conditions:
  - type: Processed
    status: False
```
Only local volumes are supported initially.
Parts of `.spec` may be ignored.

#### Delete volume
Create a `VolumeRequest`, with `.spec.scope` set to `deleted`.
Once the volume has been deleted, the `VolumeRequest` will also be deleted after
a brief timeout.
