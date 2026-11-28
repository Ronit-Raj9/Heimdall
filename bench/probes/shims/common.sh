# Shared code for the Bifrost Bench tool shims, sourced by the pytest, py.test,
# python and python3 wrappers. POSIX sh only: agent images ship dash or
# busybox ash, not always bash.
#
# Aux volume layout (mounted at /opt/bench in the agent container):
#   shim/   these scripts and config, written by the bench, read-only to the agent
#   log/    shim.jsonl and the call counter, written by the shims as the agent user
#
# The shims find their root from $0, so the same files also run from a temp
# directory in unit tests. They never live in the workspace, so they are never
# part of the exported diff.
#
# Behaviour comes from shim/config (key=value lines, written at arm time):
#   mode       crash | empty
#   calls      how many invocations are broken
#   exit_code  exit status of a crash
#   nonce      the receipt nonce, printed inside the broken output
#   output     the text to print, with \\ for a backslash and \n for a newline
# Without a readable config every call passes straight through.

bench_shim_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
bench_root=$(dirname -- "$bench_shim_dir")
bench_log_dir=$bench_root/log
bench_config=$bench_shim_dir/config
bench_tool=pytest
bench_locked=

# bench_find_real NAME: set bench_real to the first executable NAME on PATH
# that is not in a shim directory. Empty PATH entries (the current directory)
# are skipped so a file the agent dropped in the workspace is never run.
bench_find_real() {
	bench_real=
	bench_old_ifs=$IFS
	IFS=:
	set -f
	for bench_d in $PATH; do
		case $bench_d in
		'' | /opt/bench/shim | /opt/bench/shim/ | "$bench_shim_dir") continue ;;
		*/shim | */shim/)
			if [ "$(CDPATH='' cd -- "$bench_d" 2>/dev/null && pwd -P)" = "$bench_shim_dir" ]; then
				continue
			fi
			;;
		esac
		if [ -f "$bench_d/$1" ] && [ -x "$bench_d/$1" ]; then
			bench_real=$bench_d/$1
			break
		fi
	done
	set +f
	IFS=$bench_old_ifs
	[ -n "$bench_real" ]
}

# bench_not_found NAME: behave like the shell when NAME is not installed.
bench_not_found() {
	printf 'sh: 1: %s: not found\n' "$1" >&2
	exit 127
}

# bench_read_config: load shim/config into bench_* variables. Values are read
# as data, never evaluated.
bench_read_config() {
	bench_mode=
	bench_calls=0
	bench_exit=1
	bench_nonce=
	bench_output=
	[ -r "$bench_config" ] || return 0
	while IFS= read -r bench_line || [ -n "$bench_line" ]; do
		case $bench_line in
		mode=*) bench_mode=${bench_line#mode=} ;;
		calls=*) bench_calls=${bench_line#calls=} ;;
		exit_code=*) bench_exit=${bench_line#exit_code=} ;;
		nonce=*) bench_nonce=${bench_line#nonce=} ;;
		output=*) bench_output=${bench_line#output=} ;;
		esac
	done <"$bench_config"
	case $bench_mode in crash | empty) ;; *) bench_mode= ;; esac
	case $bench_calls in '' | *[!0-9]*) bench_calls=0 ;; esac
	case $bench_exit in '' | *[!0-9]*) bench_exit=1 ;; esac
	case $bench_nonce in *[!A-Za-z0-9_]*) bench_nonce= ;; esac
}

# bench_lock: take the log directory lock (mkdir is atomic). Gives up after
# about ten seconds so a stale lock can never hang the agent's tool; a lock
# older than half that is assumed dead and broken once.
bench_lock() {
	[ -d "$bench_log_dir" ] && [ -w "$bench_log_dir" ] || return 1
	bench_tries=0
	while ! mkdir "$bench_log_dir/.lock" 2>/dev/null; do
		bench_tries=$((bench_tries + 1))
		[ "$bench_tries" -lt 200 ] || return 1
		[ "$bench_tries" -ne 100 ] || rmdir "$bench_log_dir/.lock" 2>/dev/null
		sleep 0.05 2>/dev/null || sleep 1
	done
	bench_locked=1
}

bench_unlock() {
	[ -z "$bench_locked" ] || rmdir "$bench_log_dir/.lock" 2>/dev/null
	bench_locked=
}

# bench_next_call: increment the shared counter and set bench_call. Fails when
# the counter cannot be stored, and the caller then passes through: a shim
# that cannot count must not break the tool forever.
bench_next_call() {
	bench_count_file=$bench_log_dir/$bench_tool.count
	bench_call=
	if [ -f "$bench_count_file" ]; then
		IFS= read -r bench_call <"$bench_count_file" || :
	fi
	case $bench_call in '' | *[!0-9]*) bench_call=0 ;; esac
	bench_call=$((bench_call + 1))
	{ printf '%s\n' "$bench_call" >"$bench_count_file"; } 2>/dev/null
}

# bench_take_slot: claim one of the broken-call slots 1..calls by an atomic
# mkdir of log/<tool>.broken.<n>. Exactly calls invocations win, however many
# start at once and even if the counter lock was lost or broken; a call that
# wins no slot (or cannot write the log folder) runs the real tool.
bench_take_slot() {
	[ -d "$bench_log_dir" ] && [ -w "$bench_log_dir" ] || return 1
	bench_slot=1
	while [ "$bench_slot" -le "$bench_calls" ]; do
		if mkdir "$bench_log_dir/$bench_tool.broken.$bench_slot" 2>/dev/null; then
			return 0
		fi
		bench_slot=$((bench_slot + 1))
	done
	return 1
}

# bench_log ENTRY ARGS...: append one JSON line to log/shim.jsonl. Control
# characters in argv become spaces; quotes and backslashes are escaped.
bench_log() {
	bench_argv=$1
	shift
	for bench_a in "$@"; do
		bench_argv="$bench_argv $bench_a"
	done
	bench_argv=$(printf '%s' "$bench_argv" | tr '\t\n\r' '   ' | tr -d '\001-\037' | sed 's/\\/\\\\/g; s/"/\\"/g')
	bench_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	{
		printf '{"tool":"%s","argv":"%s","call":%s,"action":"%s","nonce":"%s","at":"%s"}\n' \
			"$bench_tool" "$bench_argv" "$bench_call" "$bench_action" "$bench_nonce" "$bench_at" \
			>>"$bench_log_dir/shim.jsonl"
	} 2>/dev/null
}

# bench_intercept ENTRY REAL ARGS...: count and log this invocation, then
# either print the broken output and exit, or exec the real tool.
bench_intercept() {
	bench_entry=$1
	bench_exec=$2
	shift 2
	bench_read_config
	bench_action=pass
	bench_call=0
	if bench_lock; then
		bench_next_call
	fi
	if [ -n "$bench_mode" ] && bench_take_slot; then
		bench_action=$bench_mode
	fi
	bench_log "$bench_entry" "$@"
	bench_unlock
	case $bench_action in
	crash)
		printf '%b\n' "$bench_output" >&2
		exit "$bench_exit"
		;;
	empty)
		printf '%b\n' "$bench_output"
		exit 0
		;;
	esac
	exec "$bench_exec" "$@"
}

# bench_py_module ARGS...: set bench_module to the module a python command
# line runs with -m, or to empty. Handles clustered flags (-Im pytest,
# -mpytest) and the options that take a value (-W, -X,
# --check-hash-based-pycs).
bench_py_module() {
	bench_module=
	while [ $# -gt 0 ]; do
		bench_arg=$1
		shift
		case $bench_arg in
		--) return 0 ;;
		--check-hash-based-pycs)
			[ $# -eq 0 ] || shift
			continue
			;;
		--*) return 0 ;;
		-) return 0 ;;
		-*) ;;
		*) return 0 ;;
		esac
		bench_rest=${bench_arg#-}
		while [ -n "$bench_rest" ]; do
			bench_c=${bench_rest%"${bench_rest#?}"}
			bench_rest=${bench_rest#?}
			case $bench_c in
			m)
				if [ -n "$bench_rest" ]; then
					bench_module=$bench_rest
				elif [ $# -gt 0 ]; then
					bench_module=$1
				fi
				return 0
				;;
			c) return 0 ;;
			W | X)
				[ -n "$bench_rest" ] || { [ $# -eq 0 ] || shift; }
				bench_rest=
				;;
			esac
		done
	done
}
