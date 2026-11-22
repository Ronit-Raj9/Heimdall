# Library registry: images pinned by digest

Library images (a benchmark's base image, the helper, every task's own image)
are built once, pushed to a registry, and pinned by digest in the
benchmark's `suite.lock`. Every worker (the shared worker, bring your own
workers, cloud workers) then pulls the same bytes instead of building its own
copy. A fresh worker starts in the time of a pull, not of a build, and the
lock proves the image is the one admission graded.

Code: `bench/imagepin` (sync, prefetch, tokens, layer report),
`handlers/registrytoken.go` (token endpoint), `utils/imagepins.go` (pull by
digest), `suite/fingerprint.go` (lock identities).

## How it fits together

1. The operator runs `bifrost-bench images sync <benchmark>` on the machine
   that built the images. It pushes each image to
   `<registry>/library/<benchmark>/<image>:<tag>`, writes the digest of each
   push to `<benchmark>/.suite.image-pins`, and re-locks the benchmark.
   `suite.lock` then records each image as `<tag>=<registry>/<repo>@sha256:<hex>`
   (see `docs/lock-fingerprints.md`). The pins file is outside the files
   fingerprint; the lock binds the digests themselves.
2. A worker that lacks a pinned image pulls it by digest and tags it, never
   builds it. Before the slots of a newly admitted evaluation, the runner
   pulls every pinned image of that benchmark it lacks, in the background
   (`runner start` does it; `images prefetch` does it by hand). A local image
   with the same tag but another digest is never replaced: the lock check
   refuses it as drift, as before.
3. The registry trusts short-lived pull tokens (5 minutes) that the Heimdall
   server signs at `GET|POST /api/v1/registry/token` (Docker registry token
   authentication). A docker client logs in with a Heimdall credential as the
   password: an org API token (`hm_...`) or a runner token (`hmr_...`). An org
   gets pull on the open library and on the restricted benchmarks the access
   file allows it. Only the sync identity pushes. Nothing deletes.

## Access file (licence-restricted benchmarks)

`HEIMDALL_LIBRARY_REGISTRY_ACCESS` names a YAML file:

```yaml
namespace: library          # default
restricted:
  gaia: [org_7f3a]          # only these orgs pull library/gaia/...
  locomo: []                # no org at all (non-commercial licence)
```

A benchmark not listed is open to every org. Restricting a benchmark here
keeps its images (and the task data inside them) away from orgs that may not
use it; the catalog and packs still need their own licence filter
(GAP_G3 P0-7).

## Operator steps (hosted)

On the server host, in the checkout:

```sh
D=/var/lib/heimdall-library-token
sudo bifrost-bench images token-key -out $D           # token.key (server only), token.crt (registry)
openssl rand -hex 24 | sudo tee $D/sync.password >/dev/null
sudo sh -c "bifrost-bench registry htpasswd -user sync < $D/sync.password > $D/sync.htpasswd"
sudo sh -c "printf 'sync:%s\n' \"\$(cat $D/sync.password)\" > $D/sync.auth"
sudo chown -R 10001:10001 $D && sudo chmod 0700 $D && sudo chmod 0600 $D/token.key $D/sync.*
sudo chmod 0644 $D/token.crt
```

In `deploy/hosted/.env`: `HEIMDALL_LIBRARY_REGISTRY_TOKEN_DIR=$D`, optionally
`HEIMDALL_LIBRARY_REGISTRY_ACCESS=/path/to/access.yaml`, and
`HEIMDALL_COMPOSE_OVERRIDE=deploy/hosted/library-registry.compose.yml` (or
copy its services into `docker-compose.override.yml`). Then add the registry
route to the Caddyfile site block, before the catch-all `handle`:

```
	handle /v2/* {
		request_body {
			max_size 0
		}
		reverse_proxy library-registry:5000 {
			transport http {
				read_timeout 30m
				write_timeout 30m
			}
		}
	}
```

and restart: `deploy/hosted/upgrade.sh` (or `docker compose ... up -d
library-registry heimdall caddy`).

Push a benchmark (from the host, over loopback; port
`HEIMDALL_LIBRARY_REGISTRY_PUBLISH`, default 5001):

```sh
bifrost-bench images sync coding-core -registry 127.0.0.1:5001 -auth-file $D/sync.auth
bifrost-bench images layers coding-core -auth-file $D/sync.auth -plain-http
```

`images sync` reports each image (`ok`, `skip` for an image not on the
machine, `FAIL`), the pins written and the new lock. Images pushed over
loopback are pinned as `127.0.0.1:5001/...`; to pin them under the public
name, push to `<domain>` instead (through Caddy). Workers must pull the name
the lock records.

## Worker steps

```sh
bifrost-bench images login -registry heimdall.example.com -token-file runner.token
bifrost-bench images prefetch coding-core          # optional: warm before the first lease
```

`runner start` prefetches by itself. The shared worker pulls only approved
images: add the registry prefix (for example `heimdall.example.com/library/`)
to `HEIMDALL_SHARED_PLATFORM_IMAGES`.

## Layer sharing

`images layers <benchmark>` reads the registry's manifests and prints what
pulling every image one by one would move against the distinct bytes the
registry stores and a worker pulls once, and the largest shared layers. A
SWE-style family built on shared base and environment layers (the upstream
`sweb.base`, `sweb.env`, `sweb.eval` tiers) shares most of its bytes; the
registry stores each shared blob once and docker mounts it across
repositories on push. A family whose images share little is the case for
restructuring its Dockerfiles (Epoch-style: one base, one environment layer
per repository version, a thin instance layer), which is not automated yet.

## Testing on a local registry

`BENCH_REGISTRY_IT=1 go test ./imagepin -run TestLocalRegistryEndToEnd -v`
starts a throwaway `registry:2` with token auth, the token endpoint on the
host, pushes a tiny image as the sync identity, removes it, pulls it back by
digest as an org (logged as the cold pull time), and checks that an org
cannot push and cannot pull a restricted benchmark. It touches no other
registry.
