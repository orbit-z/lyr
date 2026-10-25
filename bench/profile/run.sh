#!/usr/bin/env bash
# CPU profiles of Orbit and Astro serving /cards under load. See
# bench/profile/README.md.
#
#   bench/profile/run.sh             both servers, 5 s warm-up + 20 s profiled
#   QUICK=1 bench/profile/run.sh     1 s + 5 s
#
# Settings (environment):
#   PAGE=cards  CONCURRENCY=64  WARMUP=5s  DURATION=20s (whole seconds)
#   SERVERS="astro orbit"
#   ASTRO_CPUS=0-3,8-11  ORBIT_CPUS=0  LOAD_CPUS=4-7,12-15   (taskset lists)
#   ORBIT_GOMAXPROCS=1
#   SKIP_BUILD=1  (reuse the existing site builds)
#
# Writes bench/results/profile-<date>/: astro.cpuprofile (+ .json stats),
# astro-promises.json, orbit.pprof, orbit-server (the binary the profile
# belongs to), load-*.json, and summary.md with every table.
set -euo pipefail

PROF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH="$(dirname "$PROF")"
ORBIT_SITE="$BENCH/orbit-site"
ASTRO_SITE="$BENCH/astro-site"

QUICK="${QUICK:-0}"
if [[ "$QUICK" == 1 ]]; then
	WARMUP="${WARMUP:-1s}"
	DURATION="${DURATION:-5s}"
else
	WARMUP="${WARMUP:-5s}"
	DURATION="${DURATION:-20s}"
fi
PAGE="${PAGE:-cards}"
CONCURRENCY="${CONCURRENCY:-64}"
SERVERS="${SERVERS:-astro orbit}"
# Same split as bench/run.sh: Astro gets the 8 server CPUs (as in the
# published "Astro" row), Orbit one CPU with GOMAXPROCS=1 ("orbit-1").
ASTRO_CPUS="${ASTRO_CPUS:-0-3,8-11}"
ORBIT_CPUS="${ORBIT_CPUS:-0}"
ORBIT_GOMAXPROCS="${ORBIT_GOMAXPROCS:-1}"
LOAD_CPUS="${LOAD_CPUS:-4-7,12-15}"
# Ports of their own, so a bench/run.sh running at the same time doesn't clash.
ASTRO_PORT=4411
ORBIT_PORT=4410
PPROF_ADDR=127.0.0.1:4412

OUT="$BENCH/results/profile-$(date +%Y-%m-%d-%H%M%S)"
mkdir -p "$OUT"
LOAD="$OUT/load"
SERVER_PID=""
LOAD_PID=""

log() { printf '\033[1m==> %s\033[0m\n' "$*" >&2; }
cleanup() {
	if [[ -n "$LOAD_PID" ]]; then kill "$LOAD_PID" 2>/dev/null || true; fi
	if [[ -n "$SERVER_PID" ]]; then kill "$SERVER_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT

secs() { echo "${1%s}"; }
pin() { # CPUS: the exec prefix (taskset execs the server, so $! is its PID)
	if command -v taskset >/dev/null; then echo "taskset -c $1"; else echo env; fi
}

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

stop_server() {
	if [[ -n "$SERVER_PID" ]]; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
		SERVER_PID=""
	fi
}

start_load() { # PORT NAME: runs the load generator in the background
	local p
	read -ra p <<<"$(pin "$LOAD_CPUS")"
	"${p[@]}" "$LOAD" run -url "http://127.0.0.1:$1/$PAGE" -c "$CONCURRENCY" \
		-warmup "$WARMUP" -d "$DURATION" -pid "$SERVER_PID" >"$OUT/load-$2.json" &
	LOAD_PID=$!
}

wait_load() {
	wait "$LOAD_PID"
	LOAD_PID=""
}

# --- build -------------------------------------------------------------------
log "building the load generator"
(cd "$BENCH/load" && go build -o "$LOAD" .)
if [[ "${SKIP_BUILD:-0}" != 1 ]]; then
	if [[ " $SERVERS " == *" orbit "* ]]; then
		log "building bench/orbit-site (go tool orbit build)"
		(cd "$ORBIT_SITE" && go tool orbit build)
	fi
	if [[ " $SERVERS " == *" astro "* ]]; then
		log "building bench/astro-site (npm ci && npm run build)"
		(cd "$ASTRO_SITE" && npm ci --no-audit --no-fund --loglevel=error && ASTRO_TELEMETRY_DISABLED=1 npm run build --silent >/dev/null)
	fi
fi

{
	echo "# Profile of /$PAGE, $(date -Iseconds)"
	echo
	echo "- CPU: $(lscpu | sed -n 's/^Model name: *//p' | head -1), $(nproc) logical CPUs, $(uname -sr)"
	echo "- $(go version | awk '{print $3}'), Node $(node --version), astro $(node -p "require('$ASTRO_SITE/node_modules/astro/package.json').version"), @astrojs/node $(node -p "require('$ASTRO_SITE/node_modules/@astrojs/node/package.json').version")"
	echo "- load: $CONCURRENCY keep-alive connections, $WARMUP warm-up, $DURATION profiled; load generator on CPUs $LOAD_CPUS"
	echo "- load average at start: $(cut -d' ' -f1-3 /proc/loadavg)"
	echo
} >"$OUT/summary.md"

# --- Astro ---------------------------------------------------------------------
if [[ " $SERVERS " == *" astro "* ]]; then
	read -ra p <<<"$(pin "$ASTRO_CPUS")"
	astro_start() { # MODE
		(cd "$ASTRO_SITE" && NODE_ENV=production HOST=127.0.0.1 PORT="$ASTRO_PORT" \
			PROFILE_MODE="$1" PROFILE_OUT="$OUT/astro${2:-}.cpuprofile" \
			exec "${p[@]}" node --import "$PROF/node-hook.mjs" ./dist/server/entry.mjs) >/dev/null &
		SERVER_PID=$!
		wait_ready "http://127.0.0.1:$ASTRO_PORT/$PAGE"
	}

	log "Astro: CPU profile of /$PAGE under load (CPUs $ASTRO_CPUS)"
	astro_start cpu
	start_load "$ASTRO_PORT" astro
	sleep "$(secs "$WARMUP")"
	kill -USR2 "$SERVER_PID"
	sleep "$(secs "$DURATION")"
	kill -USR2 "$SERVER_PID"
	wait_load
	for _ in $(seq 100); do [[ -s "$OUT/astro.cpuprofile.json" ]] && break; sleep 0.1; done
	stop_server

	log "Astro: promises and async resources per request (100 sequential requests)"
	astro_start promises -promises
	for _ in $(seq 50); do curl -fs -o /dev/null "http://127.0.0.1:$ASTRO_PORT/$PAGE"; done
	kill -USR2 "$SERVER_PID"
	sleep 0.5
	for _ in $(seq 100); do curl -fs -o /dev/null "http://127.0.0.1:$ASTRO_PORT/$PAGE"; done
	kill -USR2 "$SERVER_PID"
	for _ in $(seq 100); do [[ -s "$OUT/astro-promises.cpuprofile.json" ]] && break; sleep 0.1; done
	mv "$OUT/astro-promises.cpuprofile.json" "$OUT/astro-promises.json"
	stop_server

	node "$PROF/summarize-cpuprofile.mjs" "$OUT/astro.cpuprofile" "$OUT/load-astro.json" "$OUT/astro-promises.json" >>"$OUT/summary.md"
fi

# --- Orbit ---------------------------------------------------------------------
if [[ " $SERVERS " == *" orbit "* ]]; then
	log "Orbit: building dist/server with a pprof listener (go build -overlay)"
	if [[ ! -d "$ORBIT_SITE/orbit_gen" ]]; then (cd "$ORBIT_SITE" && go tool orbit build); fi
	printf '{"Replace":{"%s":"%s"}}\n' "$ORBIT_SITE/cmd/server/zz_pprof.go" "$PROF/pprof.go.in" >"$OUT/overlay.json"
	(cd "$ORBIT_SITE" && go build -overlay "$OUT/overlay.json" -o "$OUT/orbit-server" ./cmd/server)

	log "Orbit: CPU profile of /$PAGE under load (CPUs $ORBIT_CPUS, GOMAXPROCS=$ORBIT_GOMAXPROCS)"
	read -ra p <<<"$(pin "$ORBIT_CPUS")"
	(cd "$ORBIT_SITE" && GOMAXPROCS="$ORBIT_GOMAXPROCS" PORT="$ORBIT_PORT" ORBIT_PPROF_ADDR="$PPROF_ADDR" \
		exec "${p[@]}" "$OUT/orbit-server") &
	SERVER_PID=$!
	wait_ready "http://127.0.0.1:$ORBIT_PORT/$PAGE"
	start_load "$ORBIT_PORT" orbit
	sleep "$(secs "$WARMUP")"
	curl -fs -o "$OUT/orbit.pprof" "http://$PPROF_ADDR/debug/pprof/profile?seconds=$(secs "$DURATION")"
	wait_load
	stop_server

	go tool pprof -traces "$OUT/orbit-server" "$OUT/orbit.pprof" 2>/dev/null >"$OUT/orbit-traces.txt"
	node "$PROF/summarize-pprof.mjs" "$OUT/orbit-traces.txt" "$OUT/load-orbit.json" >>"$OUT/summary.md"
fi

echo "- load average at end: $(cut -d' ' -f1-3 /proc/loadavg)" >>"$OUT/summary.md"
log "results: $OUT/summary.md"
cat "$OUT/summary.md"
