#!/usr/bin/env bash
# Orbit vs. Astro SSR benchmark. See bench/README.md.
#
#   bench/run.sh            full run (5 s warm-up + 20 s per run, about 5 minutes)
#   QUICK=1 bench/run.sh    smoke run (1 s + 3 s per run)
#
# Settings (environment):
#   CONCURRENCY=64  WARMUP=5s  DURATION=20s
#   SERVERS="orbit-1 orbit-all astro-1 astro"
#   SCENARIOS="cards fetch fetch-deferred fetch-deferred-page fetch-cached"
#   SERVER_CPUS=0-3,8-11  ONE_CPU=0  LOAD_CPUS=4-7,12-15   (taskset lists)
#   SKIP_BUILD=1  (reuse the existing builds)
set -euo pipefail

BENCH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$BENCH")"
ORBIT_SITE="$BENCH/orbit-site"
ASTRO_SITE="$BENCH/astro-site"

QUICK="${QUICK:-0}"
if [[ "$QUICK" == 1 ]]; then
	WARMUP="${WARMUP:-1s}"
	DURATION="${DURATION:-3s}"
else
	WARMUP="${WARMUP:-5s}"
	DURATION="${DURATION:-20s}"
fi
CONCURRENCY="${CONCURRENCY:-64}"
SERVERS="${SERVERS:-orbit-1 orbit-all astro-1 astro}"
# fetch-deferred-page requests only /fetch-deferred (its island is never
# fetched), for the page's TTFB on its own.
SCENARIOS="${SCENARIOS:-cards fetch fetch-deferred fetch-deferred-page fetch-cached}"
# Physical-core split. On this kind of CPU (Linux numbering), CPU n and CPU n+8
# are the two SMT threads of one core, so the server gets cores 0-3 (both
# threads) and the load generator cores 4-7: they never share a core.
SERVER_CPUS="${SERVER_CPUS:-0-3,8-11}"
ONE_CPU="${ONE_CPU:-0}"
LOAD_CPUS="${LOAD_CPUS:-4-7,12-15}"
ORBIT_PORT=4400
ASTRO_PORT=4401

RESULTS="$BENCH/results"
STAMP="$(date +%Y-%m-%d-%H%M%S)"
WORK="$(mktemp -d)"
RUNS="$WORK/runs.jsonl"
LOAD="$WORK/load"
SERVER_PID=""

log() { printf '\033[1m==> %s\033[0m\n' "$*" >&2; }

cleanup() {
	if [[ -n "$SERVER_PID" ]]; then kill "$SERVER_PID" 2>/dev/null || true; fi
	jobs -p | xargs -r kill 2>/dev/null || true
	rm -rf "$WORK"
}
trap cleanup EXIT

PIN=1
if ! command -v taskset >/dev/null; then
	log "taskset not found: running without CPU pinning (results are less reliable)"
	PIN=0
fi
pin() { # pin CPUS CMD...
	local cpus="$1"
	shift
	if [[ "$PIN" == 1 ]]; then taskset -c "$cpus" "$@"; else "$@"; fi
}

# --- build -------------------------------------------------------------------
log "building the load generator"
(cd "$BENCH/load" && go build -o "$LOAD" .)
if [[ "${SKIP_BUILD:-0}" != 1 ]]; then
	log "building bench/orbit-site (go tool orbit build)"
	(cd "$ORBIT_SITE" && go tool orbit build)
	log "building bench/astro-site (npm ci && npm run build)"
	(cd "$ASTRO_SITE" && npm ci --no-audit --no-fund --loglevel=error && ASTRO_TELEMETRY_DISABLED=1 npm run build --silent >/dev/null)
fi

# --- servers -----------------------------------------------------------------
wait_ready() { # URL
	for _ in $(seq 100); do
		if curl -fs -o /dev/null "$1"; then return 0; fi
		if ! kill -0 "$SERVER_PID" 2>/dev/null; then
			log "server exited"
			return 1
		fi
		sleep 0.1
	done
	log "server not ready: $1"
	return 1
}

port_of() { if [[ "$1" == astro* ]]; then echo "$ASTRO_PORT"; else echo "$ORBIT_PORT"; fi; }

# ensure_cached SERVER URL: stores the page in the route cache (the first
# request is a MISS) and fails unless the next response is a HIT, so a cached
# run measures hits only.
ensure_cached() {
	local header=X-Orbit-Cache got
	if [[ "$1" == astro* ]]; then header=X-Astro-Cache; fi
	curl -fs -o /dev/null "$2"
	got="$(curl -fs -o /dev/null -D - "$2" | tr -d '\r' | awk -v h="$header" 'tolower($1) == tolower(h ":") {print $2}')"
	if [[ "$got" != HIT ]]; then
		log "$1: $2 is not served from the route cache ($header: ${got:-missing}, want HIT)"
		exit 1
	fi
}

pinner() { # CPUS: prints the exec prefix (taskset execs the server, so $! is its PID)
	if [[ "$PIN" == 1 ]]; then echo "taskset -c $1"; else echo env; fi
}

start_server() { # NAME; sets SERVER_PID
	local one all
	read -ra one <<<"$(pinner "$ONE_CPU")"
	read -ra all <<<"$(pinner "$SERVER_CPUS")"
	case "$1" in
	orbit-1)
		(cd "$ORBIT_SITE" && GOMAXPROCS=1 PORT="$ORBIT_PORT" exec "${one[@]}" ./dist/server) &
		;;
	orbit-all)
		(cd "$ORBIT_SITE" && PORT="$ORBIT_PORT" exec "${all[@]}" ./dist/server) &
		;;
	astro)
		(cd "$ASTRO_SITE" && NODE_ENV=production HOST=127.0.0.1 PORT="$ASTRO_PORT" exec "${all[@]}" node ./dist/server/entry.mjs) >/dev/null &
		;;
	astro-1)
		# The same single process, with all its threads (GC, libuv) on one CPU,
		# like orbit-1.
		(cd "$ASTRO_SITE" && NODE_ENV=production HOST=127.0.0.1 PORT="$ASTRO_PORT" exec "${one[@]}" node ./dist/server/entry.mjs) >/dev/null &
		;;
	*)
		log "unknown server $1"
		exit 1
		;;
	esac
	SERVER_PID=$!
	wait_ready "http://127.0.0.1:$(port_of "$1")/cards"
}

stop_server() {
	if [[ -n "$SERVER_PID" ]]; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
		SERVER_PID=""
	fi
}

# --- equivalence check ---------------------------------------------------------
log "checking that both sites serve the same content"
start_server orbit-all
ORBIT_PID="$SERVER_PID"
SERVER_PID=""
start_server astro
ASTRO_PID="$SERVER_PID"
CHECK="$WORK/check.txt"
{
	"$LOAD" check -a "http://127.0.0.1:$ORBIT_PORT/cards" -b "http://127.0.0.1:$ASTRO_PORT/cards"
	"$LOAD" check -a "http://127.0.0.1:$ORBIT_PORT/fetch" -b "http://127.0.0.1:$ASTRO_PORT/fetch"
	"$LOAD" check -island -a "http://127.0.0.1:$ORBIT_PORT/fetch-deferred" -b "http://127.0.0.1:$ASTRO_PORT/fetch-deferred"
	"$LOAD" check -a "http://127.0.0.1:$ORBIT_PORT/fetch-cached" -b "http://127.0.0.1:$ASTRO_PORT/fetch-cached"
} | tee "$CHECK"
# The check stored /fetch-cached on both servers: its next response is a hit.
ensure_cached orbit-all "http://127.0.0.1:$ORBIT_PORT/fetch-cached"
ensure_cached astro "http://127.0.0.1:$ASTRO_PORT/fetch-cached"
stop_server
SERVER_PID="$ORBIT_PID"
stop_server

# --- load ----------------------------------------------------------------------
: >"$RUNS"
for server in $SERVERS; do
	for scenario in $SCENARIOS; do
		# A fresh process per scenario, so each RSS figure is that scenario's own.
		start_server "$server"
		island=()
		if [[ "$scenario" == fetch-deferred ]]; then island=(-island); fi
		if [[ "$scenario" == fetch-cached ]]; then ensure_cached "$server" "http://127.0.0.1:$(port_of "$server")/fetch-cached"; fi
		pin "$LOAD_CPUS" "$LOAD" run -url "http://127.0.0.1:$(port_of "$server")/${scenario%-page}" "${island[@]}" \
			-c "$CONCURRENCY" -warmup "$WARMUP" -d "$DURATION" -pid "$SERVER_PID" \
			-server "$server" -scenario "$scenario" >>"$RUNS"
		stop_server
	done
done

# --- report --------------------------------------------------------------------
mkdir -p "$RESULTS"
orbit_commit="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
if [[ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]]; then orbit_commit="$orbit_commit (dirty)"; fi
astro_v="$(node -p "require('$ASTRO_SITE/node_modules/astro/package.json').version")"
node_adapter_v="$(node -p "require('$ASTRO_SITE/node_modules/@astrojs/node/package.json').version")"
mem_gb="$(awk '/MemTotal/ {printf "%.0f GB", $2/1048576}' /proc/meminfo)"
check_summary="$(sed 's/http:\/\/127.0.0.1:[0-9]*//g; s/?[^ ]*//g' "$CHECK" | tr '\n' ';')"
pinning="server $SERVER_CPUS (orbit-1: CPU $ONE_CPU, GOMAXPROCS=1; astro-1: CPU $ONE_CPU), load generator $LOAD_CPUS"
if [[ "$PIN" == 0 ]]; then pinning="none"; fi

"$LOAD" report -in "$RUNS" -md "$RESULTS/$STAMP.md" -json "$RESULTS/$STAMP.json" \
	-m "date=$(date -Iseconds)" \
	-m "cpu=$(lscpu | sed -n 's/^Model name: *//p' | head -1)" \
	-m "cpus=$(nproc) logical" \
	-m "ram=$mem_gb" \
	-m "kernel=$(uname -sr)" \
	-m "go=$(go version | awk '{print $3}')" \
	-m "node=$(node --version)" \
	-m "orbit commit=$orbit_commit" \
	-m "astro=$astro_v, @astrojs/node $node_adapter_v (standalone, single process)" \
	-m "load=closed loop, $CONCURRENCY keep-alive connections, $WARMUP warm-up + $DURATION measured per run, fresh server process per run" \
	-m "pinning=$pinning" \
	-m "content check=$check_summary" \
	-m "quick=$QUICK"

log "results: $RESULTS/$STAMP.md"
cat "$RESULTS/$STAMP.md"
