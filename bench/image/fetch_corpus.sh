#!/bin/sh
# Downloads the QOI benchmark suite (public test images collected by
# Dominic Szablewski for https://qoiformat.org/benchmark/), extracts the
# images listed in corpus.txt and verifies their sha256.
#
#   ./fetch_corpus.sh DIR       images end up in DIR/images/<category>/...
#   LIST=holdout.txt ./fetch_corpus.sh DIR
#                               the same for another list of the suite
set -eu
dir=${1:?usage: fetch_corpus.sh DIR}
here=$(cd "$(dirname "$0")" && pwd)
list=$(cd "$(dirname "${LIST:-$here/corpus.txt}")" && pwd)/$(basename "${LIST:-corpus.txt}")
url=https://qoiformat.org/benchmark/qoi_benchmark_suite.tar
tar_sha256=00166f555ca760e23647025e46d9b046e03cd2e22869d4ecb496edc527cd0d7d
mkdir -p "$dir"
cd "$dir"
if [ ! -f qoi_benchmark_suite.tar ] || ! echo "$tar_sha256  qoi_benchmark_suite.tar" | sha256sum -c --status; then
	curl -fL --retry 3 -o qoi_benchmark_suite.tar.part "$url"
	mv qoi_benchmark_suite.tar.part qoi_benchmark_suite.tar
fi
echo "$tar_sha256  qoi_benchmark_suite.tar" | sha256sum -c
# Paths may contain spaces: everything after the two-space separator.
grep -v '^#' "$list" | sed -n 's/^[0-9a-f]\{64\}  /images\//p' > corpus.files
tar xf qoi_benchmark_suite.tar --verbatim-files-from -T corpus.files
grep -v '^#' "$list" | sed -n 's/^\([0-9a-f]\{64\}\)  /\1  images\//p' | sha256sum -c --quiet
echo "corpus ready: $(wc -l < corpus.files) images in $dir/images"
