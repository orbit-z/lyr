#!/bin/sh
# Headless syntax tests for editors/nvim: runs syntax.vim in Neovim and, when
# installed, Vim, each twice: with the bundled syntax files (full) and with
# none of them (bare). Exits nonzero on any failure.
#
#   editors/nvim/test/run.sh
#   LYR_TEST_LINES=500 editors/nvim/test/run.sh   # also time a 500-line file
set -u
here=$(cd "$(dirname "$0")" && pwd)
LYR_PLUGIN=$(dirname "$here")
LYR_TEST_OUT=$(mktemp)
export LYR_PLUGIN LYR_TEST_OUT
trap 'rm -f "$LYR_TEST_OUT"' EXIT
status=0
ran=0

# The plugin goes first on 'runtimepath'.
setup='let &rtp = $LYR_PLUGIN . "," . &rtp'

for editor in nvim vim; do
	command -v $editor >/dev/null 2>&1 || continue
	ran=1
	for mode in full bare; do
		: >"$LYR_TEST_OUT"
		if [ $editor = nvim ]; then
			set -- nvim --headless --clean -u NONE -i NONE -n
		else
			set -- vim -N -u NONE -i NONE -n -es
		fi
		LYR_TEST_MODE=$mode "$@" --cmd "$setup" --cmd 'filetype on' --cmd 'syntax on' \
			-S "$here/syntax.vim" </dev/null || status=1
		if [ -s "$LYR_TEST_OUT" ]; then
			sed "s/^/$editor ($mode): /" "$LYR_TEST_OUT"
		else
			echo "$editor ($mode): no report (the editor failed to run the test)"
			status=1
		fi
	done
done
if [ $ran = 0 ]; then
	echo "neither nvim nor vim found" >&2
	exit 1
fi
exit $status
