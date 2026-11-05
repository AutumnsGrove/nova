#!/bin/sh
# package.sh builds the release archives and their checksums.
#
# usage: package.sh <version> <os> <arch> <full-binary> <min-binary> <outdir>
#
# Each archive holds the binary as `nova`, plus LICENSE and README.md. The full
# archive is nova_<version>_<os>_<arch>.tar.gz and the notui build is
# nova-min_<version>_<os>_<arch>.tar.gz. checksums.txt gets one SHA-256 line per
# archive, in the order the archives are made.
set -eu

version=$1
os=$2
arch=$3
full=$4
min=$5
out=$6

mkdir -p "$out"
stage="$out/.stage"
rm -rf "$stage"
mkdir -p "$stage"

pack() { # prefix binary
	prefix=$1
	binary=$2
	rm -rf "$stage/pkg"
	mkdir -p "$stage/pkg"
	cp "$binary" "$stage/pkg/nova"
	cp LICENSE README.md "$stage/pkg/"
	archive="${prefix}_${version}_${os}_${arch}.tar.gz"
	tar -czf "$out/$archive" -C "$stage/pkg" nova LICENSE README.md
}

pack nova "$full"
pack nova-min "$min"

: >"$out/checksums.txt"
for archive in "$out"/nova_"${version}"_"${os}"_"${arch}".tar.gz "$out"/nova-min_"${version}"_"${os}"_"${arch}".tar.gz; do
	name=$(basename "$archive")
	if command -v sha256sum >/dev/null 2>&1; then
		sum=$(sha256sum "$archive" | awk '{print $1}')
	else
		sum=$(shasum -a 256 "$archive" | awk '{print $1}')
	fi
	printf '%s  %s\n' "$sum" "$name" >>"$out/checksums.txt"
done

rm -rf "$stage"
