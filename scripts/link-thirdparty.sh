#!/usr/bin/env bash
# Populate thirdparty/store/ with symlinks to fabric-store's C sources.
#
# This is a local-dev fallback. The real mechanism is a linkfile in the
# goal manifest's default.xml.
# When repo sync populates thirdparty/store/ automatically, this script
# becomes unnecessary; until then, run it after checkout.
#
# The directory is named "thirdparty" (not "vendor") because Go treats a
# top-level vendor/ dir as its own vendored-dependencies dir and demands
# a matching modules.txt.
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
store="${here}/../../6-datasource/store"

if [[ ! -d "${store}" ]]; then
	echo "not a workspace checkout: ${store} not found" >&2
	exit 1
fi

mkdir -p "${here}/thirdparty/store"
for f in fdb_vfs.c fdb_keys.h; do
	if [[ ! -f "${store}/${f}" ]]; then
		echo "missing source: ${store}/${f}" >&2
		exit 1
	fi
	ln -sfn "${store}/${f}" "${here}/thirdparty/store/${f}"
	echo "linked thirdparty/store/${f} -> ${store}/${f}"
done
