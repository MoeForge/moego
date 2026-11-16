#!/bin/sh
# Fetches the C headers for the cgo competitors (libwebp, libpng, zlib,
# libdeflate) as Debian -dev packages without root, and links their .so
# names to the system's runtime libraries, so the benchmark links the
# installed libwebp.so.7 / libpng16.so.16 / libz.so.1 / libdeflate.so.0.
# It also builds fpnge (github.com/veluca93/fpnge at a pinned commit) into
# DIR/usr/lib/x86_64-linux-gnu/libfpnge.a; build imgbench with -tags fpnge
# to include it.
#
#   ./sysroot.sh DIR    then build with
#   CGO_CFLAGS="-I DIR/usr/include" CGO_LDFLAGS="-L DIR/usr/lib/x86_64-linux-gnu"
set -eu
dir=${1:?usage: sysroot.sh DIR}
mkdir -p "$dir/debs"
cd "$dir/debs"
apt-get download libpng-dev libwebp-dev libsharpyuv-dev zlib1g-dev libdeflate-dev webp
for d in *.deb; do dpkg-deb -x "$d" ..; done
cd ..
lib=usr/lib/x86_64-linux-gnu
sys=/usr/lib/x86_64-linux-gnu
for pair in libwebp.so:libwebp.so.7 libsharpyuv.so:libsharpyuv.so.0 libpng16.so:libpng16.so.16 libpng.so:libpng16.so.16 libz.so:libz.so.1 libdeflate.so:libdeflate.so.0; do
	ln -sfn "$sys/${pair#*:}" "$lib/${pair%%:*}"
done
dpkg-deb -f debs/libwebp-dev_*.deb Package Version
dpkg-deb -f debs/libpng-dev_*.deb Package Version
dpkg-deb -f debs/zlib1g-dev_*.deb Package Version
dpkg-deb -f debs/libdeflate-dev_*.deb Package Version

fpnge_commit=097ebccf3d3db443d9a8f238b1deed010a82f961
for try in 1 2 3; do
	[ -d src/fpnge/.git ] && break
	rm -rf src/fpnge
	git clone -q https://github.com/veluca93/fpnge.git src/fpnge || sleep 10
done
git -C src/fpnge checkout -q "$fpnge_commit"
g++ -O2 -mavx2 -mbmi2 -mpclmul -msse4.1 -fPIC -c src/fpnge/fpnge.cc -o src/fpnge/fpnge.o
ar rcs "$lib/libfpnge.a" src/fpnge/fpnge.o
cp src/fpnge/fpnge.h usr/include/
echo "fpnge $fpnge_commit"
