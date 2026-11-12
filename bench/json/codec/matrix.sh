#!/bin/bash
# matrix.sh BIN CPU ROUNDS [GROUPS...]: runs the codec benchmarks of the
# test binary BIN (go test -c ./codec) with each row of each engine in a
# process of its own, one sample per process, on CPU, and prints the result
# lines, after the goos, goarch and cpu lines of the first process. Rows
# and engines rotate by one each round, so the n-th sample of a row's
# engines comes from the n-th round. GROUPS default to every workload:
# W1 W1t W2 W3 W3m W4 W5 W6 W8.
set -u
BIN=$1; CPU=$2; ROUNDS=$3; shift 3
GROUPS_=${*:-W1 W1t W2 W3 W3m W4 W5 W6 W8}
dec="moego encjson jsonv2 sonic-std sonic-default goccy moego-nocopy"
all="moego encjson jsonv2 sonic-std sonic-default goccy"
rows=()
for g in $GROUPS_; do
	case $g in
	W1) for s in 1KiB 64KiB 1MiB 8MiB; do rows+=("W1|^$s\$|$dec"); done ;;
	W1t) for s in 16KiB 100Ktok 256Ktok; do rows+=("W1t|^$s\$|$dec"); done ;;
	W2) for k in claude responses; do for s in 1KiB 1MiB; do rows+=("W2|^$k\$/^$s\$|$dec"); done; done ;;
	W3) for k in unmarshal marshal; do for c in content tool usage; do rows+=("W3|^$k\$/^$c\$|$all"); done; done ;;
	W3m) for s in 1KiB 64KiB 1MiB 8MiB 100Ktok 256Ktok; do rows+=("W3m|^$s\$|$all"); done ;;
	W4) for k in any aws; do for s in 1KiB 64KiB 1MiB; do rows+=("W4|^$k\$/^$s\$|$all"); done; done ;;
	W5) for s in 4KiB 16KiB; do rows+=("W5|^$s\$|$all"); done ;;
	W6) for k in unmarshal marshal; do for s in 1x1536 16x1536; do rows+=("W6|^$k\$/^$s\$|$all"); done; done ;;
	W8) for k in valid compact indent; do for s in 1KiB 1MiB; do rows+=("W8|^$k\$/^$s\$|$all"); done; done ;;
	*) echo "unknown group $g" >&2; exit 2 ;;
	esac
done
n=${#rows[@]}
keep='^(goos|goarch|cpu|Benchmark|FAIL|panic|---)'
for r in $(seq 1 "$ROUNDS"); do
	for i in $(seq 0 $((n - 1))); do
		IFS="|" read -r g pat engs <<<"${rows[$(((i + r - 1) % n))]}"
		es=($engs)
		m=${#es[@]}
		for j in $(seq 0 $((m - 1))); do
			e=${es[$(((j + r - 1) % m))]}
			# sonic has no Compact or Indent, so those rows print nothing for it.
			taskset -c "$CPU" "$BIN" -test.run '^$' -test.bench "^Benchmark$g\$/$pat/^$e\$" \
				-test.benchmem -test.benchtime 1s -test.cpu 1 2>&1 | grep -E "$keep"
			keep='^(Benchmark|FAIL|panic|---)'
		done
	done
done
