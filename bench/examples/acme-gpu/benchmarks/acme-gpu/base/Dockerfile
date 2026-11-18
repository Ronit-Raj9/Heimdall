# One CUDA image for the acme-gpu suite: the agent, the scripted controls and
# the verifier all run in it, so a model or kernel the agent writes is loaded
# by the verifier with exactly the libraries it was built against.
#
# Why this shape (docs/gpu-tasks.md has the details and the dates):
# - PyTorch 2.15.0 from PyPI is the CUDA 13.2 build. Its wheel build table
#   (.ci/wheel/linux/build_env_setup.py at tag v2.15.0) compiles CUDA 13.2
#   x86_64 wheels for compute capability 7.5, 8.0, 8.6, 9.0, 10.0 and 12.0,
#   so a Turing card (GTX 1650, sm_75) gets native kernels. CUDA 13 dropped
#   sm_50, sm_60 and sm_70, so Maxwell, Pascal and Volta cards are out.
# - No CUDA toolkit image: the CUDA libraries come as the pip wheels PyTorch
#   itself depends on, and the driver (libcuda) is injected by the NVIDIA
#   container toolkit through CDI at run time. The base is the same
#   python:3.12-slim digest the coding-core image uses, so its layers are
#   shared.
# - Kernel tasks use Triton, which PyTorch already depends on (no extra
#   download). Triton compiles a small C launcher at run time, hence gcc and
#   libc6-dev.
# - Each large wheel is installed in its own layer with --no-deps, so the
#   build never holds every downloaded wheel and every installed file at once
#   (the build host's disk is shared). The last layer installs the small
#   pure-Python dependencies from requirements.txt, also with --no-deps, so
#   every installed version is pinned here.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir --no-deps --no-compile nvidia-cudnn-cu13==9.26.0.51
RUN pip install --no-cache-dir --no-deps --no-compile nvidia-cublas==13.4.1.3 nvidia-cuda-nvrtc==13.2.86 \
    nvidia-cuda-runtime==13.2.86 nvidia-nvjitlink==13.4.92 nvidia-cuda-cupti==13.2.86 nvidia-nvtx==13.2.86 \
    nvidia-cufile==1.17.1.22 nvidia-curand==10.4.2.66
RUN pip install --no-cache-dir --no-deps --no-compile nvidia-cufft==12.2.0.57 nvidia-cusolver==12.2.0.11 \
    nvidia-cusparse==12.7.10.12
RUN pip install --no-cache-dir --no-deps --no-compile nvidia-nccl-cu13==2.30.7 nvidia-nvshmem-cu13==3.7.2 \
    nvidia-cusparselt-cu13==0.8.1
RUN pip install --no-cache-dir --no-deps --no-compile triton==3.9.0
RUN pip install --no-cache-dir --no-deps --no-compile torch==2.15.0
COPY requirements.txt /opt/acme-gpu/requirements.txt
RUN pip install --no-cache-dir --no-deps --no-compile -r /opt/acme-gpu/requirements.txt \
 && pip check \
 && pip freeze > /opt/acme-gpu/pip-freeze.txt
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
# The root file system is read-only for the agent and the verifier; only /tmp
# and the home folder are writable, so the compiler caches go to /tmp.
ENV PYTHONDONTWRITEBYTECODE=1 \
    TRITON_CACHE_DIR=/tmp/triton-cache \
    TORCHINDUCTOR_CACHE_DIR=/tmp/inductor-cache \
    TRITON_DEFAULT_FP_FUSION=1 \
    CUDA_MODULE_LOADING=LAZY
WORKDIR /workspace
USER agent
