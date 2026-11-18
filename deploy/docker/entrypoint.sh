#!/bin/sh
# sbpanel container entrypoint.
#   master [sbmaster flags]    run the panel (settings via SBM_* variables)
#   node   [sbnode run flags]  enroll once from SBN_MASTER / SBN_TOKEN / SBN_CA_SHA256, then run the node
#   anything else is executed as is (e.g. "sh")
# The binary comes from the GitHub release SBP_VERSION (vX.Y.Z or "latest"), optionally through
# SBP_MIRROR (a download prefix such as https://ghproxy.example/). It is verified against the
# release's checksums.txt and kept in <data>/bin, so a restart without network still works.
set -eu
REPO="genguzzz/sbpanel"

say() { echo "[entrypoint] $*"; }
die() { echo "[entrypoint] $*" >&2; exit 1; }

arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

# HTTPS only, redirects included: the archive is only as trustworthy as checksums.txt,
# which comes over the same connection
dl() { curl --proto '=https' --proto-redir '=https' "$@"; }

# vX.Y.Z[-pre]: it ends up in URLs and paths
valid_version() {
	case "$1" in v[0-9]*.[0-9]*.[0-9]*) ;; *) return 1 ;; esac
	case "$1" in *[!0-9A-Za-z.-]*) return 1 ;; esac
}

case "${SBP_MIRROR:-}" in "" | https://*) ;; *) die "SBP_MIRROR must be an https:// URL" ;; esac

latest() {
	url=$(dl -fsSLI --retry 2 -o /dev/null -w '%{url_effective}' "${SBP_MIRROR:-}https://github.com/$REPO/releases/latest") || return 1
	tag="${url##*/}"
	valid_version "$tag" || return 1
	echo "$tag"
}

# checksums OUT VERSION MIRRORBASE: checksums.txt from github.com directly, so a mirror can't
# serve a tampered archive with a matching sum. Only when github.com is unreachable (a network
# error or timeout; an HTTP error is an answer) does the mirror's copy count, with a warning.
checksums() {
	direct="https://github.com/$REPO/releases/download/$2/checksums.txt"
	[ -n "${SBP_MIRROR:-}" ] || { dl -fsSL --retry 3 -o "$1" "$direct"; return; }
	rc=0
	dl -fsSL --max-time 15 -o "$1" "$direct" || rc=$?
	[ "$rc" = 0 ] && return 0
	[ "$rc" = 22 ] && return 1
	say "github.com unreachable; checksums.txt came from the mirror: integrity depends on the mirror"
	dl -fsSL --retry 3 -o "$1" "$3/checksums.txt"
}

# fetch NAME DATADIR: make sure <data>/bin/NAME is the wanted release
fetch() {
	name="$1"; bin="$2/bin"
	mkdir -p "$bin"
	have=""; [ -x "$bin/$name" ] && [ -f "$bin/$name.version" ] && have="$(cat "$bin/$name.version")"
	want="${SBP_VERSION:-latest}"
	if [ "$want" = latest ]; then
		if ! want="$(latest)"; then
			[ -n "$have" ] || die "cannot determine the latest release (set SBP_VERSION or SBP_MIRROR)"
			say "cannot reach GitHub; running the cached $name $have"
			return 0
		fi
	fi
	valid_version "$want" || die "bad SBP_VERSION: $want (want vX.Y.Z or latest)"
	[ "$have" = "$want" ] && { say "$name $want (cached)"; return 0; }
	a="$(arch)"; pkg="sbpanel_${want}_linux_${a}"
	base="${SBP_MIRROR:-}https://github.com/$REPO/releases/download/$want"
	tmp="$(mktemp -d "$bin/.dl.XXXXXX")"
	say "downloading $name $want ($a)"
	if ! dl -fsSL --retry 3 -o "$tmp/$pkg.tar.gz" "$base/$pkg.tar.gz" ||
		! checksums "$tmp/checksums.txt" "$want" "$base"; then
		rm -rf "$tmp"
		[ -n "$have" ] || die "download failed: $base/$pkg.tar.gz"
		say "download failed; running the cached $name $have"
		return 0
	fi
	sum="$(grep " $pkg.tar.gz\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
	[ -n "$sum" ] && [ "$sum" = "$(sha256sum "$tmp/$pkg.tar.gz" | cut -d' ' -f1)" ] || { rm -rf "$tmp"; die "checksum mismatch for $pkg.tar.gz"; }
	tar -xzf "$tmp/$pkg.tar.gz" -C "$tmp" "$pkg/$name"
	[ -f "$tmp/$pkg/$name" ] && [ ! -L "$tmp/$pkg/$name" ] || { rm -rf "$tmp"; die "$pkg.tar.gz: $name is not a regular file"; }
	chmod 0755 "$tmp/$pkg/$name"
	[ -n "$have" ] && cp -p "$bin/$name" "$bin/$name.prev"
	mv -f "$tmp/$pkg/$name" "$bin/$name"
	echo "$want" >"$bin/$name.version"
	rm -rf "$tmp"
	say "$name ${have:-none} -> $want"
}

cmd="${1:-master}"
[ $# -gt 0 ] && shift
case "$cmd" in
master)
	data="${SBM_DATA:-/var/lib/sbmaster}"
	fetch sbmaster "$data"
	exec "$data/bin/sbmaster" --data "$data" "$@"
	;;
node)
	dir="${SBN_DIR:-/var/lib/sbnode}"
	fetch sbnode "$dir"
	if [ ! -s "$dir/node_id" ]; then
		if [ -z "${SBN_MASTER:-}" ] || [ -z "${SBN_TOKEN:-}" ] || [ -z "${SBN_CA_SHA256:-}" ]; then
			die "sbnode is not enrolled: set SBN_MASTER, SBN_TOKEN and SBN_CA_SHA256 (from the panel's Add node)"
		fi
		chmod 700 "$dir"
		"$dir/bin/sbnode" enroll --dir "$dir" --master "$SBN_MASTER" --token "$SBN_TOKEN" --ca-sha256 "$SBN_CA_SHA256"
	fi
	exec "$dir/bin/sbnode" run --dir "$dir" "$@"
	;;
*)
	exec "$cmd" "$@"
	;;
esac
