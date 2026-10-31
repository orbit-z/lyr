#!/usr/bin/env bash
# Timing driver for the typecheck spike. Usage: ./bench.sh <N> [cold|warm|dev|all]
# Prefix with CORES=0-3 to pin everything to 4 CPUs (taskset).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")" && pwd)
N=$1; WHAT=${2:-all}
SITE=$ROOT/sites/s$N
TB=$ROOT/bin/tcbench
SCR=${SCR:-${TMPDIR:-/tmp}/lyr-tcspike}
REPS=${REPS:-5}
mkdir -p "$SCR"
PIN=() # kept empty: pinning is done by re-exec'ing the whole script
if [[ -n ${CORES:-} && -z ${PINNED:-} ]]; then PINNED=$CORES exec taskset -c "$CORES" "$0" "$@"; fi
case $N in 50) C=c017 ;; 200) C=c077 ;; *) C=c017 ;; esac

now() { date +%s%N; }
# run CMD..., print elapsed ms; fail loudly
ms() { local s; s=$(now); if ! "${PIN[@]}" "$@" >"$SCR/last.out" 2>&1; then echo "FAIL: $*" >&2; cat "$SCR/last.out" >&2; exit 1; fi; echo $(( ($(now) - s) / 1000000 )); }
stats() { # label values...
  local label=$1; shift
  local sorted; sorted=$(printf '%s\n' "$@" | sort -n)
  local arr=($sorted); local n=${#arr[@]}
  printf '%-58s min %6s  median %6s  ms  (n=%d: %s)\n' "$label" "${arr[0]}" "${arr[$((n/2))]}" "$n" "$*"
}
gen() { "$ROOT/bin/gensite" -n "$N" -out "$SITE" -stub "$ROOT/lyrstub" >/dev/null; }
build() { (cd "$SITE" && go build -o "$SCR/render" ./lyr_gen/cmd/render); }
freshcache() { rm -rf "$SCR/gc"; if [[ ${1:-} == std ]]; then cp -a "$SCR/gc-std" "$SCR/gc"; else mkdir -p "$SCR/gc"; fi; export GOCACHE=$SCR/gc; }

echo "# s$N  nproc=$(nproc) pin=${PINNED:-none}  $(date -Is)  load: $(cut -d' ' -f1-3 /proc/loadavg)"

# std-warm cache: stdlib and the stub runtime compiled, project packages not.
if [[ ! -d $SCR/gc-std ]]; then
  gen
  (cd "$SITE" && GOCACHE=$SCR/gc-std.tmp go build github.com/bertrandkhe/lyr net/http time strings strconv fmt bytes os) && mv "$SCR/gc-std.tmp" "$SCR/gc-std"
fi

if [[ $WHAT == cold || $WHAT == all ]]; then
  for kind in empty std; do
    a=(); b=(); c=(); d=(); e=(); f=()
    for i in $(seq 3); do
      gen; freshcache $kind; a+=($(ms "$TB" -dir "$SITE" -mode pkgs-all -n 1 -q))
      gen; freshcache $kind; b+=($(ms "$TB" -dir "$SITE" -mode custom-all -n 1 -q))
      gen; freshcache $kind; c+=($(ms build))
      gen; freshcache $kind; d+=($(ms bash -c "'$TB' -dir '$SITE' -mode pkgs-all -n 1 -q -rewrite && cd '$SITE' && go build -o '$SCR/render' ./lyr_gen/cmd/render"))
      gen; freshcache $kind; e+=($(ms bash -c "'$TB' -dir '$SITE' -mode custom-all -n 1 -q -rewrite && cd '$SITE' && go build -o '$SCR/render' ./lyr_gen/cmd/render"))
      gen; freshcache $kind; f+=($(ms bash -c "'$TB' -dir '$SITE' -mode custom-srcdecl -n 1 -q -rewrite && cd '$SITE' && go build -o '$SCR/render' ./lyr_gen/cmd/render"))
    done
    stats "cold($kind cache) packages.Load all impl" "${a[@]}"
    stats "cold($kind cache) custom importer all impl" "${b[@]}"
    stats "cold($kind cache) go build (pass-1 code)" "${c[@]}"
    stats "cold($kind cache) build pipeline: packages.Load+rewrite+go build" "${d[@]}"
    stats "cold($kind cache) build pipeline: custom+rewrite+go build" "${e[@]}"
    stats "cold($kind cache) build pipeline: srcdecl+rewrite+go build" "${f[@]}"
  done
fi

warmup() { freshcache std; gen; rm -rf "$SCR/pass1"; cp -a "$SITE/lyr_gen/impl" "$SCR/pass1"; cp "$SITE/lyr_gen/decl/components/$C/decl.go" "$SCR/decl.go"; cp "$SITE/internal/posts/posts.go" "$SCR/posts.go"
  "$TB" -dir "$SITE" -mode custom-all -n 1 -q -rewrite >/dev/null; build; build; }
restore() { for p in ${1//,/ }; do cp "$SCR/pass1/$p/render.go" "$SITE/lyr_gen/impl/$p/render.go"; done; }

if [[ $WHAT == warm || $WHAT == all ]]; then
  warmup
  restore "$(cd "$SCR/pass1" && find . -name render.go | xargs -n1 dirname | sed 's|^\./||' | paste -sd,)"
  for m in pkgs-all custom-all pkgs-one custom-one custom-hot; do
    "${PIN[@]}" "$TB" -dir "$SITE" -mode $m -pkg components/$C -n $((REPS+1)) -q | sed 's/^/warm, no change: /'
  done
  a=(); for i in $(seq $REPS); do a+=($(ms build)); done
  stats "warm, no change: go build (up to date)" "${a[@]}"
fi

if [[ $WHAT == dev || $WHAT == all ]]; then
  warmup
  ALL=$(cd "$SCR/pass1" && find . -name render.go | xargs -n1 dirname | sed 's|^\./||' | sort | paste -sd,)
  IMPORTERS=$(grep -rl "decl/components/$C\"" "$SCR/pass1" | xargs -n1 dirname | sed "s|$SCR/pass1/||" | sort -u | paste -sd,)
  echo "edited component: $C; impl packages affected by a Props change: $IMPORTERS"
  editbody() { sed "s|</ul>\\\\n|</ul><!--v$1-->\\\\n|" "$SCR/pass1/components/$C/render.go" > "$SITE/lyr_gen/impl/components/$C/render.go"; }
  editprops() { sed "s|^\tAuthor   \*posts.Author|\tAuthor   *posts.Author\n\tExtra$1 int|" "$SCR/decl.go" > "$SITE/lyr_gen/decl/components/$C/decl.go"; restore "$IMPORTERS"; }
  edituser() { cp "$SCR/posts.go" "$SITE/internal/posts/posts.go"; echo "func F$1() int { return $1 }" >> "$SITE/internal/posts/posts.go"; restore "$ALL"; }
  k=0
  for approach in ${APPROACHES:-none pkgs custom srcdecl}; do
    # Each approach lives in its own consistent world: "none" keeps pass-1
    # (Expr) code on disk; the typed approaches keep typed code on disk and
    # restore pass-1 code only for the packages a change regenerates.
    restore "$ALL"
    [[ $approach != none ]] && "$TB" -dir "$SITE" -mode custom-all -n 1 -q -rewrite >/dev/null
    build
    for edit in body props user; do
      case $edit in
        body) PK=components/$C; ONE=one ;;
        props) PK=$IMPORTERS; ONE=one ;;
        user) PK=; ONE=all ;;
      esac
      tc=(); bd=(); tot=()
      for i in $(seq $REPS); do
        k=$((k+1)); edit$edit $k
        if [[ $approach == none ]]; then t=0
        elif [[ $approach == srcdecl ]]; then t=$(ms "$TB" -dir "$SITE" -mode custom-srcdecl -n 1 -q -rewrite) # checks all impl packages
        else t=$(ms "$TB" -dir "$SITE" -mode $approach-$ONE -pkg "$PK" -n 1 -q -rewrite); fi
        b=$(ms build)
        tc+=($t); bd+=($b); tot+=($((t+b)))
      done
      [[ $approach != none ]] && stats "dev, $edit edit, $approach: type-check+rewrite" "${tc[@]}"
      stats "dev, $edit edit, $approach: go build after" "${bd[@]}"
      stats "dev, $edit edit, $approach: TOTAL" "${tot[@]}"
    done
  done
  restore "$ALL"
  "${PIN[@]}" "$TB" -dir "$SITE" -mode custom-hot -pkg components/$C -n $((REPS+1)) -q | sed 's/^/dev, in-process hot re-check of one package: /'
fi
