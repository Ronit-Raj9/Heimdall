# GPU tasks (ML-engineering agents)

Status: built and checked on 18 Nov 2026 (X7) on one machine: an NVIDIA
GeForce GTX 1650 (Turing, compute capability 7.5, 4 GiB), driver 615.78.08
(CUDA 13.4 user-mode driver), NVIDIA Container Toolkit 1.20.1, Docker with
CDI. Every check used scripted controls and cost nothing; no model has been
run on these tasks. What was measured, and what was not, is listed under
[Results](#results) and [Limits](#limits).

A GPU task is an ordinary task with `resources.gpu` in its `task.yaml`. The
bench gives each run of it whole GPUs of its own, passes them to the agent
container and the clean-room verifier only, records the hardware in the
sealed run, samples the GPUs from the host while the agent works, refuses
the plan on a machine that cannot run it, and makes a comparison INVALID
when its arms ran on different GPU models.

## Setup

1. An NVIDIA driver on the host (`nvidia-smi` works) and the NVIDIA
   Container Toolkit.
2. A CDI specification, so Docker can pass a GPU as a device:
   `sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml` (Docker also
   reads `/run/cdi`). `docker info` lists the devices under "CDI spec
   directories" and "Discovered Devices" (`nvidia.com/gpu=0`,
   `nvidia.com/gpu=<uuid>`, `nvidia.com/gpu=all`). Regenerate it after a
   driver update: on the test host `/etc/cdi/nvidia.yaml` was still written
   for driver 610.57.04, while a `/run/cdi/nvidia.yaml` written at boot
   matched 615.78.08, and containers got the current driver's libraries.
   What wrote the `/run/cdi` file on that host was not checked.
3. `bifrost-bench gpu check` (below). Without CDI, `gpu.mode:
   device_requests` passes GPUs with `--gpus device=<uuid>` through the
   NVIDIA runtime hook instead; both were checked on the test host.

```yaml
# deploy/bench.yaml
gpu:
  mode: cdi            # cdi (default) | device_requests | off
  devices: []          # GPUs the pool holds, by index or UUID; empty or [all] means every GPU
  sample_ms: 500       # host-side nvidia-smi interval; 0 means 500, negative switches the sampler off
  smi: nvidia-smi      # the host's nvidia-smi; empty finds it on PATH
```

Every key has an environment override (`BENCH_GPU_MODE`,
`BENCH_GPU_DEVICES=0,GPU-...`, `BENCH_GPU_SAMPLE_MS`, `BENCH_GPU_SMI`). A name
in `devices` that matches no GPU is not ignored: the pool is not built, and
GPU tasks are refused at plan time.

### `bifrost-bench gpu check`

Lists the host's GPUs (index, name, memory, driver, CUDA version, compute
capability, UUID), which ones the config selects, and proves, per selected
GPU, that a container started the way the bench starts an agent (no
network, every capability dropped, `no-new-privileges`, an unprivileged
user, read-only root, no privileged mode) sees exactly that GPU and no
other, by running `nvidia-smi -L` in it. It also takes one host-side sample.
`-suite <id>` adds whether each GPU task of the suite fits; `-json` prints
the report. It exits 4 on any problem. Output on the test host:

```
mode cdi, container check image bench/coding-core-base:1
index name                         memory     driver     cuda   compute  selected  uuid
0     NVIDIA GeForce GTX 1650      4096 MiB   615.78.08  13.4   7.5      true      GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15
container --device nvidia.com/gpu=GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15 seen (nvidia-smi -L listed GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15)
host sampler GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15: utilization 6%, memory 52 of 4096 MiB in use
ok: every selected GPU is visible to a hardened container, alone
```

The container check needs an image with glibc: the toolkit injects the
host's `nvidia-smi`, a glibc binary, so it does not run in Alpine. The
helper image (`images.helper`) is used unless `-image` names another.

## Writing a GPU task

```yaml
# task.yaml
resources:
  gpu: {count: 1, min_vram_mb: 2048}   # whole GPUs, each with at least this much memory (MiB, as nvidia-smi reports)
```

- `count` is 1 to 8; `min_vram_mb` is 0 (any) or more. `suite lint` checks
  both, refuses a GPU request on a task with a team (GPU team runs are not
  supported) and warns on a task of the `ml_engineering` track
  (`schemas.TrackMLEngineering`) that asks for no GPU.
- The agent image must have the CUDA user-space libraries the task needs
  (the driver itself, `libcuda`, comes from the host through the toolkit).
  The example suite and its scripted controls use one image for the agent
  and the verifier, so whatever the agent builds loads with the same
  libraries in the clean room.
- Graders are unchanged: a GPU verifier is an ordinary `verifier_command`
  that writes a reward with dimensions. Reporting the dimensions `speedup`,
  `heldout_score` and `time_to_target_s` fills the GPU metrics of the same
  names.

## What the bench does with a GPU task

### Passthrough

A run holds GPUs from the machine's pool (`schemas.GPUAllocator`,
`gpu.Pool`) and passes them by UUID: `--device nvidia.com/gpu=<uuid>` per GPU
under CDI, or one `--gpus device=<uuid>,...` request. UUIDs, not indexes, so
a reordered PCI bus never hands a run another card. Only two containers get
them:

- the agent container (`environments/gpu.go`), including a scripted
  control running in the agent's place;
- the clean-room verifier (`graders/gpu.go`); the preparation step that
  applies the diff does not.

Services, the tool proxy, the event collector, the observers and every
helper never do. The hardening is unchanged (every capability dropped,
`no-new-privileges`, read-only root, no host mounts, internal networks
only); the GPU adds no capability and never privileged mode. A GPU task
whose run holds no GPU is refused before its agent starts (the run seals as
`aborted_infra` with the reason), never run on the CPU instead, and its
verifier is refused too. Two resource changes follow the GPU: the verifier
of a GPU task gets 8 GiB of host memory instead of 2 GiB (a CUDA context
with PyTorch loaded takes most of 2 GiB alone), and a GPU task's admission
controls get 6 GiB, 2 CPUs and 900 seconds instead of 1 GiB, 1 CPU and 120
seconds.

### Scheduling

- **One run per GPU.** The scheduler starts one GPU worker per GPU of the
  pool beside its CPU workers. A GPU worker takes only GPU slots, holds the
  slot's GPUs from before the agent starts until the run is sealed (so the
  verifier runs on the same card), then gives them back. A CPU worker never
  takes a GPU slot. GPU slots and CPU slots therefore never wait behind each
  other, and every host-side sample of a GPU belongs to exactly one run.
- **Plan-time refusals**, with the reason, before anything is frozen:

  | Case | Message (excerpt) |
  |---|---|
  | no pool (`gpu.mode: off`, no driver, or detection failed) | `task small needs a GPU but GPU tasks are off on this machine (gpu.mode: off, or no NVIDIA driver; bifrost-bench gpu check)` |
  | too little memory | `task big needs 1 GPU(s) with at least 16000 MiB each, but GPU 0 NVIDIA GeForce GTX 1650 has 4096 MiB` |
  | too few GPUs | `task two needs 2 GPU(s), but this machine has 1 GPU(s) the bench can use, 1 with enough memory` |
  | Kubernetes driver | `task small needs a GPU and runs on the Docker driver only, not kubernetes` |
  | a team task | `task t needs a GPU and declares a team; GPU team runs are not supported` |

  `suite admit` refuses the same way. A scheduler started on a manifest
  with GPU slots and no pool (a resume on another machine) refuses to run
  rather than leave those slots pending.
- **Remote workers.** A worker registers its GPUs
  (`WorkerRegister.Capabilities.GPUs`, listed in `GET /workers`). The
  coordinator leases a GPU slot only to a worker whose free GPUs (registered
  minus those its running leases hold) meet the request; a worker without
  GPUs still takes CPU slots. On the worker the pipeline holds the GPUs from
  its own pool for the run. A worker's replan skips the plan-time GPU check
  (`engine.WithoutGPUCheck`), so a worker without GPUs can still serve the
  CPU slots of a mixed experiment. While remote workers are attached and GPU
  slots are pending with no local GPU, the run waits for a GPU worker and
  says so in its event stream. Not supported yet: planning a GPU experiment
  on a coordinator that has no GPU itself.

  **The hook for hosted runners.** Runners that reuse the worker code need
  one thing: give the worker's registry a GPU pool (`Registry.GPUs =
  gpu.NewPool(mode, gpus, sampleMS, smi)`, with `gpus` from `gpu.Detect` on
  the runner or the GPUs the sandbox provider declares). Registration then
  carries the GPUs (`engine.WorkerCapabilities`), the coordinator leases by
  them, and the pipeline holds them per run. Nothing else changes.

### Hardware record and sampler

Every run of a GPU task records `hardware` in its sealed `run.json`
(`schemas.HardwareRecord`): the GPUs it held (index, UUID, name, memory,
driver, CUDA version, compute capability), the passthrough mode and the
device names docker was given. While the agent runs, a host-side
`nvidia-smi --query-gpu=... -lms <sample_ms> -i <uuids>` loop samples the
run's GPUs into `gpu.samples.jsonl` in the run folder (sealed with it; at
most 80,000 lines), after a baseline reading taken just before the agent
starts, and sums them into `hardware.sampler`. It runs on the host, outside
every container, so the agent cannot see, stop or forge it; because a run
holds its GPUs alone, attribution is exact. A sampler that cannot start or
stops early records why, and its numbers are unknown, never zero.

| Metric | Definition | Direction |
|---|---|---|
| `gpu_seconds` | seconds the run held its GPUs while the agent ran, times the number of GPUs, summed over sessions (sampler clock, else the engine's) | lower is better |
| `peak_vram_mb` | highest memory in use on any of the run's GPUs while the agent ran, minus that GPU's baseline reading (a desktop card's display holds some) | lower is better |
| `gpu_util` | mean `utilization.gpu` over every sample, as a fraction | higher is better |
| `speedup` | the verifier's measured speedup (median of per-round ratios) | higher is better |
| `heldout_score` | the verifier's held-out score, as a fraction | higher is better |
| `time_to_target_s` | seconds the agent's training needed to reach the target, run by the verifier | lower is better |

All six are not applicable on a run that holds no GPU. The three verifier
metrics are not applicable when a decided grade does not carry them and
unknown when the grade itself is missing or undecided. The interval of a
speedup is in the reward dimensions `speedup_lo` and `speedup_hi`
(`reward_speedup_lo`, `reward_speedup_hi` per run).

### Hardware parity

The verdict instrument `hardware_parity` reads the sealed hardware record of
every counted run. When the counted GPU runs of the compared arms span more
than one GPU model (name and memory size, so a 4 GiB and an 8 GiB card of one
name are two models), the check fails and the verdict is **INVALID** with the
reason, for example `hardware parity: counted GPU runs ran on different GPU
models (arm A: 2 on GTX 1650 4096 MiB; arm B: 1 on RTX 4090 24564 MiB), so a
difference between the arms could be the hardware`. An experiment without
GPU runs gets no such check, so its analysis reads exactly as before.

## Grading on the GPU, on data the agent never sees

The examples use three patterns; each verifier's `requirements.md` maps its
dimensions to instruction lines.

1. **Weights only, fixed secret held-out set** (`train-classifier`). The
   agent leaves `model.pt` and `model.json` as artifacts. The verifier loads
   the weights with `torch.load(weights_only=True)` (tensors and plain
   containers only, no pickled code) into its own copy of the model class
   and scores them on held-out points from a generator that only the hidden
   verifier folder holds, with a fixed seed. No code of the agent runs in
   the verifier, the agent's container never has the generator or the seed,
   and a regrade scores the same set.
2. **The agent's training code, timed in a child process**
   (`fix-slow-loop`). The agent's code has to run to be timed, so it runs in
   a child process of the verifier (`train_harness.py`), with the
   verifier's own `data.py` and `model.py` first on its import path. The
   verifier holds the clock: it times each `train()` call from its "go" to
   the child's "done", never the child's start-up; it reads the weights the
   moment "done" arrives (a reply sent before training finished finds no
   file) and kills the child's process group after. Each round draws a
   fresh data seed from `os.urandom`, so weights trained in advance cannot
   match. After the last round it kills every other process in the
   container, and only then draws the held-out points with another fresh
   seed: held-out points never exist while any of the agent's code runs.
   The original loop is timed the same way in the same verification, in
   alternating order, so the speedup compares two loops on the same card at
   the same moment.
3. **The agent's kernel, checked and timed in a child process**
   (`fused-softmax`, KernelBench). Correctness first: every case is seeded
   from `os.urandom` at grading time and compared, in the verifier's own
   process, with a float64 reference. Then alternating timed rounds of the
   original and the agent's kernel: each round makes fresh inputs in the
   child, the verifier times "go" itself, and checks one output it picks at
   random against its own reference, so a fast round that returns wrong or
   cached results fails. The child keeps references to every torch function
   it uses before it imports the module under test, refuses PyTorch's own
   softmax (`fused-softmax`), and reports whether a torch function or
   dispatch mode was installed at measurement time, which fails the round.

The speedup is the median of the per-round ratios. With 9 rounds its
interval is the 2nd smallest and 2nd largest ratio, a distribution-free
interval of about 96% for the median; with 3 rounds (`fix-slow-loop`) it is
the smallest and largest, about 75%. `fused-softmax` passes on the
interval's lower end, as KernelBench tasks do when imported with a
`speedup_target`; `fix-slow-loop` passes on the median, because on a shared
machine one of three rounds can be far slower than the others.

### What the child-process design does not stop

The agent's code is in the child process, so it can do anything a Python
process can do there, including reading the hidden verifier folder mounted
in that container. That is why the patterns 2 and 3 keep no secret in it:
their held-out points and inputs are drawn from `os.urandom` at grading
time (pattern 1, whose verifier runs no agent code, is the one with a
stored secret). The child cannot change the verifier's clock, its reference
outputs or its verdict, and it never sees held-out data. But a module that
writes forged replies to the harness's reply channel, or that hooks
PyTorch below the level the harness checks, could report a kernel as done
before its work is finished. The random output check per round catches a
wrong result, not a late one. KernelBench's own guide lists such reward
hacks (cached results, a non-default CUDA stream, modified inputs); the
admission exploits here cover the common ones (PyTorch's own softmax through
the ATen op, an untrained model, zeros of the right shape, planted reward
files), not every one.

## The CUDA image

`examples/acme-gpu/benchmarks/acme-gpu/base/Dockerfile`, tag
`heimdall-example/acme-gpu-cuda:1`, is the one image for the tasks, the
scripted controls, the verifier and the imported KernelBench suite.

- **PyTorch 2.15.0 from PyPI, the CUDA 13.2 build.** PyTorch's wheel build
  table (`.ci/wheel/linux/build_env_setup.py` at tag v2.15.0, commit
  31a77fc9) builds CUDA 13.2 x86_64 wheels for compute capability 7.5, 8.0,
  8.6, 9.0, 10.0 and 12.0; the CUDA 12.6 build also covers 5.0, 6.0 and 7.0.
  In the image, `torch.cuda.get_arch_list()` printed `['sm_75', 'sm_80',
  'sm_86', 'sm_90', 'sm_100', 'sm_120']`, and a matmul, a convolution
  (cuDNN 9.26) and fused Adam ran on the GTX 1650 inside the hardened
  container. CUDA 13 dropped Maxwell, Pascal and Volta (sm_50, sm_60,
  sm_70): those cards need the CUDA 12.6 wheels instead.
- **Kernel route: Triton 3.9, bundled with PyTorch** (a PyTorch dependency,
  so no extra download). A fused softmax compiled and ran on sm_75: 0.19 ms
  against 0.83 ms for the five-kernel version and 0.22 ms for
  `torch.softmax` on 4096 x 1000 float32. Triton compiles a small C
  launcher at run time, hence gcc and libc6-dev (the compiler alone is
  about 85 MB). The other routes were not needed: CuPy RawKernel through NVRTC would add the CuPy
  wheel (69 MB for `cupy-cuda13x`); PyTorch `load_inline` needs nvcc and the
  full CUDA headers.
- **Runtime only.** No CUDA toolkit or `devel` base image: the CUDA
  libraries are the pip wheels PyTorch itself depends on, installed one
  large wheel per layer with `--no-deps` and pinned, plus `pip check`; the
  driver comes from the host through the toolkit. The base is the same
  `python:3.12-slim` digest as the coding-core image.
- **Size: 9.36 GB as `docker image ls` reports it on the test host**, which
  uses the containerd image store: that figure counts the unpacked layers
  (about 5.8 GB) and the compressed layer blobs (about 3 GB) the store keeps
  beside them. Over the 7 GB budget this work had. What makes it big, unpacked
  in `site-packages` (5.3 GB): the CUDA 13 libraries under `nvidia/cu13`
  (cuBLAS, cuFFT, cuSOLVER, cuSPARSE, NVRTC and the rest, 1.7 GB), PyTorch
  itself (1.1 GB, of which `libtorch_cuda.so` 520 MB and `libtorch_cpu.so`
  448 MB), Triton (996 MB), cuDNN (848 MB), NCCL (242 MB), cuSPARSELt
  (224 MB) and NVSHMEM (193 MB). `libtorch_cuda.so` links cuBLAS, cuDNN,
  cuFFT, cuSPARSE, cuSPARSELt, NCCL, cuFile, NVRTC and (through
  `libtorch_nvshmem.so`) NVSHMEM directly (`readelf -d` in the image), so
  none of them can be left out of a PyPI install; Triton is a separate
  dependency, needed by `torch.compile` and the kernel tasks.
- **The slimmer route, not taken:** for kernel-only tasks, CuPy with NVRTC
  and cuBLAS from pip (roughly 1.5 GB unpacked, estimated from the wheel
  sizes on PyPI, not built); for training tasks, a PyTorch built
  from source without NCCL, NVSHMEM and cuSPARSELt and for one compute
  capability, or a classic Docker store without the duplicate compressed
  blobs. Each trades generality or build time for size.

## Determinism and noise

- **Seeds.** Every reference and control seeds PyTorch (`torch.manual_seed`).
  `train-classifier` grades on a fixed held-out set, so a regrade of the same
  weights gives the same score. `fix-slow-loop`, `fused-softmax` and
  KernelBench draw their seeds from `os.urandom` at grading time on purpose
  (nothing to precompute, nothing to leak), so a regrade sees new inputs;
  their pass rules leave room for that.
- **GPU arithmetic is not guaranteed bit-for-bit repeatable** (atomics,
  cuBLAS and cuDNN algorithm choice). On this card the `train-classifier`
  reference reached the same held-out accuracy, 0.9187, in all 9 of its
  trainings (3 admission, 1 experiment, 5 repeats), while the original and
  the fixed loop of `fix-slow-loop`, which shuffle with different random
  streams, differ by up to 0.4 points on the same data. The tolerances (1e-5 absolute and
  1e-4 relative for softmax, 1e-2 for KernelBench as upstream, 1.5 points of
  accuracy for the training loop) are wide enough for that and narrow
  enough to catch the mutants.
- **Timing noise.** The test host was shared with about 20 other agents
  (load average 15 to 28 on 8 cores) during every measurement here. The
  verifiers time on the GPU with the original and the agent's code in
  alternating order within one verification, so slow periods hit both; the
  verifier container has 2 CPUs. Under that load per-round ratios of one
  verification of the `fix-slow-loop` reference ranged from 2.9 to 7.0. That
  is why a speedup is reported with its interval and decided on the median
  or the interval's lower end, why the targets sit well below what the
  references reach, and why trials matter: one run of one task is one noisy
  draw, and the experiment's paired test over tasks and trials is what
  decides.
- **Repeats.** Admission runs every reference three times and requires
  three passes; 5 more runs of each reference passed too, and the spread is
  under [Results](#results).

## Example: `examples/acme-gpu`

Three tasks that fit 4 GiB and a few minutes each. See its
[README](../examples/acme-gpu/README.md).

| Task | The agent | Verifier (clean room, GPU) | Pass | Mutants (must fail) |
|---|---|---|---|---|
| `train-classifier` | trains a ticket router on 20,000 points (`train.npz`) and leaves `model.pt` and `model.json` | loads the weights only, scores 10,000 held-out points from a hidden generator | held-out accuracy at least 0.905 | the starter script as is (about 0.83); a trained model with the wrong `model.json` |
| `fix-slow-loop` | makes `train()` at least 2.5 times faster, same model, optimizer, epochs and batch | times the original and the agent's loop in child processes on fresh data, 3 rounds; held-out points drawn after every child is gone | accuracy within 1.5 points of the original's in every round, median speedup at least 2.5 | the same slow loop without its host syncs; the fast loop with 2 epochs instead of 10 |
| `fused-softmax` | writes a fused softmax kernel at least twice as fast on 4096 x 4096 float32 | 10 correctness cases against float64; 9 alternating timed rounds of 6 calls with a random output checked per round | every case correct, interval lower end at least 2.0 | no max subtraction (overflows); the original plus a copy |

Each also has an empty control and an exploit (planted reward files plus a
cheat: an untrained model, an instant `train()` on shrunken data, PyTorch's
softmax through its ATen op).

## Importer: KernelBench

`bifrost-bench import kernelbench -from <clone of KernelBench> -suite <id>
[-ids 1,9,12,50,88] [-set level=1] [-set max_input_mb=256] [-set speedup_target=0] [-set
image=<tag>] [-set min_vram_mb=2048]`.

`-set level` picks the upstream folder: 1 (100 single-operator problems,
the default), 2 (100 fused-operator problems) or 3 (50 full architectures);
task ids are `l<level>-<nnn>-<name>`, and one suite holds one level. Level
4 (20 problems that load Hugging Face transformers models) is refused: it
needs the transformers package and weights downloaded from the network.

Verified on 18 Nov 2026 from the primary source,
<https://github.com/ScalingIntelligence/KernelBench> at commit 423217d9
(5 Mar 2026): the LICENSE file is the MIT license (GitHub's license detector
labels it "Other"); level 1 is 100 files `KernelBench/level1/<n>_<Name>.py`,
each with `class Model`, `get_inputs()` and `get_init_inputs()`;
`src/kernelbench/eval.py` builds `Model` and `ModelNew` after the same seed
and compares their outputs in float32 with atol = rtol = 1e-2 over several
random draws before timing (upstream calls this fast_p for a speedup above
p). Upstream ships no `ModelNew` solutions.

Each problem becomes a diff task (`ml_engineering` track, one GPU):
`problem.py` (upstream, unchanged, restored before grading), a
`solution.py` starter whose `ModelNew` raises, and a hidden verifier
(pattern 3 above, upstream's tolerance). Controls: the reference is
`ModelNew = Model` unchanged (it proves the verifier and the correctness
check, at a speedup of about 1), the empty control is the starter, the
mutant skews the output by 10% and 0.1, the exploit returns zeros of the
right shape and plants reward files. By default a pass needs correctness
only (fast_0) and the speedup is reported; `-set speedup_target=1` is
fast_1, and the unchanged reference can then not be admitted.

**Size filter.** Many level-1 problems are far larger than a 4 GiB card:
`19_ReLU` alone makes a 4096 x 393216 float32 input, 6 GiB. The importer
estimates `get_inputs()` statically (module-level integer constants and
`torch.rand`-like calls; anything else is unknown) and skips, with the
reason, every problem over `max_input_mb` (default 256) or of unknown size.
The estimate also reads unpacking assignments (`depth, height, width = 24,
48, 48`) and tuple constants unpacked with `*` (`torch.rand(batch_size,
*input_shape)`). At commit 423217d9 with the default 256 MiB that imports
28 of 100 level-1, 79 of 100 level-2 and 41 of 50 level-3 problems; with
`-set max_input_mb=16384` it imports 96, 97 and 49, and the rest are skipped
with the reason (inputs built in a loop or from another tensor's shape, a
name it cannot evaluate, or one level-2 problem whose inputs are over 16 GiB). A larger card takes `-set max_input_mb=...`. The filter
reads inputs only: outputs and intermediates are not knowable without
running the model, so admission is the real check.
`9_Tall_skinny_matrix_multiplication_` has 8 MiB of input and a 32768 x 32768 output (4 GiB); its
reference fails admission with CUDA out of memory on the 4 GiB card (the
verifier then reports the dimension `out_of_memory`), so it is not
admitted. The verifier deletes each saved output after reading it, because
`/tmp` in the verifier is a 512 MiB tmpfs and one output of the AlexNet-like
`50_conv_standard_2D` problem is 297 MB.

## Results

All on 18 Nov 2026, on the GTX 1650 test host, with scripted controls, at
no cost. The host was shared with about 20 other agents throughout (load
average 15 to 28 on 8 cores).

**Passthrough and `gpu check`.** Both modes (`cdi`, `device_requests`) saw
the GPU, alone, in a container with every capability dropped, a read-only
root, no network and uid 65534; PyTorch in the CUDA image ran a matmul, a
convolution (cuDNN 9.26), fused Adam and a Triton kernel under the agent's
hardening (uid 1000, `--read-only`, `--cap-drop ALL`, `no-new-privileges`,
`--network none`).

**Admission of `acme-gpu`** (`suite admit`, about 30 minutes): all three
tasks admitted. Each reference passed 3 of 3; the empty control, both
mutants, the exploit and the null agents (noop, refuse) failed on every
task. The first admission of `fused-softmax` failed one reference run:
the benchmark was then 24 calls on 2048 x 2048, where a fused call takes
about 0.1 ms and the Python launch overhead of a loaded host is a large
part of it, so one round's ratio fell below 2. It was changed to 6 calls on
4096 x 4096 (GPU-bound: 3.3 ms against 0.83 ms per call), re-locked and
admitted with 3 of 3.

**Verifier runs by hand** (the clean room's container settings, the
reference patch or control applied):

| Task | Control | Result |
|---|---|---|
| `fix-slow-loop` | reference | speedup median 3.78 (3 rounds 3.24 to 4.67), held-out 0.916 against the original's 0.912: pass; an earlier run 3.73 (2.88 to 7.05) |
| `fix-slow-loop` | mutant without host syncs | speedup 0.75: fail |
| `fix-slow-loop` | mutant with 2 epochs | speedup 13.5, held-out 0.917 against 0.946: accuracy dropped, fail |
| `fused-softmax` | reference, 4096 x 4096 | 3.94 (interval 3.90 to 4.02) and 3.96 (3.79 to 3.98): pass |
| `fused-softmax` | mutant without max subtraction | wrong on the 1e4-scaled case: fail |
| `fused-softmax` | mutant, original plus a copy | 0.81 (0.81 to 0.82): fail |
| `fused-softmax` | empty | 1.00 (0.87 to 1.02): fail |

**Spread of the references**, 5 more verifications each outside
admission, with the clean room's container settings (on top of the 3
admission runs and the experiment):

| Task | Passed | Spread |
|---|---|---|
| `train-classifier` | 5 of 5 | held-out accuracy 0.9187 every time (target 0.905): the reference's training is bit-for-bit repeatable on this card, and the held-out set is fixed |
| `fix-slow-loop` | 5 of 5 | median speedup 4.31 to 4.89 (target 2.5; smallest single round 3.61); agent accuracy 0.4 points below to 0.02 points above the original's (tolerance 1.5) |
| `fused-softmax` | 5 of 5 | median speedup 4.02 to 4.08, interval lower ends 3.74 to 4.07 (target 2.0) |

**Experiment `e_gpu_smoke_2itf`** (`experiments/gpu-smoke.yaml`, oracle
against noop, 6 slots, through the bench's stack with scripted controls):
arm A 3 of 3, arm B 0 of 3; verdict INCONCLUSIVE (3 tasks cannot reach
significance; this is a wiring check), marked SYNTHETIC; `hardware_parity`
PASS, "every counted GPU run ran on NVIDIA GeForce GTX 1650 4096 MiB". The
six runs ran one after another on the one GPU worker. Every sealed
`run.json` holds the GPU, `mode: cdi` and the device name; every run folder
holds `gpu.samples.jsonl`.

| Run | Task, arm | Outcome | gpu_seconds | peak_vram_mb | gpu_util | Verifier |
|---|---|---|---|---|---|---|
| `r_r8cg6i87wy` | train-classifier, oracle (trains in the agent container) | pass | 18.7 | 113 | 0.13 | heldout_score 0.919 |
| `r_w53744q88w` | train-classifier, noop | fail (artifact missing) | 0.8 | 0 | 0 | not run |
| `r_khdtmbp3oi` | fix-slow-loop, oracle | pass | 0.4 | 0 | 0 | speedup 4.63, heldout_score 0.924, time_to_target_s 1.02 |
| `r_ju4kbakhph` | fix-slow-loop, noop | fail | 0.8 | 0 | 0 | speedup 1.00, heldout_score 0.939, time_to_target_s 3.69 |
| `r_lfjl05k266` | fused-softmax, oracle | pass | 0.4 | 0 | 0.03 | speedup 4.05 |
| `r_xm1tusjlup` | fused-softmax, noop | fail | 0.4 | 0 | 0 | speedup 1.00 |

The diff-task oracles only apply a patch, so their agent phase is under a
second and the GPU numbers are near zero, as they should be; the GPU work
of those tasks happens in the verifier, which the sampler does not cover
(see Limits).

**KernelBench.** At commit 423217d9 the importer wrote 28 of the 100
level-1 problems and skipped 72 with the reason (59 over 256 MiB of input,
13 of unknown size). Five were tried on the GTX 1650 (suite
`kernelbench-l1`, locked): four were admitted, `1_Square_matrix_multiplication_`,
`12_Matmul_with_diagonal_matrices_`, `50_conv_standard_2D__square_input__square_kernel`
and `88_MinGPTNewGelu` (reference 3 of 3; empty starter, skewed mutant,
zeros exploit, noop and refuse failed), and one was not:
`9_Tall_skinny_matrix_multiplication_`, whose reference failed 3 of 3 with
CUDA out of memory (a 4 GiB output). By hand, the unchanged-model
reference of problem 50 measured a speedup of 0.99 (interval 0.80 to 1.00),
as an unchanged model should.

## Limits

- One consumer GPU (4 GiB, Turing, a display attached) is not
  representative of data-centre GPUs: memory, bandwidth, tensor cores, MIG
  partitions and NVLink all differ, and a speedup measured here does not
  carry over. Hardware parity keeps a comparison on one model; it does not
  make numbers from two models comparable.
- Cloud GPUs: an organization whose default worker is Modal runs GPU tasks
  in Modal GPU sandboxes (one GPU type per runner, `HEIMDALL_SANDBOX_GPU`,
  default `L4`; see `docs/open-models.md`), and a RunPod worker is a GPU
  pod. The host-side sampler is off there. The Kubernetes driver refuses
  GPU tasks for now (`docs/kubernetes.md` owns that driver).
- Only whole GPUs: no MIG, no fractional sharing, no time slicing. One run
  per GPU is a rule, not a measurement of contention.
- The GPU pool belongs to one bench process. Two bench processes on one
  machine (two experiments, or an experiment and a `suite admit`) do not
  share it and can put two runs on one GPU at once; run one at a time, or
  give each its own GPUs with `gpu.devices`.
- The host-side sampler sees the whole GPU. One run per GPU makes that the
  run's, but a desktop display or another process outside the bench on the
  same card is in the numbers too; `peak_vram_mb` subtracts the baseline
  reading, `gpu_util` does not.
- `gpu_seconds` is held time, not busy time: an agent that thinks for ten
  minutes and trains for one still holds the GPU for eleven.
- The sampler covers the agent's sessions only. The verifier holds the same
  GPU (nothing else can use it meanwhile), but its GPU time and memory are
  not sampled or reported: they are the bench's cost of grading, not the
  agent's.
- The hosted server plans GPU experiments from the GPUs the org's workers
  declare (registered remote GPU workers, a Modal default worker); a local
  coordinator still needs a GPU of its own to plan one.
- No GPU team runs and no GPU tasks on the Kubernetes driver.
- The child-process timing protocol raises the bar for cheating; it is not
  a sandbox inside the sandbox (see above).
- The image is large (above) and was built on the test host; its layers are
  not pinned by digest in the lock (a local build), only its Dockerfile and
  requirements.
