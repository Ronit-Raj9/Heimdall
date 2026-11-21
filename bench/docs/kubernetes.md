# Kubernetes driver

The Kubernetes driver runs each benchmark run as one pod in its own namespace of a Kubernetes cluster, with the same workspace, observers, diff export, grading and verdict as the Docker driver. An enterprise can keep agents off the bench machine and inside a cluster it already governs (quotas, node pools, network policy, audit), while the evidence and the verdict stay what they are under Docker.

Status (18 Nov 2026): built, unit-tested against a fake API server, validated against a bare kube-apiserver, and proven on a local kind cluster (Kubernetes v1.35.0, kindnet) against the Docker driver on the same tasks: same grades, same patches, same receipts, sealed evidence verifying, egress blocked ("Proof on kind"). Managed clusters (EKS, GKE, AKS) are not tested; "Managed clusters" says what should carry over and what will not.

Code: `environments/driver.go` (the seam), `environments/driverkit.go` (the Docker driver's pieces another driver reuses), `k8s/` (the driver: `client.go` REST client and kubeconfig, `ws.go` and `exec.go` WebSocket exec and attach, `podspec.go` the run pod, `driver.go` the run lifecycle, `observers.go`, `egress.go` the egress probe and the gateway preflight, `check.go`, `gateway.go`, `refusals.go`), `engine/driverplan.go` (plan time), `cmd/bifrost-bench/k8s.go` (wiring and the `k8s` command), `deploy/k8s/` (kind config, RBAC, the parity experiment).

## The seam

A suite names its environment type, `docker_workspace`: a hardened workspace seeded from the task repo, one agent process, a trusted diff export. The driver is where that environment's containers live. `environments.Driver` is `schemas.Environment` (the interface the engine already drives) plus two methods:

| Engine step | Call | Docker driver | Kubernetes driver |
|---|---|---|---|
| prepare | `Create` | volumes, seeded by a helper container, baseline commit | namespace, anchor config map, deny-all policy; protected and notes hashes from the host task folder |
| start agent, exec, stop, collect | `Exec` | `docker create` + `docker start -a`, observers beside it, `docker kill` on the wall budget | the run pod: workspace seeded inside it (baseline commit set here), observers as native sidecars, agent attached, every agent process killed on the wall budget, observer records written |
| copy out | `ExportDiff` | export script in a helper with the volume read-only | the same export script in the pod's bench helper, workspace read-only |
| copy out (answer, artifact tasks) | `ExportAnswer`, `ExportArtifacts` | answer and artifact scripts in a helper | the same scripts in the bench helper, the artifact tar streamed back over exec and re-checked against every cap (tested against the fake API only, with the real artifact script) |
| cleanup | `Destroy` | containers, volumes, networks, images by run label | anchor (garbage-collects its dependents), then the namespace |
| plan | `Refusals(slot)` | nothing | every unsupported combination, with the reason |
| identity | `Driver()` | `docker` | `kubernetes` |

The Docker driver (`environments.NewDockerDriver`) is the `docker_workspace` environment with those two methods added; every other method, optional interfaces included, is the unchanged code. The only behaviour it adds is to refuse a run whose manifest was frozen for the Kubernetes driver. Recorded Docker manifests, locks and seals keep their hashes: `Experiment.Environment` is `omitempty` and the planner leaves it nil for a Docker experiment that names no driver.

The environment part keeps its name and version under both drivers (it is the same environment type, run elsewhere), so suite locks and lock fingerprints do not change. Which driver ran is frozen in the manifest instead: the planner writes `environment: {driver: kubernetes}` into every Kubernetes experiment, and each run folder gets a `kubernetes.json` (cluster, namespace, node, the image ids the node ran, baseline commit, restarts, events).

## Setup on kind

Needs `kind`, Docker, Go 1.26 and the images built (`make images`). A host that runs several containers can run out of inotify instances, and a kind node's containerd and kubelet then fail with "too many open files" (kind's known issue; this machine hit it at the default 128). Raise the limit first:

```sh
sudo sysctl -w fs.inotify.max_user_instances=1024
kind create cluster --config deploy/k8s/kind.yaml --kubeconfig ~/.kube/heimdall-kind
```

`deploy/k8s/kind.yaml` names the cluster `hd-x3` and publishes two NodePorts on loopback: 18381 for Bifrost and 18382 for the fault proxy's control API. Then, from `bench/`, with the settings as environment overrides of `deploy/bench.yaml` (or the same keys in a copy of it):

```sh
export BENCH_ENVIRONMENT_DRIVER=kubernetes
export BENCH_KUBERNETES_KUBECONFIG=~/.kube/heimdall-kind
export BENCH_KUBERNETES_KIND_CLUSTER=hd-x3
export BENCH_KUBERNETES_GATEWAY_URL=http://bifrost.heimdall-gw.svc:8080
export BENCH_GATEWAY_URL=http://127.0.0.1:18381
export BENCH_FAULTPROXY_CONTROL_URL=http://127.0.0.1:18382
export BIFROST_SETUP_TOKEN=$(openssl rand -hex 24) BENCH_FAULTPROXY_TOKEN=$(openssl rand -hex 24)

bifrost-bench k8s images load -gateway          # kind load docker-image: helper, bench image, pinned Bifrost
bifrost-bench k8s gateway up -node-port-gateway 30080 -node-port-control 30091
bifrost-bench k8s check -image bench/coding-core-base:1
bifrost-bench run deploy/k8s/parity.yaml        # oracle 4 of 4, noop 0 of 4
bifrost-bench k8s cleanup                        # lists this install's run namespaces; -delete removes the stale ones
bifrost-bench k8s gateway down
```

Configuration (`bench.yaml`):

```yaml
environment:
  driver: kubernetes            # docker (default) | kubernetes
kubernetes:
  kubeconfig: ""                # empty: $KUBECONFIG, ~/.kube/config, then the pod's service account
  context: ""                   # empty: the current context
  namespace_prefix: heimdall-run
  gateway_url: http://bifrost.heimdall-gw.svc:8080   # the gateway Service, in-cluster
  image_pull_policy: IfNotPresent                    # IfNotPresent | Never | Always
  service_account: ""           # created, tokenless, in each run namespace; empty uses the namespace default
  node_selector: {}             # e.g. {heimdall.dev/pool: agents}
  tolerations: []               # [{key, operator, value, effect, toleration_seconds}]
  kind_cluster: ""              # target of `k8s images load`
  namespaces: []                # namespace pool, e.g. [heimdall-run-1, heimdall-run-2]; empty: one namespace per run
  runtime_class: ""             # RuntimeClass of every run and probe pod, e.g. gvisor or kata; empty: cluster default
```

### Namespace pool

By default the driver creates one namespace per run and deletes it afterwards, which needs a ClusterRole with namespace create/delete, secrets and `pods/exec` everywhere (`deploy/k8s/rbac-cluster.yaml`, the legacy alternative). Many clusters refuse that. Set `kubernetes.namespaces` to namespaces an administrator created up front and the driver never creates or deletes a namespace: each run leases one free pool namespace by creating its anchor config map `run` there. A conflict means another run holds it and the next one is tried; an anchor left by an earlier attempt of the same run is removed (with the objects it owns) and created again; when none is free the run fails with `no free namespace in kubernetes.namespaces`. `Destroy` deletes the anchor (its dependents are garbage-collected), deletes the run's pod, secret and policies itself and waits until they are gone, since the next run reuses the names. The egress probe and `k8s check` lease a pool namespace the same way, and `k8s check` asks for the namespaced verbs in each pool namespace. `k8s cleanup` in pool mode removes stale anchors of this install, never a namespace. A service account an administrator put in a pool namespace is used as it is. Give the pool at least as many namespaces as runs in flight. `deploy/k8s/rbac.yaml` creates `heimdall-run-1` to `heimdall-run-4` (label `heimdall.run: "1"`), a Role and RoleBinding in each, read access to the gateway Service in its namespace only, and a small read-only ClusterRole (nodes get/list for the node image checks, `selfsubjectaccessreviews` for `k8s check`, `runtimeclasses` get).

```yaml
kubernetes:
  namespaces: [heimdall-run-1, heimdall-run-2, heimdall-run-3, heimdall-run-4]
```

### Sandboxed runtime

`kubernetes.runtime_class` sets `runtimeClassName` on every run and probe pod, so agents run under a sandboxed runtime such as gVisor or Kata. `k8s check` fails when the RuntimeClass does not exist or cannot be read. The cluster administrator installs the runtime on the nodes and its RuntimeClass, for example:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
```

An experiment may name the driver it must run on (`spec/experiment.schema.json`):

```yaml
environment: {driver: kubernetes}
```

The planner refuses it when `bench.yaml` configures another driver, and a resumed or remote run of an experiment frozen for one driver is refused by the other at `Create`.

## What runs where

| Where | What |
|---|---|
| The bench machine (the controller) | `bifrost-bench`: planner, scheduler, store and seals, the clean-room grader (hidden tests in a fresh Docker container with no network), gates, metrics, verdict. Image metadata (PATH, entrypoint, ids, registry digests) is read from its Docker daemon, where images are built and from where they are loaded or pushed |
| The run namespace `<prefix>-<run id>` | the anchor config map, the deny-all and gateway egress policies, the agent's env secret, the observer scripts, and one pod |
| The gateway namespace (`heimdall-gw`) | Bifrost (the image pinned by digest in `deploy/docker-compose.yml`) and the bench's fault proxy, both from `k8s gateway up` |

So the agents, the only untrusted code, run in the cluster, while grading stays on the controller exactly as under Docker. The controller therefore still needs Docker, for the grader and the image metadata; a controller without Docker is not supported.

### The run pod

| Container | Kind | Image | User, capabilities | Mounts | Job |
|---|---|---|---|---|---|
| `prep` | init | helper | root; CHOWN, DAC_OVERRIDE, FOWNER | workspace, aux, home, tmp | receives the task repo as a tar over exec and runs the Docker driver's populate script on it: same files, same baseline commit, same owner |
| `fswatch` | native sidecar | helper | root; DAC_OVERRIDE | workspace read-only | the Docker driver's inotify observer |
| `procwatch` | native sidecar | helper | root; SYS_PTRACE | none of the agent's | the Docker driver's `/proc` observer, in the pod's shared PID namespace |
| `collector` | native sidecar | `images.toolproxy` | 65534; none | its ledger | the agent event collector on the pod's loopback |
| `agent` | main | the plan's image | the plan's uid (1000 by default), never root; none | workspace, aux, home and `/tmp` (memory, 512 MiB each) | the agent |
| `bench` | main | helper | root; DAC_OVERRIDE, KILL | workspace read-only, observer output, ledger read-only | the trusted helper the bench execs into: diff export, observer stop and read-out, the wall-budget kill |

Every container drops all capabilities except those listed, has a read-only root file system and no privilege escalation; the pod has `shareProcessNamespace`, no service account token, no service links, the RuntimeDefault seccomp profile, `restartPolicy: Never` and an `activeDeadlineSeconds` of the wall budget plus 30 minutes as a backstop. No volume is a host path: every one is an emptyDir, a config map or a secret.

Order: `prep` holds the pod until the bench has seeded the workspace through exec; the sidecars then start one by one, each held by a startup probe until its ready line (or its error, so a broken observer is missing evidence, never a blocked run), and only then the agent. The agent's first process is a gate (`IFS= read -r _; exec "$@" </dev/null`): the bench attaches to the agent container and releases it with one line on stdin, so not one byte of output is written before it is captured. stdout and stderr arrive on separate attach streams and go, capped and with the run key redacted, into the same `agent.stream.jsonl` and `agent.stderr.txt` as under Docker. The exit code comes from the container's terminated state.

The agent's argv and environment are the Docker driver's: the same launch wrapper (`BENCH_WRAP_*` variables decoded by `sh -c`) for plan files and long values, the same `PATH` with the shim folder first, the same `HOME`, and the task's own variables (`environment.env`) under the plan's, as under Docker. They travel in an immutable secret loaded with `envFrom`, never in the pod spec.

### Observers in a shared PID namespace

Under Docker the process observer joins the agent container's own PID namespace. A pod has one PID namespace for all its containers, so the process observer also sees the pause process, the bench's containers, the kubelet's probes and the bench's execs. The driver keeps only processes of the agent's uid (every other container runs as another uid, and the agent cannot change its uid: no privilege escalation, every capability dropped), drops PID 1 (pause), and shows the agent container's main process as PID 1 with its children pointing at it, as in its own namespace under Docker; the observer's coverage note says so. The PIDs-allocated bound still holds but is looser, because PIDs handed to the bench's own processes count too. Under Docker the agent sees the process observer in `ps`; here it also sees the workspace observer, the collector and the bench helper's `sleep` (all as other users it cannot signal).

The process observer starts before the agent here, so it sees the agent from its first process; under Docker it joins a moment after the agent starts.

## Differences from Docker an agent or a reader can see

| | Docker driver | Kubernetes driver |
|---|---|---|
| Processes the agent sees in `ps` | its own and the process observer | its own, both observers, the collector, the bench helper's `sleep`, and the kubelet's probe commands (all of other uids) |
| Process observer start | joins a moment after the agent starts; a very short agent (a scripted control) can end first, and `commands_executed` is then unknown | starts before the agent, so it sees the agent from its first process |
| Process records | PIDs of the agent's own namespace | PIDs of the pod's namespace, other uids dropped, the agent's main process (found before release) shown as PID 1; its record shows the runtime's setup process (`runc:[2:INIT]`, uid 0), because the observer, which starts first, sees the process before the runtime execs the gate in it |
| Workspace seeding | `cp -dR --preserve=mode` from the task folder | a tar of the task folder (files, folders, symlinks, modes) over exec, then the same populate script: same files, same baseline commit |
| Agent events | collector, events file and prefixed stdout | collector and prefixed stdout; no events file (`BENCH_EVENTS_FILE` is not set) |
| An agent command that does not exist | `docker create` fails: the run is an infrastructure error | the gate's `exec` fails: the agent exits 127 and the run is graded like any other exit |
| Resource limits | `--cpus`, `--memory`, `--memory-swap`, `--pids-limit 512` | CPU and memory requests and limits from the same budgets; no per-pod PID limit |
| Run record | no extra file | `kubernetes.json` in the run folder |

## Probes

Gateway probes act in the fault proxy behind Bifrost and need nothing from the driver. The fixture and tool shim layers arm between `Create` and `Exec` by writing into the run's workspace and aux volumes through Docker helpers on the controller (`probes/fixture.go`, `probes/toolshim.go`), unchanged. For a run under one of them, `Create` gives the handle two local Docker volumes (`bench-k8s-ws-<run>`, `bench-k8s-aux-<run>`, labelled with the run) for the layers to write into. `Exec` lays their files into the pod after the baseline commit, exactly where they land under Docker (after `Create`'s commit), keeping owners and modes, so the shim folder stays root-owned and read-only to the agent; only then is the pod released. After the agent, the pod's aux `log/` folder is copied back into the staging aux volume, where the shim log evidence and the layer's receipts read it. `Destroy` removes both volumes.

## Security model

- **Network.** Every run namespace gets a deny-all NetworkPolicy (ingress and egress) when it is created, before any pod exists. A plan with the evaluation network also gets one egress rule: to the gateway Service's pods (its selector, in its namespace) on its target port, and nothing else. A plan with network `none` (every scripted control except toy) keeps deny-all. The pod has no DNS server (`dnsPolicy: None`, nameserver 127.0.0.1); the agent URL's host (`bifrost` in `gateway.agent_url`) is an `/etc/hosts` entry for the gateway Service's cluster IP, so the agent is given the same URL as under Docker and cluster DNS cannot be used as a side channel.
- **Egress is shown, not assumed.** The gateway preflight runs a control pod without policies and then a probe pod with the run policies. The policed pod must fail to reach the internet (1.1.1.1:443), the cloud metadata address, DNS and the cluster DNS Service (UDP and TCP), and must reach the gateway (the positive control). `egress_probe` is `blocked` only when at least one of those targets was reachable from the control pod and is blocked under the policies; when the control reached none of them, enforcement is not shown and the preflight fails with `not_run`. The API server is probed for information only (it is often reachable on the pod's own node and run pods mount no token), never counted in the verdict. The result replaces the Docker probe's `egress_probe` and `eval_network` settings in the manifest, and the egress gate holds every run to it as under Docker: `eval_network` is `kubernetes:<namespace_prefix>@<api server>` in both the manifest and each run's handle. `k8s check` runs the same contrast.
- **Credentials.** The run's virtual key is in the agent's env secret only; it is redacted from the captured streams and the observer records as under Docker. The bench's own credential (kubeconfig or service account) never enters a run pod.
- **Images.** The image the node ran is checked against the one the experiment froze: the container's image id must be the frozen local id (an image loaded with `kind load`) or carry one of the image's registry digests (a pulled one); anything else fails the run before the agent is released. Locks are unchanged: locally built images are bound by tag as before, pulled ones by registry digest; no machine-local id is committed.
- **Pod Security.** The agent container meets the `restricted` Pod Security Standard. The pod does not: the observers and the bench helper run as root, and the process observer needs SYS_PTRACE, which `baseline` forbids too. With `observers.process: false` the pod meets `baseline`. A cluster that enforces `baseline` or `restricted` everywhere must exempt the run namespaces (prefix `heimdall-run`) or switch the process observer off.
- **The gateway namespace** has no NetworkPolicy of its own: run pods can reach only Bifrost's pods (their egress rule), but anything else in the cluster can reach Bifrost and the fault proxy's control port. Restrict ingress there to the bench and the run namespaces (label `heimdall.dev/role: run`) in production.

## What is refused

At plan time, with the reason, before anything is frozen (`k8s/refusals.go`); `Create` checks again for a resumed run:

| Refused | Why |
|---|---|
| Tasks with services, external APIs or a mailbox | services run as Docker containers beside the agent |
| Multi-agent teams | one agent container per run |
| Multi-session tasks, agent memory sections | no session reset or memory volume between agent containers |
| Agent skills folders | plan folders are copied into Docker volumes |
| Snapshot grading | the agent's root file system is never committed |
| Tasks with their own image, workspace path or root user (a task that only sets `environment.env` runs) | the workspace is seeded from the task repo only; agents never run as root |
| `remote_http` agents | they need the Docker egress forwarder |
| `realism: neutral` | the neutral view renames Docker objects |
| Adapter-layer probes (hook faults) and member-targeted probes | the hook controller joins the agent's Docker network; there are no teams |
| GPU tasks (`resources.gpu`) | no GPU request is passed to the pod |
| Compose tasks (`compose`), even one whose only service is the workspace | their services run as Docker containers, and the workspace service is a task image |
| `bifrost-bench agent check` | stays on Docker (its network observer is Docker-only) |

All three of PS2's failure families run under this driver: the unavailable model (`model-outage`, gateway layer), the broken tool (`test-crash`, `empty-test-run`, tool shim layer) and the misleading instruction (`stale-notes`, fixture layer); see "Probes".

## The gateway in the cluster

`k8s gateway up` deploys, in the namespace `kubernetes.gateway_url` names: Bifrost from the image pinned by digest in `deploy/docker-compose.yml`, with `deploy/bifrost/config.json` (its stores moved from Postgres to SQLite on an emptyDir, so no database runs; the bench reads requests through Bifrost's API either way) and the pinned datasheets (gzipped in a config map, since the model parameter sheet is over a config map's 1 MiB as plain JSON); and the bench's fault proxy (`bifrost-bench proxy`, `deploy/bench.yaml`). The admin and control tokens come from `BIFROST_SETUP_TOKEN` and `BENCH_FAULTPROXY_TOKEN` and are stored in a secret; no provider key is ever copied in, so every model call is answered by the fault proxy's scripted model and a run costs nothing. To use a real provider, add its key to the fault proxy deployment yourself; that path is not tested. The run's virtual key is minted against this Bifrost exactly as against the compose stack. `-node-port-gateway` and `-node-port-control` publish both on NodePorts (the kind config maps them to loopback); in a cluster where the bench itself runs in a pod, leave them off and point `gateway.url` and `faultproxy.control_url` at the Services. `k8s gateway down` deletes the namespace.

The fault proxy's ledger lives on an emptyDir: a restarted proxy pod loses the ledger lines of earlier runs, and their gateway join then reads incomplete. Use a persistent volume for a long-lived pool.

## Cleanup

Every object of a run is owned by its anchor config map (owner references), and everything lives in the run's namespace. `Destroy` deletes the anchor, which garbage-collects its dependents, then the namespace; both are idempotent and work after a failed `Create`. Namespaces carry `heimdall.dev/bench`, `heimdall.dev/run`, `heimdall.dev/exp` and `heimdall.dev/role` labels; `k8s cleanup` lists the namespaces named `<namespace_prefix>-...` except the gateway's, so another install on the same cluster is never touched, and classifies each as the Docker janitor does: a run whose slot lease is fresh is kept, a dead or sealed run is stale, and a namespace with no lease information is stale only when older than `-grace` (6h). `-delete` removes the stale ones only. A run namespace whose name already exists with another run's labels is never replaced; the run fails instead. Long prefixes (at most 40 characters) keep run namespaces unique by ending a cut name in a hash of the run ID. In pool mode (`kubernetes.namespaces`) nothing above deletes a namespace: `Destroy` releases the anchor and the run's objects, and `k8s cleanup` lists this install's anchors in the pool namespaces with the same live/dead/grace rules and with `-delete` releases the stale ones.

## Exec and attach protocol

Exec (seeding, export, observer read-out, the wall-budget kill) and attach (the agent's streams) use the API server's WebSocket endpoints with protocol `v5.channel.k8s.io`, through a small RFC 6455 client of the driver's own on `net/http` (`k8s/ws.go`). v5 (Kubernetes 1.30 and later) is required for any call with stdin, because only v5 can close stdin; `k8s check` refuses older servers. SPDY was not used: it needs a multiplexing implementation the standard library does not have, and Kubernetes has moved its own clients to WebSocket. `kubectl` is not needed.

## Managed clusters (untested)

Nothing here was run on EKS, GKE or AKS, and the provider facts below are unverified: they are not re-checked against the vendors' documentation as of 18 Nov 2026. `k8s check` is the test that matters on a given cluster. What should carry over and what will not:

- **Authentication.** Managed clusters' kubeconfigs usually authenticate with an exec plugin (`aws eks get-token`, `gke-gcloud-auth-plugin`, `kubelogin`), which the driver refuses with that reason. Use a service account token (`tokenFile` in a kubeconfig, or run the bench in the cluster with `deploy/k8s/rbac.yaml`), or a client certificate where the cluster issues them.
- **NetworkPolicy.** EKS enforces policies only with the VPC CNI's network policy agent (or Calico) switched on; GKE with Dataplane V2 or network policy enabled; AKS with Azure, Calico or Cilium network policy chosen at cluster creation. `k8s check` shows whether policies are enforced; a cluster that does not enforce them fails it.
- **Images.** Push the agent, helper and bench images to the cluster's registry and reference them by digest; `k8s images load` is kind-only. Set `image_pull_policy`. `kubernetes.service_account` is created fresh, with no token, in every run and probe namespace (the driver role needs `create` on `serviceaccounts`), so for a private registry attach pull secrets through an admission policy or a node credential, not to an account in another namespace.
- **Metadata address.** The probe treats 169.254.169.254 as forbidden; on clouds it is the instance metadata service, so the deny-all policy also protects node credentials, as long as the CNI enforces egress to link-local addresses (verify with `k8s check`).
- **Node pools.** `node_selector` and `tolerations` place run pods on a dedicated pool.

## What was verified

| Check | How | Result (18 Nov 2026) |
|---|---|---|
| Docker behaviour unchanged | `GOWORK=off go test ./...`; engine manifest-hash and lock tests; the Docker driver registered through the seam in every run below | green; Docker manifests keep no `environment` field |
| Driver lifecycle | `go test ./k8s/` against an in-memory fake API server with a WebSocket exec and attach of its own: Create, Exec (seed tar, gated agent, attach streams with the key redacted, observers, collector), ExportDiff, ExportAnswer, ExportArtifacts (the real artifact script), staged probe files, Destroy | pass |
| WebSocket client | `go test ./k8s/`: RFC 6455 accept vector, frame round trips, v5 stdin close, v4 refused with stdin, a stream cut before its status is an error | pass |
| Real API server validation | `BENCH_K8S_API_KUBECONFIG=... go test -tags k8s ./k8s/ -run TestAPIServer`, against kube-apiserver v1.35.0 with etcd 3.6.6 (the images of the kind node) and no kubelet | every run object, the run pod (native sidecars, `dnsPolicy: None`, host aliases, owner references), every gateway object and a second `gateway up` (merge patches) accepted; every permission `k8s check` asks for allowed; Destroy deletes the namespace |
| `deploy/k8s/rbac.yaml` | `kubectl apply` on that API server, then `kubectl auth can-i ... --as=system:serviceaccount:heimdall-system:heimdall-bench` | applies; the account may create namespaces and `pods/exec`, may not create cluster roles |
| In-cluster gateway config | the SQLite Bifrost config under Docker (see Limits) | Setup and preflight clean; the toy smoke read turns, tool calls and tokens from it (`e_smoke_zfc5`) |
| Docker baselines for the parity runs | `deploy/k8s/parity.yaml`, `deploy/k8s/parity-probes.yaml` and `experiments/smoke.yaml` under the Docker driver, on a throwaway Bifrost (SQLite) and fault proxy with no provider key | `e_k8s_parity_28mm`: oracle 4 of 4, noop 0 of 4; `e_k8s_parity_probes_jh6x`: oracle 6 of 6, noop 0 of 6, every probe armed; `e_smoke_zfc5` (toy): clean 4 of 4, model-outage 0 of 4. Earlier baselines `e_k8s_parity_mr5f` and `e_k8s_parity_probes_goo9` ran while the host was out of inotify instances (workspace observer missing) and with the image before it was rebuilt, so they are not used for the comparison |

## Proof on kind

18 Nov 2026, 11:47 to 12:28 IST, one kind node (`deploy/k8s/kind.yaml`, node image v1.35.0, kindnet with its network policy support, containerd 2.2.1), the gateway from `k8s gateway up` (Bifrost v2.5.1 by digest, the fault proxy with no provider key), runs root in the bench's own scratch folder, every model call answered by the scripted model: $0.

`k8s check`:

```
PASS  host inotify           fs.inotify.max_user_instances is 1024
PASS  api server             https://127.0.0.1:43057 v1.35.0 (context kind-hd-x3)
PASS  api version            v1.35.0: native sidecars and WebSocket exec with stdin close (v5.channel.k8s.io)
PASS  permissions            all 13 verbs the driver uses are allowed
PASS  gateway                http://bifrost.hd-x3-gw.svc:8080 -> 10.96.48.246 (agents reach http://bifrost:8080 through /etc/hosts; egress allowed to pods map[app.kubernetes.io/name:bifrost] port 8080)
PASS  image bench/coding-core-base:1 on all 1 nodes
PASS  network policy         enforced: without policies a pod reached dns, tcp:1.1.1.1:443, tcp:169.254.169.254:80, udp:cluster-dns:53, tcp:cluster-dns:53; with the run policies all of them are blocked and the gateway answers
WARN  api server reach       reachable but not held against isolation: tcp:api-server:443 (the API server on the pod's own node; run pods mount no service account token)
```

| Experiment (Kubernetes) | Docker baseline | Slots | Result under both drivers |
|---|---|---:|---|
| `e_k8s_parity_k2wh` (`deploy/k8s/parity.yaml`: t01 to t04, clean) | `e_k8s_parity_28mm` | 8 | oracle 4 of 4 pass, noop 4 of 4 fail; the same grade on every slot; byte-identical patches (the same `diff_sha256` on all 8 slots); no gate fired; the egress gate passed on every run |
| `e_k8s_parity_probes_esrp` (`deploy/k8s/parity-probes.yaml`: t01, t02 under stale-notes, test-crash, model-outage) | `e_k8s_parity_probes_jh6x` | 12 | oracle 6 of 6, noop 0 of 6; the same grade on every slot; the same receipts (assigned, armed, fired, delivered, visible, effect) on every slot; identical patches except under stale-notes, where the patch holds the notes file with its per-run nonce, which differs between any two runs |
| `e_smoke_9ic3` (`experiments/smoke.yaml`: the toy agent through the in-cluster Bifrost, clean and model-outage) | `e_smoke_zfc5` | 8 | clean 4 of 4 pass, model-outage 4 of 4 fail; the same steps, tool calls, tokens, model attempts and cost at pinned prices per slot (scripted tokens: nothing was spent), read from the in-cluster Bifrost's log store; the Docker baseline ran before `bench/coding-core-base:1` was rebuilt, so its image id differs; model-outage fired, delivered and visible on all 4 |

`bifrost-bench check -exp <id>` on the three Kubernetes experiments verifies every seal and the seals.log chain (no INVALID); the probes experiment exits 5, a policy block, as its Docker baseline does (the default policy's ceilings on 12 scripted runs). Every manifest froze `environment: {driver: kubernetes}`, `egress_probe: blocked` and `eval_network: kubernetes:hd-x3-run@https://127.0.0.1:43057`. After the 36 Kubernetes runs `k8s cleanup` listed 0 run namespaces and no staging volume was left; `k8s gateway down` and `kind delete cluster` removed the rest.

Metric availability, the only difference: under Kubernetes the process observer starts before the agent, so `commands_executed` (and with it `agent_steps`, `tool_calls_executed`, `tool_calls_unwitnessed` where the Docker run had no observer records) is observed on every run, while under Docker the scripted controls end before the process observer can join them and it reads unknown with that reason. In `e_smoke_9ic3` against `e_smoke_zfc5`, `files_changed_events` and `tool_proof_rate` also differ, because that Docker baseline ran while the host was out of inotify instances. Values of the observer-based metrics differ where Docker had no process records (t04 oracle: `agent_steps` 3 under Docker from file events only, 1 under Kubernetes with the command that made them).

What the first attempts found and the driver now handles: kindnet programs a new pod's policy a moment after the pod starts, so a probe that connected at once got out (the probe now waits up to 30 s for isolation, and every run pod is held until a connection to the internet from its own network namespace fails); a `kind load`ed image is reported by its config digest while the experiment freezes the image index digest of Docker's containerd store (the driver now reads the config digest from `docker save` and accepts the match only when the local image still has the frozen id); the observer can first see the agent's main process as root while the runtime sets it up (it is now found before release and shown as PID 1).

Downloads during the proof: `maximhq/bifrost:v2.5.1@sha256:5ec98...` (Docker Hub, the vendor image, 105.7 MB) was pulled by the node, because `kind load` of the local multi-platform image failed (`content digest ... not found`) and a single-platform import keeps only the platform manifest, not the pinned index digest.

## Limits

- Grading, gates and image metadata need Docker on the controller.
- The agent's streams travel over one attach connection; a connection that breaks before the agent ends leaves later output uncaptured, and the run is marked truncated, never silently complete.
- The agent event collector runs; the events file channel does not (its reader reads the Docker aux volume), so an agent that only writes `BENCH_EVENTS_FILE` reports nothing here. Prefixed stdout lines and the collector work.
- There is no per-pod PID limit (Docker's `--pids-limit 512`); the kubelet's node-wide `podPidsLimit` applies. emptyDir volumes have no size limit except home and `/tmp` (512 MiB each, in memory, as under Docker).
- Kubernetes 1.30 or later; native sidecars and WebSocket exec with stdin close.
- Remote workers (`bifrost-bench worker`) configured with the Kubernetes driver should work (the frozen driver is checked at `Create`) but are not tested; a worker whose driver differs from the experiment's fails each slot it leases with that reason.
- The in-cluster gateway's Bifrost keeps config and logs in SQLite, the compose stack in Postgres. Before the cluster run, the same SQLite config was checked under Docker (a throwaway Bifrost v2.5.1 plus fault proxy on their own networks): Setup and preflight with no problem, and the toy smoke (`experiments/smoke.yaml`, experiment `e_smoke_zfc5`) read 3 model turns, 1 tool call and 1,958 tokens per clean run from Bifrost's SQLite log store.
