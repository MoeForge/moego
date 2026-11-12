#!/bin/bash
# w7.sh DIR TIMECPU PEAKCPUS SIZE: runs the large-body scenarios with the
# commands DIR/w7-<engine> (go build -o DIR/w7-<engine> ./cmd/w7/<engine>)
# on a SIZE MiB body, three runs per scenario and engine, in two passes:
# time on TIMECPU, one CPU, into DIR/w7t.out; memory on PEAKCPUS, two CPUs,
# so that the heap sampler runs while the engine works, into DIR/w7p.out.
# ojson's own scenarios use DIR/w7-ojson, when it is there.
set -eu
D=$1; TC=$2; PC=$3; MB=$4
engs="moego encjson jsonv2 sonic-std sonic-default goccy"
F=$D/w7-$MB.json
"$D/w7-moego" -scenario gen -size "$MB" -file "$F"
for pass in t p; do
	if [ $pass = t ]; then cpus=$TC; else cpus=$PC; fi
	out=$D/w7$pass.out
	for sc in decoder bytes marshal marshalp; do
		for e in $engs; do
			for r in 1 2 3; do taskset -c "$cpus" "$D/w7-$e" -scenario $sc -size "$MB" -file "$F" | tail -1 >>"$out"; done
		done
	done
	for e in moego encjson jsonv2 goccy; do
		for r in 1 2 3; do taskset -c "$cpus" "$D/w7-$e" -scenario valid -size "$MB" -file "$F" | tail -1 >>"$out"; done
	done
	if [ -x "$D/w7-ojson" ]; then
		for sc in validr getmany getmanyr; do
			for r in 1 2 3; do taskset -c "$cpus" "$D/w7-ojson" -scenario $sc -size "$MB" -file "$F" | tail -1 >>"$out"; done
		done
	fi
done
rm -f "$F"
