#!/bin/sh
# Runs the whole lossless image benchmark on Linux amd64:
#
#   bench/image/run.sh OUT
#
# Environment (defaults in brackets):
#   CORPUS        corpus directory, see fetch_corpus.sh [OUT/../image-corpus]
#   SYSROOT       C headers for cgo, see sysroot.sh [OUT/../image-sysroot]
#   CHECK_CPUS    CPUs for the build and the check pass, one process each
#                 [all but the last four]
#   TIMING_CPUS   CPUs for the timing passes, one process each [the last four]
#   HEAVY         encoders too slow to time on every image [see below]
#   HEAVY_EVERY   they are timed on every n-th image of corpus.txt [4]
#   SLOW          encoders checked (and timed) on every SLOW_EVERY-th image only [8]
#   MACHINE       machine line for summary.md [uname and go version]
#   NOTES         markdown file inserted into summary.md after the main table
#
# 1. check: every encoder encodes every image once; each output is decoded
#    by the Go and the C decoder and compared with the source pixels. This
#    pass gives the sizes and the failures.
# 2. timing: every encoder that passed is timed on its images, one process
#    per CPU (images balanced by pixel count), GOMAXPROCS=1.
# 3. heavy timing: the HEAVY encoders, timed on every HEAVY_EVERY-th image.
#    SLOW encoders are checked and timed on every SLOW_EVERY-th image only.
set -eu
ncpu=$(nproc)
[ "$ncpu" -ge 8 ] || [ -n "${CHECK_CPUS:-}${TIMING_CPUS:-}" ] || { echo "run.sh needs 8 CPUs, or CHECK_CPUS and TIMING_CPUS" >&2; exit 1; }
check_cpus=${CHECK_CPUS:-$(seq -s ' ' 0 $((ncpu - 5)))}
timing_cpus=${TIMING_CPUS:-$(seq -s ' ' $((ncpu - 4)) $((ncpu - 1)))}
# Keep this shell and every helper on the check CPUs, away from the timing
# CPUs; the measuring processes below pin themselves to single CPUs.
if [ -z "${RUN_SH_PINNED:-}" ]; then
	export RUN_SH_PINNED=1
	exec taskset -c "$(echo $check_cpus | tr ' ' ,)" sh "$0" "$@"
fi
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: run.sh OUT}
heavy=${HEAVY:-mayahiro/best,gen2brain-wasm/z9,libwebp/z9}
# gowebper levels above 0 fail to round-trip on most of the corpus and take
# minutes per image; they are checked on every SLOW_EVERY-th image only.
slow=${SLOW:-gowebper/level6-default,gowebper/level9}
slow_every=${SLOW_EVERY:-8}
heavy_every=${HEAVY_EVERY:-4}
machine=${MACHINE:-"$(uname -srm), $(go version | cut -d' ' -f3)"}
mkdir -p "$out/bin"
out=$(cd "$out" && pwd)
corpus=${CORPUS:-$(dirname "$out")/image-corpus}
sysroot=${SYSROOT:-$(dirname "$out")/image-sysroot}
first_cpu=${check_cpus%% *}

[ -f "$sysroot/usr/include/fpnge.h" ] || nice -n 10 "$here/sysroot.sh" "$sysroot"
[ -f "$corpus/corpus.files" ] || nice -n 10 "$here/fetch_corpus.sh" "$corpus"

cd "$here"
export CGO_ENABLED=1 CGO_CFLAGS="-O2 -I$sysroot/usr/include" CGO_LDFLAGS="-L$sysroot/usr/lib/x86_64-linux-gnu"
cpulist=$(echo $check_cpus | tr ' ' ,)
nice -n 10 taskset -c "$cpulist" go build -trimpath -tags nodynamic,fpnge -o "$out/bin/imgbench" ./cmd/imgbench
nice -n 10 taskset -c "$cpulist" go build -trimpath -o "$out/bin/imgsum" ./cmd/imgsum
{ go version; go list -m all; "$out/bin/imgbench" -listenc; } > "$out/versions.txt"
cp corpus.txt "$out/"
bench="$out/bin/imgbench -corpus $corpus/images -list $here/corpus.txt"

# pass DIR NICE CPUS ARGS...: one imgbench per CPU, shard k of n on the k-th
# CPU. A pass that already finished (DIR/DONE) is skipped; an interrupted
# one resumes under new file tags, skipping the cells it already has.
pass() {
	dir=$1 niceness=$2 cpus=$3
	shift 3
	[ -f "$dir/DONE" ] && return
	mkdir -p "$dir"
	try=$(ls "$dir"/log-*.txt 2>/dev/null | sed 's/.*-r\([0-9]*\)-shard.*/\1/' | sort -n | tail -1)
	try=$(( ${try:-0} + 1 ))
	n=$(echo $cpus | wc -w)
	k=0
	pids=
	for cpu in $cpus; do
		nice -n "$niceness" taskset -c "$cpu" $bench -out "$dir" -shard "$k/$n" -tag "r$try-shard$k" -resume "$@" \
			> "$dir/log-r$try-shard$k.txt" 2>&1 &
		pids="$pids $!"
		k=$((k + 1))
	done
	echo "$(date '+%F %T') $(basename "$dir") (try $try): pids$pids"
	st=0
	for p in $pids; do wait "$p" || st=1; done
	[ "$st" = 0 ] || { echo "a process failed; see $dir/log-*.txt" >&2; exit 1; }
	touch "$dir/DONE"
	echo "$(date '+%F %T') $(basename "$dir") done"
}

not() { echo "-$1" | sed 's/,/,-/g'; }
pass "$out/check" 10 "$check_cpus" -check -encoders "all,$(not "$slow")"
pass "$out/check-slow" 10 "$check_cpus" -check -encoders "$slow" -every "$slow_every"
pass "$out/timing" 0 "$timing_cpus" -checked "$out/check" -encoders "all,$(not "$heavy"),$(not "$slow")"
pass "$out/timing-heavy" 0 "$timing_cpus" -checked "$out/check" -encoders "$heavy" -every "$heavy_every"
pass "$out/timing-slow" 0 "$timing_cpus" -checked "$out/check-slow" -encoders "$slow" -every "$slow_every"

cat > "$out/commands.txt" <<CMDS
# $(go version)
bench/image/run.sh $out
# which runs, in bench/image:
./sysroot.sh $sysroot
./fetch_corpus.sh $corpus
CGO_CFLAGS="-O2 -I$sysroot/usr/include" CGO_LDFLAGS="-L$sysroot/usr/lib/x86_64-linux-gnu" \\
  go build -trimpath -tags nodynamic,fpnge -o imgbench ./cmd/imgbench
B="imgbench -corpus $corpus/images -list corpus.txt"
# k = 0..n-1, each process pinned to its own CPU
nice -n 10 taskset -c <one of $check_cpus> \$B -out check -check -shard k/$(echo $check_cpus | wc -w) -encoders 'all,$(not "$slow")'
nice -n 10 taskset -c <one of $check_cpus> \$B -out check-slow -check -shard k/$(echo $check_cpus | wc -w) -encoders '$slow' -every $slow_every
taskset -c <one of $timing_cpus> \$B -out timing -checked check -shard k/$(echo $timing_cpus | wc -w) \\
  -encoders 'all,$(not "$heavy"),$(not "$slow")'
taskset -c <one of $timing_cpus> \$B -out timing-heavy -checked check -shard k/$(echo $timing_cpus | wc -w) -every $heavy_every -encoders '$heavy'
taskset -c <one of $timing_cpus> \$B -out timing-slow -checked check-slow -shard k/$(echo $timing_cpus | wc -w) -every $slow_every -encoders '$slow'
go run ./cmd/imgsum -out . -check check,check-slow -timing timing,timing-heavy,timing-slow
CMDS
"$out/bin/imgsum" -out "$out" -check "$out/check,$out/check-slow" -timing "$out/timing,$out/timing-heavy,$out/timing-slow" \
	-machine "$machine" -commands "$out/commands.txt" ${NOTES:+-notes "$NOTES"}
echo "summary: $out/summary.md"
