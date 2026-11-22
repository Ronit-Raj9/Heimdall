#!/usr/bin/env bash
# Heimdall runner installer for a fresh Ubuntu or Debian box.
#
# One line, with the runner token of a worker (Workers page, "Your own VM"):
#
#   curl -fsSL https://heimdall.example.com/api/bench/runners/install.sh | sudo HEIMDALL_RUNNER_TOKEN=hmr_... sh
#
# Piped into a shell, the script cannot run from standard input (later steps
# read it), so the POSIX block below downloads it again to a file and runs
# that copy with bash. The hosted server fills in the three defaults below
# when it serves the script: its address, a git repository (only used with
# --source git) and the commit it runs, whose CLI binary and project bundle
# it serves under /downloads.
HEIMDALL_DEFAULT_SERVER=""
HEIMDALL_DEFAULT_REPO=""
HEIMDALL_DEFAULT_REF=""
if [ -z "${BASH_VERSION:-}" ] || [ ! -f "${0:-}" ] || [ -n "${HEIMDALL_INSTALL_REFETCH:-}" ]; then
  if [ -n "${HEIMDALL_INSTALL_SELF:-}" ]; then
    echo "error: the downloaded installer could not start with bash" >&2
    exit 1
  fi
  _h_server=${HEIMDALL_SERVER:-$HEIMDALL_DEFAULT_SERVER}
  if [ -z "$_h_server" ]; then
    echo "error: set HEIMDALL_SERVER=https://<your Heimdall server> (or download install.sh from your server)" >&2
    exit 1
  fi
  command -v bash >/dev/null 2>&1 || { echo "error: bash is required (apt-get install -y bash)" >&2; exit 1; }
  command -v curl >/dev/null 2>&1 || { echo "error: curl is required (apt-get install -y curl ca-certificates)" >&2; exit 1; }
  _h_tmp=$(mktemp /tmp/heimdall-install.XXXXXX) || exit 1
  if ! curl -fsSL --proto '=https,http' "${_h_server%/}/api/bench/runners/install.sh" -o "$_h_tmp"; then
    rm -f "$_h_tmp"
    echo "error: could not download ${_h_server%/}/api/bench/runners/install.sh" >&2
    exit 1
  fi
  HEIMDALL_SERVER=${_h_server%/} HEIMDALL_INSTALL_SELF=$_h_tmp HEIMDALL_INSTALL_REFETCH="" exec bash "$_h_tmp" "$@"
  exit 1
fi
#
# A runner is `bifrost-bench runner start`: it leases slots from a hosted
# Heimdall server over HTTPS, plans each experiment again with its own copy of
# the project (suites, agents, deploy/bench.yaml at the server's commit), runs
# the agent containers in the local Docker through a local Bifrost gateway
# stack, and uploads the sealed run folders.
#
# This script installs, in order: a system user, the project at the server's
# commit (by default the bundle the server serves at
# /downloads/bench-<commit>.tar.gz; a git clone with --repo or --source git),
# the bench binary (by default /downloads/bifrost-bench-linux-<arch> from the
# server; built from source in git mode, or a prebuilt release tarball),
# deploy/.env, the runner token, a systemd unit, the agent and task images,
# and the local gateway stack (deploy/docker-compose.yml). Running it again
# with a new --ref is the upgrade. See README.md next to this file.
#
# Usage (as root):
#   bash install.sh --server https://heimdall.example.com --token-file ./runner-token
#   bash install.sh --help
set -euo pipefail

# ---------------------------------------------------------------- constants

readonly SERVICE="heimdall-runner"
readonly UNIT_PATH="/etc/systemd/system/heimdall-runner.service"
readonly ETC_DIR="/etc/heimdall-runner"
readonly TOKEN_PATH="${ETC_DIR}/token"
readonly LOCK_PATH="/run/heimdall-runner-install.lock"

# Go toolchain installed only when no usable `go` is found. The checksums are
# the official ones from https://go.dev/dl/ for this version; any other
# version (a newer go.mod requirement, or HEIMDALL_GO_VERSION) is checked
# against the .sha256 file published next to the tarball.
readonly GO_VERSION_PINNED="1.26.5"
readonly GO_SHA256_AMD64="5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053"
readonly GO_SHA256_ARM64="fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49"
GO_VERSION="${HEIMDALL_GO_VERSION:-$GO_VERSION_PINNED}"

readonly NAME_RE='^[A-Za-z0-9._-]{1,64}$'
readonly LABEL_RE='^[A-Za-z0-9._:=-]{1,64}(,[A-Za-z0-9._:=-]{1,64}){0,31}$'
readonly TOKEN_RE='^hmr_[a-z0-9]{8}_[A-Za-z0-9_-]{20,128}$'
readonly SERVER_RE='^(https?)://([A-Za-z0-9.-]+)(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$'
readonly USER_RE='^[a-z_][a-z0-9_-]{0,31}$'
readonly REF_RE='^[A-Za-z0-9._/-]{1,128}$'
readonly COMMIT_RE='^[0-9a-f]{7,40}$'
readonly SHA_RE='^[0-9a-f]{64}$'
readonly CAP_RE='^[0-9]{1,7}(\.[0-9]{1,4})?$'

# Images built when bench/Makefile cannot be read (dry run on a fresh box).
# The real run reads the list from the `images:` target of the checked-out
# Makefile, so it follows the project.
readonly FALLBACK_IMAGES=(
  "bench/coding-core-base:1 benchmarks/coding-core/base"
  "bench/bifrost-bench:dev ."
  "bench/agent-claude-code:1 agents/images/claude-code"
  "bench/agent-codex:1 agents/images/codex"
  "bench/agent-mini-swe-agent:1 agents/images/mini-swe-agent"
  "bench/agent-opencode:1 agents/images/opencode"
  "bench/agent-gemini-cli:1 agents/images/gemini-cli"
  "bench/agent-custom-python:1 agents/images/custom-python"
)

# ---------------------------------------------------------------- options

# Every option may also come from the environment (the one-line install and
# the platform drivers of package byoworker set these).
SERVER="${HEIMDALL_SERVER:-$HEIMDALL_DEFAULT_SERVER}"
TOKEN_FILE=""
TOKEN_STDIN=0
POOL="${HEIMDALL_POOL:-default}"
SLOTS="${HEIMDALL_SLOTS:-1}"
RUNNER_NAME="${HEIMDALL_RUNNER_NAME:-}"
LABELS="${HEIMDALL_LABELS:-}"
SPEND_CAP=""
REF="${HEIMDALL_REF:-${HEIMDALL_DEFAULT_REF:-}}"
REPO="${HEIMDALL_REPO:-${HEIMDALL_DEFAULT_REPO:-https://github.com/maximhq/bifrost.git}}"
# SOURCE is where the project and binary come from: server (the bundle and
# binary the Heimdall server serves under /downloads, the default) or git (a
# clone of REPO, the binary built from source). Naming a repository
# (--repo or HEIMDALL_REPO) means git.
SOURCE="${HEIMDALL_SOURCE:-}"
if [ -z "$SOURCE" ] && [ -n "${HEIMDALL_REPO:-}" ]; then
  SOURCE=git
fi
FOREGROUND=0
INSTALL_DOCKER=0
MIN_DISK_GB="20"
RELEASE_URL=""
RELEASE_SHA256=""
PREFIX="/opt/heimdall-runner"
RUN_USER="heimdall-runner"
SKIP_IMAGES=0
SKIP_STACK=0
DRY_RUN=0
FORCE=0
DEBUG=0

TOKEN=""
WORK_TMP=""

usage() {
  cat <<'EOF'
Install or upgrade a Heimdall runner on this Ubuntu/Debian box.

Usage: install.sh --server URL (--token-file PATH | --token-stdin) [options]

Required:
  --server URL           Hosted Heimdall server, https://host[:port][/path].
                         Plain http is accepted only for 127.0.0.1 or
                         localhost (an SSH tunnel).
  --token-file PATH      File holding the runner token (hmr_<prefix>_<secret>).
  --token-stdin          Read the runner token from standard input instead.
                         On an upgrade both may be left out: the installed
                         token is kept.
                         HEIMDALL_RUNNER_TOKEN in the environment works too
                         (the one-line install).

Runner:
  --pool NAME            Runner pool (default: default)
  --slots N              Slots run at once, 1-64 (default: 1)
  --name NAME            Runner name (default: this host's name)
  --labels a,b           Comma-separated runner labels
  --spend-cap-usd X      Local spend cap in USD for this runner
  --debug                Run the runner with debug logging

Source and binary:
  --source server|git    Where the project and the binary come from.
                         server (default): the Heimdall server's own
                         downloads at its commit,
                         <server>/downloads/bench-<commit>.tar.gz and
                         <server>/downloads/bifrost-bench-linux-<arch>, each
                         checked against its .sha256. git: clone --repo at
                         --ref and build the binary from source.
  --ref REF              The commit the hosted server runs (the server's own
                         install script fills it in). In git mode also a
                         branch or tag (default there: main).
  --repo URL             Git repository that holds bench/; implies
                         --source git (default repository:
                         https://github.com/maximhq/bifrost.git)
  --release-url URL      Download a prebuilt tarball holding bin/bifrost-bench
                         instead of building from source. {arch} in the URL
                         becomes amd64 or arm64, e.g.
                         https://github.com/<owner>/<repo>/releases/download/<version>/heimdall-runner-linux-{arch}.tar.gz
  --sha256 HEX           Expected sha256 of the release tarball

Layout and steps:
  --prefix DIR           Install root (default: /opt/heimdall-runner)
  --user NAME            System user the runner runs as (default: heimdall-runner)
  --skip-images          Do not build the task, bench and agent images
  --skip-stack           Do not start the local gateway stack
  --min-disk-gb N        Free disk the install needs, in GB (default: 20)
  --foreground           No systemd (a cloud sandbox or a container VM): run
                         the runner as this script's last step, in the
                         foreground, instead of installing a service
  --install-docker       Install git, curl and Docker (get.docker.com) when
                         they are missing, and start dockerd when no daemon
                         runs
  --force                Continue on an OS that is not Debian or Ubuntu
  --dry-run              Print every command instead of running it; changes
                         nothing and needs neither root nor Docker
  -h, --help             Show this help

Provider keys (ANTHROPIC_API_KEY, OPENAI_API_KEY, ...) go only in
<prefix>/src/bench/deploy/.env on this box, never on the hosted server.
EOF
}

# ---------------------------------------------------------------- helpers

log() { printf '==> %s\n' "$*"; }
note() { printf '    %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

is_dry() { [ "$DRY_RUN" -eq 1 ]; }

# print_cmd prints a command with every word shell-quoted.
print_cmd() {
  local w
  printf '[dry-run]'
  for w in "$@"; do
    case $w in
      # A placeholder for a value known only at run time; no checked value
      # holds < or >.
      *'<'*'>'*) printf ' %s' "$w" ;;
      *) printf ' %q' "$w" ;;
    esac
  done
  printf '\n'
}

# run runs a command, or prints it in a dry run.
run() {
  if is_dry; then
    print_cmd "$@"
  else
    "$@"
  fi
}

# run_in runs a command in a folder, or prints it in a dry run.
run_in() {
  local dir=$1
  shift
  if is_dry; then
    printf '[dry-run] cd %q &&' "$dir"
    printf ' %q' "$@"
    printf '\n'
  else
    (cd "$dir" && "$@")
  fi
}

# as_user runs a command as the runner user with its own HOME.
as_user() {
  run runuser -u "$RUN_USER" -- env HOME="$HOME_DIR" "$@"
}

# as_user_out runs a command as the runner user and prints its output; it is
# for read-only queries and runs only for real.
as_user_out() {
  runuser -u "$RUN_USER" -- env HOME="$HOME_DIR" "$@"
}

# write_file writes stdin to path with mode and owner, atomically. A dry run
# prints the content instead; never pass a secret through it.
write_file() {
  local path=$1 mode=$2 owner=$3 tmp
  if is_dry; then
    printf '[dry-run] write %s (mode %s, owner %s):\n' "$path" "$mode" "$owner"
    sed 's/^/[dry-run]   | /'
    return 0
  fi
  tmp=$(mktemp "${path}.XXXXXX")
  cat >"$tmp"
  chown "$owner" "$tmp"
  chmod "$mode" "$tmp"
  mv -f "$tmp" "$path"
}

cleanup() {
  if [ -n "$WORK_TMP" ] && [ -d "$WORK_TMP" ]; then
    rm -rf "$WORK_TMP"
  fi
  # The copy the one-line install downloaded.
  case ${HEIMDALL_INSTALL_SELF:-} in
    /tmp/heimdall-install.*) rm -f "$HEIMDALL_INSTALL_SELF" ;;
  esac
}
trap cleanup EXIT

# version_ge reports whether version $1 >= $2.
version_ge() {
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n 1)" = "$2" ]
}

trim() {
  local s=$1
  s="${s#"${s%%[![:space:]]*}"}"
  s="${s%"${s##*[![:space:]]}"}"
  printf '%s' "$s"
}

# ---------------------------------------------------------------- parse flags

parse_args() {
  local opt val inline
  while [ $# -gt 0 ]; do
    opt=$1
    val=""
    inline=0
    case $opt in
      --*=*)
        val=${opt#*=}
        opt=${opt%%=*}
        inline=1
        ;;
    esac
    case $opt in
      --server | --token-file | --pool | --slots | --name | --labels | --spend-cap-usd | \
        --ref | --repo | --source | --release-url | --sha256 | --prefix | --user | --min-disk-gb)
        if [ "$inline" -eq 0 ]; then
          [ $# -ge 2 ] || die "$opt needs a value (see --help)"
          val=$2
          shift 2
        else
          shift
        fi
        case $opt in
          --server) SERVER=$val ;;
          --token-file) TOKEN_FILE=$val ;;
          --pool) POOL=$val ;;
          --slots) SLOTS=$val ;;
          --name) RUNNER_NAME=$val ;;
          --labels) LABELS=$val ;;
          --spend-cap-usd) SPEND_CAP=$val ;;
          --ref) REF=$val ;;
          --repo)
            REPO=$val
            [ -n "$SOURCE" ] || SOURCE=git
            ;;
          --source) SOURCE=$val ;;
          --release-url) RELEASE_URL=$val ;;
          --sha256) RELEASE_SHA256=$val ;;
          --prefix) PREFIX=$val ;;
          --user) RUN_USER=$val ;;
          --min-disk-gb) MIN_DISK_GB=$val ;;
        esac
        ;;
      --token)
        die "never pass the token on the command line; use --token-file PATH or --token-stdin"
        ;;
      --token-stdin | --skip-images | --skip-stack | --dry-run | --force | --debug | --foreground | --install-docker)
        [ "$inline" -eq 0 ] || die "$opt takes no value"
        case $opt in
          --foreground) FOREGROUND=1 ;;
          --install-docker) INSTALL_DOCKER=1 ;;
          --token-stdin) TOKEN_STDIN=1 ;;
          --skip-images) SKIP_IMAGES=1 ;;
          --skip-stack) SKIP_STACK=1 ;;
          --dry-run) DRY_RUN=1 ;;
          --force) FORCE=1 ;;
          --debug) DEBUG=1 ;;
        esac
        shift
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      *)
        die "unknown option: $opt (see --help)"
        ;;
    esac
  done
}

validate_args() {
  local scheme host
  [ -n "$SERVER" ] || die "--server is required (see --help)"
  [[ $SERVER =~ $SERVER_RE ]] || die "--server must look like https://host[:port][/path] (letters, digits, . - _ ~ / only)"
  scheme=${BASH_REMATCH[1]}
  host=${BASH_REMATCH[2]}
  if [ "$scheme" = "http" ] && [ "$host" != "127.0.0.1" ] && [ "$host" != "localhost" ]; then
    die "--server must use https; plain http is accepted only for 127.0.0.1 or localhost (an SSH tunnel), since the token would cross the network in clear text"
  fi
  SERVER=${SERVER%/}

  if [ -n "$TOKEN_FILE" ] && [ "$TOKEN_STDIN" -eq 1 ]; then
    die "use --token-file or --token-stdin, not both"
  fi

  [[ $POOL =~ $NAME_RE ]] || die "--pool must match ${NAME_RE}"
  if [ -z "$RUNNER_NAME" ]; then
    RUNNER_NAME=$(uname -n)
    [[ $RUNNER_NAME =~ $NAME_RE ]] || die "this host's name '${RUNNER_NAME}' is not a valid runner name; pass --name matching ${NAME_RE}"
  fi
  [[ $RUNNER_NAME =~ $NAME_RE ]] || die "--name must match ${NAME_RE}"
  [[ $SLOTS =~ ^[1-9][0-9]?$ ]] && [ "$SLOTS" -le 64 ] || die "--slots must be a whole number from 1 to 64"
  if [ -n "$LABELS" ]; then
    [[ $LABELS =~ $LABEL_RE ]] || die "--labels must be comma-separated labels of letters, digits and . _ : = - (at most 32, 64 characters each)"
  fi
  if [ -n "$SPEND_CAP" ]; then
    [[ $SPEND_CAP =~ $CAP_RE ]] || die "--spend-cap-usd must be a number such as 25 or 12.50"
  fi

  case ${SOURCE:-server} in
    server | git) SOURCE=${SOURCE:-server} ;;
    *) die "--source must be server or git" ;;
  esac
  if [ "$SOURCE" = "server" ]; then
    [ -n "$REF" ] || die "the server did not say which commit it runs, so there is no bundle to download; pass --ref <the server's commit>, or --repo <git URL> to clone instead"
    REF=$(printf '%s' "$REF" | tr 'A-F' 'a-f')
    [[ $REF =~ $COMMIT_RE ]] || die "--ref ${REF} is not a commit; server downloads are named by commit (7 to 40 hex characters). For a branch or tag pass --repo <git URL> to clone instead"
  else
    REF=${REF:-main}
  fi
  [[ $REF =~ $REF_RE ]] && [[ $REF != -* ]] && [[ $REF != *..* ]] || die "--ref must be a branch, tag or commit (letters, digits and . _ / - only)"
  [[ $REPO =~ ^(https://|ssh://|git@)[A-Za-z0-9._~:/@+-]+$ ]] || die "--repo must be an https://, ssh:// or git@ URL"

  if [ -n "$RELEASE_URL" ]; then
    [[ $RELEASE_URL =~ ^https://[A-Za-z0-9._~:/?\&=%+@{}-]+$ ]] || die "--release-url must be an https URL"
  fi
  if [ -n "$RELEASE_SHA256" ]; then
    [ -n "$RELEASE_URL" ] || die "--sha256 applies only to --release-url"
    RELEASE_SHA256=$(printf '%s' "$RELEASE_SHA256" | tr 'A-F' 'a-f')
    [[ $RELEASE_SHA256 =~ $SHA_RE ]] || die "--sha256 must be 64 hex characters"
  fi

  [[ $PREFIX =~ ^/[A-Za-z0-9._/-]*[A-Za-z0-9._-]$ ]] || die "--prefix must be an absolute path of letters, digits and . _ / -"
  case "/${PREFIX#/}/" in
    */../* | */./* | *//*) die "--prefix must be a plain absolute path" ;;
  esac
  case $PREFIX in
    / | /bin | /boot | /dev | /etc | /home | /lib | /opt | /proc | /root | /run | /sbin | /srv | /sys | /tmp | /usr | /var)
      die "--prefix ${PREFIX} is a system folder; pick a folder of its own such as /opt/heimdall-runner"
      ;;
    /usr/* | /boot/* | /etc/* | /efi/* | /tmp/* | /proc/* | /sys/* | /dev/* | /run/*)
      die "--prefix must not be under /usr, /boot, /etc, /efi, /tmp, /proc, /sys, /dev or /run (the service sees /usr, /boot and /etc read-only)"
      ;;
  esac
  [[ $GO_VERSION =~ ^[0-9]{1,2}\.[0-9]{1,3}(\.[0-9]{1,3})?$ ]] || die "HEIMDALL_GO_VERSION must look like 1.26.5"
  [[ $RUN_USER =~ $USER_RE ]] || die "--user must be a lowercase system user name"
  [ "$RUN_USER" != "root" ] || die "--user must not be root"
  [[ $MIN_DISK_GB =~ ^[0-9]{1,5}$ ]] || die "--min-disk-gb must be a whole number of GB"

  SRC_DIR="${PREFIX}/src"
  BENCH_DIR="${SRC_DIR}/bench"
  HOME_DIR="${PREFIX}/home"
  BIN_PATH="${BENCH_DIR}/bin/bifrost-bench"
  ENV_FILE="${BENCH_DIR}/deploy/.env"
  ENV_EXAMPLE="${BENCH_DIR}/deploy/.env.example"
  CONFIG_PATH="${BENCH_DIR}/deploy/bench.yaml"
  GO_ROOT="${PREFIX}/go"
}

# read_token reads the runner token from --token-file or stdin into TOKEN. It
# is never printed and never put in a command's arguments.
read_token() {
  local t=""
  if [ -n "$TOKEN_FILE" ]; then
    [ -f "$TOKEN_FILE" ] || die "token file ${TOKEN_FILE} does not exist or is not a file"
    [ -r "$TOKEN_FILE" ] || die "token file ${TOKEN_FILE} is not readable"
    IFS= read -r t <"$TOKEN_FILE" || true
  elif [ "$TOKEN_STDIN" -eq 0 ] && [ -n "${HEIMDALL_RUNNER_TOKEN:-}" ]; then
    t=$HEIMDALL_RUNNER_TOKEN
    unset HEIMDALL_RUNNER_TOKEN # no child process inherits it
  elif [ "$TOKEN_STDIN" -eq 1 ]; then
    if [ -t 0 ]; then
      printf 'Runner token (input hidden): ' >&2
      IFS= read -rs t || true
      printf '\n' >&2
    else
      IFS= read -r t || true
    fi
  else
    return 0
  fi
  t=$(trim "$t")
  [ -n "$t" ] || die "the runner token is empty"
  [[ $t =~ $TOKEN_RE ]] || die "that does not look like a runner token (hmr_<8 characters>_<secret>); copy the whole token from Settings > Runners"
  TOKEN=$t
}

# ---------------------------------------------------------------- checks

check_os() {
  local id="" like=""
  if [ -r /etc/os-release ]; then
    id=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"' | head -n 1)
    like=$(sed -n 's/^ID_LIKE=//p' /etc/os-release | tr -d '"' | head -n 1)
  fi
  case " ${id} ${like} " in
    *" debian "* | *" ubuntu "*)
      log "OS: ${id} (supported)"
      return 0
      ;;
  esac
  if [ "$FORCE" -eq 1 ]; then
    warn "OS '${id:-unknown}' is not Debian or Ubuntu; continuing because of --force"
  elif is_dry; then
    warn "OS '${id:-unknown}' is not Debian or Ubuntu; a real run stops here unless --force is given"
  else
    die "OS '${id:-unknown}' is not Debian or Ubuntu; pass --force to try anyway"
  fi
}

check_root() {
  if is_dry; then
    return 0
  fi
  [ "$(id -u)" -eq 0 ] || die "run as root (sudo bash install.sh ...), or add --dry-run to preview"
}

detect_arch() {
  local m
  m=$(uname -m)
  case $m in
    x86_64 | amd64) ARCH="amd64" ;;
    aarch64 | arm64) ARCH="arm64" ;;
    *)
      if is_dry; then
        warn "architecture ${m} is not amd64 or arm64; a real run stops here"
        ARCH="amd64"
      else
        die "architecture ${m} is not supported (amd64 or arm64)"
      fi
      ;;
  esac
}

docker_missing() {
  cat >&2 <<'EOF'
Docker Engine with the compose plugin is required and is not installed by this
script. Install it with Docker's official script, then run this installer again:

  curl -fsSL https://get.docker.com -o get-docker.sh
  sudo sh get-docker.sh
  docker compose version

Or follow https://docs.docker.com/engine/install/ubuntu/ (or /debian/).
EOF
  if is_dry; then
    warn "$1; a real run stops here"
    return 0
  fi
  die "$1"
}

check_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    docker_missing "docker is not installed"
    return 0
  fi
  if ! docker compose version >/dev/null 2>&1; then
    docker_missing "the docker compose plugin is not installed (docker compose version failed)"
    return 0
  fi
  log "Docker: $(docker compose version --short 2>/dev/null || echo present) (compose plugin)"
  if ! docker info >/dev/null 2>&1; then
    if is_dry; then
      warn "the Docker daemon is not reachable as this user; a real run needs it running (systemctl start docker)"
    else
      die "the Docker daemon is not reachable; start it with: systemctl enable --now docker"
    fi
  fi
}

check_tools() {
  local missing=() t
  for t in curl openssl tar gzip sha256sum runuser flock sed awk sort; do
    command -v "$t" >/dev/null 2>&1 || missing+=("$t")
  done
  if [ "$SOURCE" = "git" ]; then
    command -v git >/dev/null 2>&1 || missing+=(git)
  fi
  if ! is_dry; then
    for t in useradd usermod getent install; do
      command -v "$t" >/dev/null 2>&1 || missing+=("$t")
    done
    if [ "$FOREGROUND" -eq 0 ]; then
      command -v systemctl >/dev/null 2>&1 || missing+=(systemctl)
    fi
  fi
  if [ ${#missing[@]} -gt 0 ]; then
    if is_dry; then
      warn "missing tools: ${missing[*]}; a real run stops here (apt-get install -y git curl openssl ca-certificates)"
    else
      die "missing tools: ${missing[*]}; install them with: apt-get install -y git curl openssl ca-certificates"
    fi
  fi
  if ! is_dry && [ "$FOREGROUND" -eq 0 ] && [ ! -d /run/systemd/system ]; then
    die "systemd is not running on this box; the runner is installed as a systemd service (or pass --foreground to run it in the foreground)"
  fi
}

# install_prereqs installs what a bare sandbox image lacks (--install-docker):
# the base tools with apt, Docker with Docker's own script, and a dockerd
# started by hand where no init system runs one.
install_prereqs() {
  local missing=() t i
  [ "$INSTALL_DOCKER" -eq 1 ] || return 0
  for t in git curl openssl tar sha256sum runuser flock; do
    command -v "$t" >/dev/null 2>&1 || missing+=("$t")
  done
  if [ ${#missing[@]} -gt 0 ]; then
    command -v apt-get >/dev/null 2>&1 || die "missing tools: ${missing[*]}, and no apt-get to install them"
    log "Installing base tools (${missing[*]})"
    run env DEBIAN_FRONTEND=noninteractive apt-get update -qq
    run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git curl openssl ca-certificates tar coreutils util-linux passwd
  fi
  if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    log "Installing Docker (https://get.docker.com)"
    if is_dry; then
      print_cmd curl -fsSL https://get.docker.com -o '<tmp>/get-docker.sh'
      print_cmd sh '<tmp>/get-docker.sh'
    else
      curl -fsSL https://get.docker.com -o "${WORK_TMP}/get-docker.sh"
      sh "${WORK_TMP}/get-docker.sh"
    fi
  fi
  if ! is_dry && ! docker info >/dev/null 2>&1 && command -v dockerd >/dev/null 2>&1; then
    if [ -d /run/systemd/system ]; then
      run systemctl enable --now docker
    else
      log "Starting dockerd (no init system runs it here)"
      nohup dockerd >/var/log/heimdall-dockerd.log 2>&1 &
      for i in $(seq 1 60); do
        docker info >/dev/null 2>&1 && break
        sleep 1
      done
    fi
  fi
}

# free_gb prints the free space, in whole GB, of the file system holding
# path (its nearest existing parent).
free_gb() {
  local p=$1
  while [ ! -e "$p" ] && [ "$p" != "/" ]; do
    p=$(dirname "$p")
  done
  df -Pk "$p" 2>/dev/null | awk 'NR == 2 {print int($4 / 1048576)}'
}

# check_disk refuses a box without room for the project, the Go build and
# the agent images: --min-disk-gb free under the install root and under
# Docker's data root.
check_disk() {
  local root docker_root free where
  root=$PREFIX
  docker_root=$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)
  [ -n "$docker_root" ] || docker_root=/var/lib/docker
  for where in "$root" "$docker_root"; do
    free=$(free_gb "$where")
    if [ -z "$free" ]; then
      warn "cannot read the free disk space at ${where}"
      continue
    fi
    if [ "$free" -lt "$MIN_DISK_GB" ]; then
      if is_dry || [ "$FORCE" -eq 1 ]; then
        warn "only ${free} GB free at ${where}; the images need ${MIN_DISK_GB} GB"
      else
        die "only ${free} GB free at ${where}; the task and agent images need ${MIN_DISK_GB} GB (free some space, or pass --min-disk-gb N)"
      fi
    else
      log "Disk: ${free} GB free at ${where} (need ${MIN_DISK_GB} GB)"
    fi
  done
}

# run_foreground runs the runner as the last step (--foreground): it is the
# box's main process, so the platform restarts or stops the box with it.
run_foreground() {
  local -a args
  args=(runner start -server "$SERVER" -token-file "$TOKEN_PATH" -pool "$POOL" -slots "$SLOTS" -name "$RUNNER_NAME" -config "$CONFIG_PATH")
  if [ -n "$LABELS" ]; then
    args+=(-labels "$LABELS")
  fi
  if [ -n "$SPEND_CAP" ]; then
    args+=(-spend-cap-usd "$SPEND_CAP")
  fi
  if [ "$DEBUG" -eq 1 ]; then
    args+=(-debug)
  fi
  log "Starting the runner in the foreground (${RUNNER_NAME}, pool ${POOL})"
  if is_dry; then
    run_in "$BENCH_DIR" runuser -u "$RUN_USER" -- env HOME="$HOME_DIR" "$BIN_PATH" "${args[@]}"
    return 0
  fi
  cleanup
  cd "$BENCH_DIR"
  exec runuser -u "$RUN_USER" -- env HOME="$HOME_DIR" "$BIN_PATH" "${args[@]}"
}

# ---------------------------------------------------------------- steps

lock_install() {
  if is_dry; then
    return 0
  fi
  exec 9>"$LOCK_PATH"
  flock -n 9 || die "another run of this installer holds ${LOCK_PATH}"
  WORK_TMP=$(mktemp -d)
}

stop_running_service() {
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SERVICE" 2>/dev/null; then
    log "Upgrade: stopping ${SERVICE} while the runner is rebuilt"
    run systemctl stop "$SERVICE"
  fi
}

create_user() {
  local group
  log "System user ${RUN_USER}"
  if getent passwd "$RUN_USER" >/dev/null 2>&1; then
    note "user ${RUN_USER} exists"
  else
    run useradd --system --user-group --home-dir "$HOME_DIR" --no-create-home --shell /usr/sbin/nologin "$RUN_USER"
  fi
  run install -d -m 0755 -o root -g root "$PREFIX"
  if is_dry && ! getent passwd "$RUN_USER" >/dev/null 2>&1; then
    group=$RUN_USER
  else
    group=$(id -gn "$RUN_USER" 2>/dev/null || printf '%s' "$RUN_USER")
  fi
  RUN_GROUP=$group
  run install -d -m 0750 -o "$RUN_USER" -g "$RUN_GROUP" "$HOME_DIR"
  if ! getent group docker >/dev/null 2>&1; then
    if is_dry; then
      warn "group docker does not exist yet (Docker creates it); a real run stops here"
    else
      die "group docker does not exist; install Docker first"
    fi
  fi
  if id -nG "$RUN_USER" 2>/dev/null | tr ' ' '\n' | grep -qx docker; then
    note "${RUN_USER} is in group docker"
  else
    run usermod -aG docker "$RUN_USER"
  fi
}

checkout_source() {
  local commit
  log "Project ${REPO} at ${REF} into ${SRC_DIR}"
  if [ -d "${SRC_DIR}/.git" ]; then
    note "existing clone: fetching"
    as_user git -C "$SRC_DIR" remote set-url origin "$REPO"
  else
    if [ -e "$SRC_DIR" ] && [ -n "$(ls -A "$SRC_DIR" 2>/dev/null)" ]; then
      die "${SRC_DIR} exists, is not a git clone and is not empty; move it away first"
    fi
    run install -d -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "$SRC_DIR"
    as_user git clone --no-checkout -- "$REPO" "$SRC_DIR"
  fi
  as_user git -C "$SRC_DIR" fetch --force --tags --prune origin

  if is_dry; then
    printf '[dry-run] resolve %s to a commit: branch origin/%s, else tag %s, else commit %s\n' "$REF" "$REF" "$REF" "$REF"
    as_user git -C "$SRC_DIR" -c advice.detachedHead=false checkout --force --detach "<commit of ${REF}>"
    return 0
  fi

  commit=""
  if commit=$(as_user_out git -C "$SRC_DIR" rev-parse --verify --quiet "refs/remotes/origin/${REF}^{commit}"); then
    :
  elif commit=$(as_user_out git -C "$SRC_DIR" rev-parse --verify --quiet "refs/tags/${REF}^{commit}"); then
    :
  elif [[ $REF =~ ^[0-9a-f]{7,40}$ ]] &&
    commit=$(as_user_out git -C "$SRC_DIR" rev-parse --verify --quiet "${REF}^{commit}"); then
    :
  else
    as_user git -C "$SRC_DIR" fetch --force origin "$REF" ||
      die "ref ${REF} is not a branch, tag or commit of ${REPO}"
    commit=$(as_user_out git -C "$SRC_DIR" rev-parse --verify --quiet "FETCH_HEAD^{commit}") ||
      die "ref ${REF} does not name a commit"
  fi
  as_user git -C "$SRC_DIR" -c advice.detachedHead=false checkout --force --detach "$commit"
  note "checked out ${commit}"
  [ -d "${BENCH_DIR}/cmd/bifrost-bench" ] ||
    die "${REPO} at ${REF} has no bench/cmd/bifrost-bench; pass --repo with the repository that holds bench/"
}

# server_curl downloads from the Heimdall server: https only, except the
# plain http that validate_args allows for a loopback tunnel.
server_curl() {
  local proto='=https'
  case $SERVER in http://*) proto='=https,http' ;; esac
  curl --proto "$proto" --tlsv1.2 -fsSL --retry 3 "$@"
}

# server_download fetches <server>/downloads/<name> to path and checks it
# against <name>.sha256 from the same place. The server's .sha256 file holds
# the hex digest, optionally followed by a file name. Every failure says
# which download is missing and how to install without it.
server_download() {
  local name=$1 path=$2 url sha
  url="${SERVER}/downloads/${name}"
  if ! server_curl -o "$path" "$url"; then
    die "the server has no download ${url} (commit ${REF}). Ask its operator to publish the downloads for the commit it runs, or install from git instead: --repo <git URL that holds bench/> --ref ${REF}"
  fi
  sha=$(server_curl "${url}.sha256" | awk 'NR == 1 { print tolower($1) }') ||
    die "could not download ${url}.sha256; not installing an unchecked file"
  [[ $sha =~ $SHA_RE ]] || die "${url}.sha256 does not hold a sha256 digest; not installing ${name}"
  printf '%s  %s\n' "$sha" "$path" | sha256sum -c --quiet - ||
    die "sha256 of ${name} does not match ${url}.sha256; not installing it"
}

# bundle_prefix prints the folder inside the bundle that holds the bench
# tree: "bench/" when the bundle has a top-level bench folder, else "". It
# refuses absolute names and names with a .. part.
bundle_prefix() {
  local tgz=$1 names
  names=$(tar -tzf "$tgz") || die "the project bundle is not a gzip tarball"
  if printf '%s\n' "$names" | grep -Eq '^/|(^|/)\.\.(/|$)'; then
    die "the project bundle holds an absolute path or a .. path; not installing it"
  fi
  if printf '%s\n' "$names" | grep -Eqx '(\./)?bench/deploy/bench\.yaml'; then
    printf 'bench/'
  elif printf '%s\n' "$names" | grep -Eqx '(\./)?deploy/bench\.yaml'; then
    printf ''
  else
    die "the project bundle has no deploy/bench.yaml (neither at its top nor under bench/); not installing it"
  fi
}

# Files of an installed project that belong to this box, not to the bundle:
# provider keys and tokens, the binary, sealed runs, releases and agent
# version snapshots. An upgrade carries them into the new tree.
readonly KEEP_PATHS=(deploy/.env bin runs releases .heimdall)

# fetch_bundle installs the project from the server's bundle of commit REF:
# downloaded and checked into a temporary folder, extracted as the runner
# user next to the source folder, the box's own files carried over, then
# swapped in with two renames. A bundle already installed for REF is kept.
fetch_bundle() {
  local tgz stage old prefix p marker="${SRC_DIR}/.heimdall-bundle"
  log "Project bundle ${SERVER}/downloads/bench-${REF}.tar.gz into ${SRC_DIR}"
  if is_dry; then
    print_cmd curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "<tmp>/bench-${REF}.tar.gz" "${SERVER}/downloads/bench-${REF}.tar.gz"
    print_cmd curl --proto '=https' --tlsv1.2 -fsSL --retry 3 "${SERVER}/downloads/bench-${REF}.tar.gz.sha256"
    print_cmd sha256sum -c "<<< <published sha256>  <tmp>/bench-${REF}.tar.gz"
    print_cmd tar -C "${SRC_DIR}.incoming/bench" --no-same-owner --no-same-permissions -xzf "<tmp>/bench-${REF}.tar.gz"
    note "keeping this box's ${KEEP_PATHS[*]} (under bench/) across the swap"
    print_cmd mv "${SRC_DIR}.incoming" "$SRC_DIR"
    return 0
  fi
  if [ -f "$marker" ] && [ "$(cat "$marker" 2>/dev/null)" = "$REF" ] && [ -f "${BENCH_DIR}/deploy/bench.yaml" ]; then
    note "bundle for ${REF} already installed"
    return 0
  fi
  tgz="${WORK_TMP}/bench-${REF}.tar.gz"
  server_download "bench-${REF}.tar.gz" "$tgz"
  prefix=$(bundle_prefix "$tgz")
  chmod 0644 "$tgz"
  stage="${SRC_DIR}.incoming"
  old="${SRC_DIR}.old"
  rm -rf "$stage" "$old"
  install -d -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "$stage" "${stage}/bench"
  # A bundle with a top-level bench/ folder extracts into the source folder,
  # one without into its bench/ folder. Extracted as the runner user, so
  # nothing in it can land outside what that user may write.
  if [ -n "$prefix" ]; then
    as_user tar -C "$stage" --no-same-owner --no-same-permissions -xzf "$tgz" || die "could not extract the project bundle"
  else
    as_user tar -C "${stage}/bench" --no-same-owner --no-same-permissions -xzf "$tgz" || die "could not extract the project bundle"
  fi
  [ -f "${stage}/bench/deploy/bench.yaml" ] || die "the extracted project bundle has no deploy/bench.yaml"
  if [ -d "$SRC_DIR" ]; then
    for p in "${KEEP_PATHS[@]}"; do
      if [ -e "${BENCH_DIR}/${p}" ] || [ -L "${BENCH_DIR}/${p}" ]; then
        rm -rf "${stage}/bench/${p}"
        install -d -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "$(dirname "${stage}/bench/${p}")"
        mv "${BENCH_DIR}/${p}" "${stage}/bench/${p}"
      fi
    done
    mv "$SRC_DIR" "$old"
  fi
  mv "$stage" "$SRC_DIR"
  rm -rf "$old"
  printf '%s\n' "$REF" >"$marker"
  chown "$RUN_USER:$RUN_GROUP" "$marker"
  note "installed the bundle of ${REF}"
}

# fetch_server_binary installs <server>/downloads/bifrost-bench-linux-<arch>,
# checked against its .sha256.
fetch_server_binary() {
  local name="bifrost-bench-linux-${ARCH}" tmp
  log "Downloading the runner binary: ${SERVER}/downloads/${name}"
  if is_dry; then
    print_cmd curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "<tmp>/${name}" "${SERVER}/downloads/${name}"
    print_cmd curl --proto '=https' --tlsv1.2 -fsSL --retry 3 "${SERVER}/downloads/${name}.sha256"
    print_cmd sha256sum -c "<<< <published sha256>  <tmp>/${name}"
    print_cmd install -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "<tmp>/${name}" "${BIN_PATH}.new"
    print_cmd mv -f "${BIN_PATH}.new" "$BIN_PATH"
    return 0
  fi
  tmp="${WORK_TMP}/${name}"
  server_download "$name" "$tmp"
  as_user install -d -m 0755 "${BENCH_DIR}/bin"
  install -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "$tmp" "${BIN_PATH}.new"
  mv -f "${BIN_PATH}.new" "$BIN_PATH"
}

# get_source installs the project at REF: the server's bundle, or a git
# clone in git mode.
get_source() {
  if [ "$SOURCE" = "git" ]; then
    checkout_source
  else
    if [ -d "${SRC_DIR}/.git" ]; then
      note "${SRC_DIR} is a git clone from an earlier install; the server's bundle replaces it (pass --repo to keep cloning)"
    fi
    fetch_bundle
  fi
}

# go_ok reports whether $1 is a Go toolchain of at least version $2.
go_ok() {
  local v
  [ -x "$1" ] || return 1
  # The build runs as the runner user, so a Go under /root or a private home
  # folder does not count.
  if ! is_dry && ! runuser -u "$RUN_USER" -- test -x "$1"; then
    return 1
  fi
  v=$(cd / && GOTOOLCHAIN=local "$1" env GOVERSION 2>/dev/null) || return 1
  v=${v%% *}
  v=${v#go}
  version_ge "$v" "$2"
}

ensure_go() {
  local need want c url sha file need_from="bench/go.mod"
  need=""
  if [ -r "${BENCH_DIR}/go.mod" ]; then
    need=$(sed -n 's/^go \([0-9][0-9.]*\)[[:space:]]*$/\1/p' "${BENCH_DIR}/go.mod" | head -n 1)
  fi
  if [ -z "$need" ]; then
    need=$GO_VERSION
    need_from="the pinned version, bench/go.mod not checked out yet"
  fi
  GO_BIN=""
  for c in "${GO_ROOT}/bin/go" "$(command -v go 2>/dev/null || true)" /usr/local/go/bin/go; do
    if [ -n "$c" ] && go_ok "$c" "$need"; then
      GO_BIN=$c
      log "Go toolchain: ${GO_BIN} (at least ${need}: ${need_from})"
      return 0
    fi
  done

  want=$GO_VERSION
  if ! version_ge "$want" "$need"; then
    want=$need
    case $want in
      *.*.*) ;;
      *) want="${want}.0" ;;
    esac
  fi
  log "Go toolchain: installing go${want} into ${GO_ROOT} (none at least ${need} found)"
  file="go${want}.linux-${ARCH}.tar.gz"
  url="https://dl.google.com/go/${file}"
  sha=""
  if [ "$want" = "$GO_VERSION_PINNED" ]; then
    if [ "$ARCH" = "amd64" ]; then sha=$GO_SHA256_AMD64; else sha=$GO_SHA256_ARM64; fi
  fi
  if is_dry; then
    print_cmd curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "<tmp>/${file}" "$url"
    if [ -n "$sha" ]; then
      print_cmd sha256sum -c "<<< ${sha}  <tmp>/${file}"
    else
      print_cmd curl --proto '=https' --tlsv1.2 -fsSL "${url}.sha256"
      print_cmd sha256sum -c "<<< <published sha256>  <tmp>/${file}"
    fi
    print_cmd rm -rf "$GO_ROOT"
    print_cmd tar -C "$PREFIX" --no-same-owner -xzf "<tmp>/${file}"
    GO_BIN="${GO_ROOT}/bin/go"
    return 0
  fi
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "${WORK_TMP}/${file}" "$url"
  if [ -z "$sha" ]; then
    sha=$(curl --proto '=https' --tlsv1.2 -fsSL --retry 3 "${url}.sha256" | tr -d '[:space:]')
    [[ $sha =~ $SHA_RE ]] || die "could not read the published sha256 of ${file}"
  fi
  printf '%s  %s\n' "$sha" "${WORK_TMP}/${file}" | sha256sum -c --quiet - ||
    die "sha256 of ${file} does not match; not installing it"
  rm -rf "$GO_ROOT"
  tar -C "$PREFIX" --no-same-owner -xzf "${WORK_TMP}/${file}"
  GO_BIN="${GO_ROOT}/bin/go"
  go_ok "$GO_BIN" "$need" || die "the installed Go at ${GO_BIN} does not run"
}

build_binary() {
  local godir
  ensure_go
  godir=$(dirname "$GO_BIN")
  log "Building bin/bifrost-bench from source (CGO_ENABLED=0 GOWORK=off)"
  as_user install -d -m 0755 "${BENCH_DIR}/bin"
  as_user env \
    PATH="${godir}:/usr/local/bin:/usr/bin:/bin" \
    GOPATH="${HOME_DIR}/go" \
    GOCACHE="${HOME_DIR}/.cache/go-build" \
    GOTOOLCHAIN=local GOWORK=off CGO_ENABLED=0 GOFLAGS= \
    "$GO_BIN" -C "$BENCH_DIR" build -o bin/bifrost-bench.new ./cmd/bifrost-bench
  # A rename, so a binary that is running is never overwritten in place.
  as_user mv -f "${BIN_PATH}.new" "$BIN_PATH"
}

fetch_release() {
  local url member members dir tgz
  url=${RELEASE_URL//\{arch\}/$ARCH}
  log "Downloading the prebuilt runner: ${url}"
  if is_dry; then
    print_cmd curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "<tmp>/runner.tar.gz" "$url"
    if [ -n "$RELEASE_SHA256" ]; then
      print_cmd sha256sum -c "<<< ${RELEASE_SHA256}  <tmp>/runner.tar.gz"
    else
      warn "no --sha256: the tarball is checked by TLS only"
    fi
    print_cmd tar -C "<tmp>/release" --no-same-owner --no-same-permissions -xzf "<tmp>/runner.tar.gz" bin/bifrost-bench
    print_cmd install -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "<tmp>/release/bin/bifrost-bench" "${BIN_PATH}.new"
    print_cmd mv -f "${BIN_PATH}.new" "$BIN_PATH"
    return 0
  fi
  tgz="${WORK_TMP}/runner.tar.gz"
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "$tgz" "$url"
  if [ -n "$RELEASE_SHA256" ]; then
    printf '%s  %s\n' "$RELEASE_SHA256" "$tgz" | sha256sum -c --quiet - ||
      die "sha256 of the release tarball does not match --sha256; not installing it"
  else
    warn "no --sha256: the tarball is checked by TLS only"
  fi
  members=$(tar -tzf "$tgz") || die "the release download is not a gzip tarball"
  member=$(printf '%s\n' "$members" | grep -E '^(\./)?bin/bifrost-bench$' | head -n 1 || true)
  [ -n "$member" ] || die "the release tarball has no bin/bifrost-bench"
  dir="${WORK_TMP}/release"
  mkdir -p "$dir"
  tar -C "$dir" --no-same-owner --no-same-permissions -xzf "$tgz" -- "$member"
  [ -f "${dir}/bin/bifrost-bench" ] && [ ! -L "${dir}/bin/bifrost-bench" ] ||
    die "bin/bifrost-bench in the release tarball is not a regular file"
  as_user install -d -m 0755 "${BENCH_DIR}/bin"
  install -m 0755 -o "$RUN_USER" -g "$RUN_GROUP" "${dir}/bin/bifrost-bench" "${BIN_PATH}.new"
  mv -f "${BIN_PATH}.new" "$BIN_PATH"
}

get_binary() {
  if [ -n "$RELEASE_URL" ]; then
    fetch_release
  elif [ "$SOURCE" = "server" ]; then
    fetch_server_binary
  else
    build_binary
  fi
  if is_dry; then
    as_user "$BIN_PATH" version
  else
    BENCH_VERSION=$(as_user_out "$BIN_PATH" version) || die "${BIN_PATH} does not run on this box"
    note "bifrost-bench version ${BENCH_VERSION}"
  fi
}

# env_keys lists the KEY of every KEY=value line of a dotenv file.
env_keys() {
  sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' "$1" | sort -u
}

ensure_env() {
  local t1 t2 t3 tmp missing
  log "Gateway settings ${ENV_FILE}"
  if [ -L "$ENV_FILE" ]; then
    die "${ENV_FILE} is a symlink; replace it with a regular file"
  fi
  if [ -f "$ENV_FILE" ]; then
    note "keeping the existing deploy/.env (provider keys and tokens untouched)"
    if ! is_dry && [ -r "$ENV_EXAMPLE" ]; then
      missing=$(comm -23 <(env_keys "$ENV_EXAMPLE") <(env_keys "$ENV_FILE") | tr '\n' ' ')
      if [ -n "${missing// /}" ]; then
        warn "deploy/.env lacks keys that deploy/.env.example now has: ${missing}; add them by hand"
      fi
    fi
  else
    if is_dry; then
      # The same edit as `make env` in bench/Makefile; the values are not shown.
      printf '[dry-run] sed -e %s -e %s -e %s %s > %s\n' \
        "'s#^BIFROST_SETUP_TOKEN=.*#BIFROST_SETUP_TOKEN=<openssl rand -hex 24>#'" \
        "'s#^BIFROST_PG_PASSWORD=.*#BIFROST_PG_PASSWORD=<openssl rand -hex 24>#'" \
        "'s#^BENCH_FAULTPROXY_TOKEN=.*#BENCH_FAULTPROXY_TOKEN=<openssl rand -hex 24>#'" \
        "$ENV_EXAMPLE" "$ENV_FILE"
    else
      [ -r "$ENV_EXAMPLE" ] || die "${ENV_EXAMPLE} is missing at ${REF}"
      t1=$(openssl rand -hex 24)
      t2=$(openssl rand -hex 24)
      t3=$(openssl rand -hex 24)
      tmp=$(mktemp "${ENV_FILE}.XXXXXX")
      sed -e "s#^BIFROST_SETUP_TOKEN=.*#BIFROST_SETUP_TOKEN=${t1}#" \
        -e "s#^BIFROST_PG_PASSWORD=.*#BIFROST_PG_PASSWORD=${t2}#" \
        -e "s#^BENCH_FAULTPROXY_TOKEN=.*#BENCH_FAULTPROXY_TOKEN=${t3}#" \
        "$ENV_EXAMPLE" >"$tmp"
      mv -f "$tmp" "$ENV_FILE"
      note "created deploy/.env with random gateway tokens (scripted model until provider keys are added)"
    fi
  fi
  run chown "${RUN_USER}:${RUN_GROUP}" "$ENV_FILE"
  run chmod 0600 "$ENV_FILE"
}

write_token() {
  local tmp
  log "Runner token ${TOKEN_PATH}"
  run install -d -m 0700 -o "$RUN_USER" -g "$RUN_GROUP" "$ETC_DIR"
  if [ -z "$TOKEN" ]; then
    if is_dry; then
      note "no --token-file or --token-stdin: a real run keeps the installed token (and stops if there is none)"
    elif [ -L "$TOKEN_PATH" ]; then
      die "${TOKEN_PATH} is a symlink; pass the token again with --token-file or --token-stdin"
    elif [ -s "$TOKEN_PATH" ]; then
      note "keeping the installed token"
      chown "${RUN_USER}:${RUN_GROUP}" "$TOKEN_PATH"
      chmod 0600 "$TOKEN_PATH"
    else
      die "no runner token installed yet; pass --token-file PATH or --token-stdin"
    fi
    return 0
  fi
  if is_dry; then
    printf '[dry-run] write %s (mode 0600, owner %s:%s): <runner token, not shown>\n' "$TOKEN_PATH" "$RUN_USER" "$RUN_GROUP"
    return 0
  fi
  tmp=$(mktemp "${TOKEN_PATH}.XXXXXX")
  # printf is a shell builtin: the token never appears in a process's argv.
  printf '%s' "$TOKEN" >"$tmp"
  chown "${RUN_USER}:${RUN_GROUP}" "$tmp"
  chmod 0600 "$tmp"
  mv -f "$tmp" "$TOKEN_PATH"
  if [ -n "$TOKEN_FILE" ]; then
    note "installed; you can delete ${TOKEN_FILE} now"
  fi
}

write_unit() {
  local -a exec_args
  local exec_line="" a
  exec_args=("$BIN_PATH" runner start
    -server "$SERVER"
    -token-file "$TOKEN_PATH"
    -pool "$POOL"
    -slots "$SLOTS"
    -name "$RUNNER_NAME"
    -config "$CONFIG_PATH")
  if [ -n "$LABELS" ]; then
    exec_args+=(-labels "$LABELS")
  fi
  if [ -n "$SPEND_CAP" ]; then
    exec_args+=(-spend-cap-usd "$SPEND_CAP")
  fi
  if [ "$DEBUG" -eq 1 ]; then
    exec_args+=(-debug)
  fi
  # Every value was checked against a pattern without spaces, quotes, % or $,
  # so the words need no systemd quoting.
  for a in "${exec_args[@]}"; do
    exec_line+="${exec_line:+ }${a}"
  done
  log "systemd unit ${UNIT_PATH}"
  write_file "$UNIT_PATH" 0644 root:root <<EOF
# Written by bench/deploy/runner/install.sh; running it again rewrites this file.
# The runner token is read from ${TOKEN_PATH}, never from this file.
[Unit]
Description=Heimdall runner (bifrost-bench runner start)
Documentation=file://${BENCH_DIR}/deploy/runner/README.md
Wants=network-online.target docker.service
After=network-online.target docker.service
StartLimitIntervalSec=0

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_GROUP}
SupplementaryGroups=docker
WorkingDirectory=${BENCH_DIR}
EnvironmentFile=${ENV_FILE}
Environment=HOME=${HOME_DIR}
ExecStart=${exec_line}
Restart=always
RestartSec=10
TimeoutStopSec=120
# Hardening that keeps the Docker socket reachable. Bind mounts the runner
# asks for come from the project tree under ${PREFIX}, never from /tmp, so a
# private /tmp does not hide them from the Docker daemon.
NoNewPrivileges=true
ProtectSystem=full
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF
}

# image_builds prints "tag context" lines from the images target of the
# project's Makefile, or the built-in list when it cannot be read.
image_builds() {
  local mk="${BENCH_DIR}/Makefile" line found=0
  local re='^[[:space:]]*docker build -t ([A-Za-z0-9._/:-]+) ([A-Za-z0-9._/-]+)[[:space:]]*$'
  if [ -r "$mk" ]; then
    while IFS= read -r line; do
      if [[ $line =~ $re ]] && [[ ${BASH_REMATCH[2]} != *..* ]]; then
        printf '%s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
        found=1
      fi
    done < <(awk '/^images:/ {f = 1; next} f && /^\t/ {print; next} f {exit}' "$mk")
  fi
  if [ "$found" -eq 0 ]; then
    printf '%s\n' "${FALLBACK_IMAGES[@]}"
  fi
}

build_images() {
  local tag ctx
  if [ "$SKIP_IMAGES" -eq 1 ]; then
    log "Images: skipped (--skip-images)"
    return 0
  fi
  log "Images: task base, bench (fault proxy) and agent images"
  while read -r tag ctx; do
    if [ "$ctx" = "." ]; then
      run docker build -t "$tag" "$BENCH_DIR"
    else
      run docker build -t "$tag" "${BENCH_DIR}/${ctx}"
    fi
  done < <(image_builds)
}

start_stack() {
  if [ "$SKIP_STACK" -eq 1 ]; then
    log "Gateway stack: skipped (--skip-stack)"
    return 0
  fi
  log "Gateway stack: Bifrost, Postgres and the fault proxy (deploy/docker-compose.yml)"
  run_in "$BENCH_DIR" docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --wait
}

start_service() {
  log "Starting ${SERVICE}"
  run systemctl daemon-reload
  run systemctl enable "$SERVICE"
  run systemctl restart "$SERVICE"
}

check_status() {
  log "Runner status"
  if is_dry; then
    print_cmd sleep 5
    run_in "$BENCH_DIR" runuser -u "$RUN_USER" -- env HOME="$HOME_DIR" \
      "$BIN_PATH" runner status -server "$SERVER" -token-file "$TOKEN_PATH" -name "$RUNNER_NAME"
    return 0
  fi
  sleep 5
  if ! run_in "$BENCH_DIR" runuser -u "$RUN_USER" -- env HOME="$HOME_DIR" \
    "$BIN_PATH" runner status -server "$SERVER" -token-file "$TOKEN_PATH" -name "$RUNNER_NAME"; then
    warn "runner status failed; the service keeps retrying every 10 s. See: journalctl -u ${SERVICE} -n 100"
    STATUS_FAILED=1
  fi
}

next_steps() {
  local done_word="installed"
  if is_dry; then
    done_word="would be installed (dry run)"
  fi
  cat <<EOF

Heimdall runner ${RUNNER_NAME} ${done_word} (pool ${POOL}, ${SLOTS} slot(s), ref ${REF}, from ${SOURCE}).

Provider keys
  Real model calls need provider keys. They go ONLY in this file on this box:
    ${ENV_FILE}
  (ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY, ...). Never put them on
  the hosted server. The fault proxy container swaps them in; agents, Bifrost
  and the hosted server never see them. Empty keys mean the scripted model.
  After editing it:
    cd ${BENCH_DIR}
    sudo docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --wait
    sudo systemctl restart ${SERVICE}

Day to day
  sudo systemctl status ${SERVICE}
  sudo journalctl -u ${SERVICE} -f
  cd ${BENCH_DIR} && sudo -u ${RUN_USER} ${BIN_PATH} runner status -server ${SERVER} -token-file ${TOKEN_PATH} -name ${RUNNER_NAME}

Upgrade: run this installer again with the same flags. Downloaded from the
server, it installs the commit the server runs now; with --ref, pass the
server's new commit.
EOF
}

main() {
  parse_args "$@"
  validate_args
  read_token
  if is_dry; then
    log "Dry run: printing commands, changing nothing"
  fi
  check_os
  check_root
  detect_arch
  lock_install
  install_prereqs
  check_tools
  check_docker
  check_disk
  stop_running_service
  create_user
  get_source
  get_binary
  ensure_env
  write_token
  if [ "$FOREGROUND" -eq 1 ]; then
    build_images
    start_stack
    run_foreground
    return 0
  fi
  write_unit
  build_images
  start_stack
  start_service
  STATUS_FAILED=0
  check_status
  next_steps
  if is_dry; then
    log "Dry run complete: nothing was changed"
  fi
  if [ "$STATUS_FAILED" -eq 1 ]; then
    exit 2
  fi
}

main "$@"
