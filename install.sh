#!/bin/sh
# sbpanel installer: sing-box panel (master) and node.
#
#   curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- master
#   curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- node \
#        --master panel.example.com:9443 --token <token> --ca-sha256 <fingerprint>
#
# Commands:
#   master   [--web 127.0.0.1:8080] [--grpc :9443] [--public-grpc host:9443] [--admin-password PW] [--tz Asia/Shanghai]
#   node     --master HOST:PORT --token TOKEN --ca-sha256 FP     (copy from the panel's 添加节点)
#   upgrade  [master|node|all]     download the latest (or --version) release and restart
#   uninstall [master|node|all] [--purge]    --purge also deletes data
#   status
# Global options: --version vX.Y.Z (default: latest)   --mirror URL (prefix for GitHub downloads, e.g. https://ghproxy.example/)
#
# Supports Debian/Ubuntu, RHEL/CentOS/Rocky/Alma/Fedora, Alpine (OpenRC), Arch and openSUSE; amd64 and arm64.
set -eu

REPO="genguzzz/sbpanel"
BIN_DIR="/usr/local/bin"
SBP_VERSION=""
MIRROR=""

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "please run as root (sudo)"

CMD="${1:-}"
[ -n "$CMD" ] && shift || true

# ---- arguments ----
WEB="127.0.0.1:8080"; GRPC=":9443"; PUBLIC_GRPC=""; ADMIN_PW=""; TZ_ARG="Asia/Shanghai"
M_ADDR=""; M_TOKEN=""; M_FP=""; TARGET=""; PURGE=0
while [ $# -gt 0 ]; do
	case "$1" in
	--version) SBP_VERSION="$2"; shift 2 ;;
	--mirror) MIRROR="$2"; shift 2 ;;
	--web) WEB="$2"; shift 2 ;;
	--grpc) GRPC="$2"; shift 2 ;;
	--public-grpc) PUBLIC_GRPC="$2"; shift 2 ;;
	--admin-password) ADMIN_PW="$2"; shift 2 ;;
	--tz) TZ_ARG="$2"; shift 2 ;;
	--master) M_ADDR="$2"; shift 2 ;;
	--token) M_TOKEN="$2"; shift 2 ;;
	--ca-sha256) M_FP="$2"; shift 2 ;;
	--purge) PURGE=1; shift ;;
	master | node | all) TARGET="$1"; shift ;;
	*) die "unknown argument: $1" ;;
	esac
done
case "$MIRROR" in "" | https://*) ;; *) die "--mirror must be an https:// URL" ;; esac

# ---- platform ----
ARCH="$(uname -m)"
case "$ARCH" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported architecture: $ARCH (amd64 and arm64 are supported)" ;;
esac
[ "$(uname -s)" = Linux ] || die "only Linux is supported"

OS_ID=""; OS_LIKE=""
if [ -r /etc/os-release ]; then
	# read in a subshell: os-release defines VERSION etc., which must not leak into ours
	# shellcheck disable=SC1091
	OS_ID="$(. /etc/os-release; echo "${ID:-}")"
	# shellcheck disable=SC1091
	OS_LIKE="$(. /etc/os-release; echo "${ID_LIKE:-}")"
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
	INIT=systemd
elif command -v rc-service >/dev/null 2>&1; then
	INIT=openrc
else
	die "no supported init system found (systemd or OpenRC)"
fi

need() { command -v "$1" >/dev/null 2>&1; }

install_deps() {
	missing=""
	for c in curl tar; do need "$c" || missing="$missing $c"; done
	need sha256sum || need shasum || missing="$missing coreutils"
	[ -z "$missing" ] && return 0
	say "installing:$missing"
	case "$OS_ID $OS_LIKE" in
	*debian* | *ubuntu*) apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl tar ca-certificates coreutils ;;
	*alpine*) apk add --no-cache curl tar ca-certificates coreutils ;;
	*rhel* | *centos* | *fedora* | *rocky* | *almalinux*) (need dnf && dnf install -y -q curl tar ca-certificates coreutils) || yum install -y -q curl tar ca-certificates coreutils ;;
	*arch*) pacman -Sy --noconfirm curl tar ca-certificates coreutils ;;
	*suse*) zypper -n install curl tar ca-certificates coreutils ;;
	*) die "please install curl and tar first" ;;
	esac
}

# HTTPS only, redirects included: the archive is only as trustworthy as checksums.txt,
# which comes over the same connection
fetch() { curl --proto '=https' --proto-redir '=https' "$@"; }

# vX.Y.Z[-pre]: it ends up in URLs and paths
valid_version() {
	case "$1" in v[0-9]*.[0-9]*.[0-9]*) ;; *) return 1 ;; esac
	case "$1" in *[!0-9A-Za-z.-]*) return 1 ;; esac
}

# get_checksums OUT VERSION MIRRORBASE: checksums.txt from github.com directly, so a mirror can't
# serve a tampered archive with a matching sum. Only when github.com is unreachable (a network
# error or timeout; an HTTP error is an answer) does the mirror's copy count, with a warning.
get_checksums() {
	direct="https://github.com/$REPO/releases/download/$2/checksums.txt"
	if [ -z "$MIRROR" ]; then
		fetch -fsSL --retry 3 -o "$1" "$direct" || die "cannot download checksums"
		return 0
	fi
	rc=0
	fetch -fsSL --max-time 15 -o "$1" "$direct" || rc=$?
	[ "$rc" = 0 ] && return 0
	[ "$rc" = 22 ] && die "cannot download checksums ($direct)"
	warn "github.com unreachable; checksums.txt came from the mirror: integrity depends on the mirror"
	fetch -fsSL --retry 3 -o "$1" "$3/checksums.txt" || die "cannot download checksums"
}

sha256() { if need sha256sum; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

latest_version() {
	# the releases/latest redirect avoids the API rate limit
	url=$(fetch -fsSLI -o /dev/null -w '%{url_effective}' "${MIRROR}https://github.com/$REPO/releases/latest") || return 1
	v="${url##*/}"
	valid_version "$v" || return 1
	echo "$v"
}

download() {
	install_deps
	[ -n "$SBP_VERSION" ] || SBP_VERSION="$(latest_version)" || die "cannot determine the latest release"
	valid_version "$SBP_VERSION" || die "bad version: $SBP_VERSION (want vX.Y.Z)"
	name="sbpanel_${SBP_VERSION}_linux_${ARCH}.tar.gz"
	base="${MIRROR}https://github.com/$REPO/releases/download/$SBP_VERSION"
	TMP="$(mktemp -d)"
	trap 'rm -rf "$TMP"' EXIT
	say "downloading sbpanel $SBP_VERSION ($ARCH)"
	fetch -fL --retry 3 -o "$TMP/$name" "$base/$name" || die "download failed: $base/$name"
	get_checksums "$TMP/checksums.txt" "$SBP_VERSION" "$base"
	want="$(grep " $name\$" "$TMP/checksums.txt" | cut -d' ' -f1)"
	[ -n "$want" ] && [ "$want" = "$(sha256 "$TMP/$name")" ] || die "checksum mismatch for $name"
	PKG="sbpanel_${SBP_VERSION}_linux_${ARCH}"
	# only the two binaries: nothing else in the archive is used, and nothing else gets written
	tar -xzf "$TMP/$name" -C "$TMP" "$PKG/sbmaster" "$PKG/sbnode" || die "cannot unpack $name"
	for b in sbmaster sbnode; do
		[ -f "$TMP/$PKG/$b" ] && [ ! -L "$TMP/$PKG/$b" ] || die "$name: $b is not a regular file"
	done
	PKG="$TMP/$PKG"
}

systemd_version() { systemctl --version 2>/dev/null | awk 'NR==1{print $2}'; }

# write_unit NAME DESCRIPTION EXEC DATADIR [ENVFILE]
write_unit() {
	name="$1"; desc="$2"; exec="$3"; data="$4"; envf="${5:-}"
	if [ "$INIT" = systemd ]; then
		sv="$(systemd_version)"; sv="${sv:-0}"
		if [ "$sv" -ge 232 ] 2>/dev/null; then
			hard="ProtectSystem=strict
ReadWritePaths=$data"
		else # CentOS 7 (systemd 219)
			hard="ProtectSystem=full
ReadWriteDirectories=$data"
		fi
		cat >/etc/systemd/system/$name.service <<EOF
[Unit]
Description=$desc
After=network-online.target
Wants=network-online.target

[Service]
${envf:+EnvironmentFile=-$envf}
ExecStart=$exec
Restart=always
RestartSec=3
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN CAP_NET_RAW
NoNewPrivileges=true
$hard

[Install]
WantedBy=multi-user.target
EOF
		systemctl daemon-reload
		systemctl enable "$name" >/dev/null 2>&1
		systemctl restart "$name"
	else
		cat >/etc/init.d/$name <<EOF
#!/sbin/openrc-run
description="$desc"
command="${exec%% *}"
command_args="${exec#* }"
command_background=true
pidfile="/run/$name.pid"
output_log="/var/log/$name.log"
error_log="/var/log/$name.log"
depend() { need net; }
${envf:+start_pre() { set -a; [ -f $envf ] && . $envf; set +a; }}
EOF
		chmod +x /etc/init.d/$name
		rc-update add "$name" default >/dev/null 2>&1
		rc-service "$name" restart
	fi
}

svc() { if [ "$INIT" = systemd ]; then systemctl "$1" "$2"; else rc-service "$2" "$1"; fi; }

# env_line NAME VALUE: one single-quoted assignment. The file is read by systemd and sourced by
# sh (OpenRC's start_pre, this script), so an unquoted value such as a password with $(...),
# ; or a space would run as a command as root.
env_line() {
	case "$2" in *"'"* | *'
'*) die "$1 must not contain a single quote or a newline" ;; esac
	printf "%s='%s'\n" "$1" "$2"
}

install_master() {
	download
	install -m 0755 "$PKG/sbmaster" "$BIN_DIR/sbmaster"
	install -d -m 0700 /var/lib/sbmaster
	if [ ! -f /etc/sbmaster.env ]; then
		umask 077
		{
			env_line SBM_WEB "$WEB"
			env_line SBM_GRPC "$GRPC"
			env_line SBM_PUBLIC_GRPC "$PUBLIC_GRPC"
			env_line SBM_TZ "$TZ_ARG"
			if [ -n "$ADMIN_PW" ]; then env_line SBM_ADMIN_PASSWORD "$ADMIN_PW"; fi
		} >"$TMP/sbmaster.env"
		install -m 0600 "$TMP/sbmaster.env" /etc/sbmaster.env
	fi
	write_unit sbmaster "sbpanel master" "$BIN_DIR/sbmaster --data /var/lib/sbmaster" /var/lib/sbmaster /etc/sbmaster.env
	sleep 2
	# shellcheck disable=SC1091
	w="$(. /etc/sbmaster.env 2>/dev/null; echo "${SBM_WEB:-$WEB}")"; g="$(. /etc/sbmaster.env 2>/dev/null; echo "${SBM_GRPC:-$GRPC}")"
	say "sbmaster $SBP_VERSION is running (web $w, node gRPC $g)"
	if [ -z "$ADMIN_PW" ] && [ "$INIT" = systemd ]; then
		pw="$(journalctl -u sbmaster --no-pager 2>/dev/null | grep -o 'with password [0-9a-f]*' | tail -1 | cut -d' ' -f3)"
		[ -n "$pw" ] && say "admin password: $pw  (change it after the first login)"
	fi
	echo "   Put the web address behind HTTPS (nginx/Caddy), open the gRPC port for nodes,"
	echo "   then add nodes in the panel. Logs: journalctl -u sbmaster -f"
}

install_node() {
	[ -n "$M_ADDR" ] && [ -n "$M_TOKEN" ] && [ -n "$M_FP" ] || [ -f /var/lib/sbnode/node_id ] ||
		die "node needs --master, --token and --ca-sha256 (copy the command from the panel)"
	download
	install -m 0755 "$PKG/sbnode" "$BIN_DIR/sbnode"
	install -d -m 0700 /var/lib/sbnode
	if [ -n "$M_TOKEN" ]; then
		"$BIN_DIR/sbnode" enroll --dir /var/lib/sbnode --master "$M_ADDR" --token "$M_TOKEN" --ca-sha256 "$M_FP"
	fi
	write_unit sbnode "sbpanel node (embedded sing-box)" "$BIN_DIR/sbnode run --dir /var/lib/sbnode" /var/lib/sbnode
	sleep 2
	say "sbnode $SBP_VERSION is running; it should show as online in the panel within a few seconds"
	"$BIN_DIR/sbnode" status --dir /var/lib/sbnode || true
}

upgrade() {
	t="${TARGET:-all}"
	download
	for n in sbmaster sbnode; do
		case "$t:$n" in all:* | master:sbmaster | node:sbnode) ;; *) continue ;; esac
		[ -x "$BIN_DIR/$n" ] || continue
		install -m 0755 "$PKG/$n" "$BIN_DIR/$n.new" && mv -f "$BIN_DIR/$n.new" "$BIN_DIR/$n"
		svc restart "$n"
		say "$n upgraded to $SBP_VERSION"
	done
}

uninstall() {
	t="${TARGET:-all}"
	for n in sbmaster sbnode; do
		case "$t:$n" in all:* | master:sbmaster | node:sbnode) ;; *) continue ;; esac
		if [ "$INIT" = systemd ]; then
			systemctl disable --now "$n" >/dev/null 2>&1 || true
			rm -f /etc/systemd/system/$n.service
			systemctl daemon-reload
		else
			rc-service "$n" stop >/dev/null 2>&1 || true
			rc-update del "$n" >/dev/null 2>&1 || true
			rm -f /etc/init.d/$n
		fi
		rm -f "$BIN_DIR/$n"
		if [ "$PURGE" = 1 ]; then
			rm -rf "/var/lib/$n"
			[ "$n" = sbmaster ] && rm -f /etc/sbmaster.env
			say "$n removed with data"
		else
			say "$n removed (data kept in /var/lib/$n; use --purge to delete)"
		fi
	done
}

status() {
	for n in sbmaster sbnode; do
		[ -x "$BIN_DIR/$n" ] || continue
		if [ "$INIT" = systemd ]; then systemctl --no-pager --lines=0 status "$n" || true; else rc-service "$n" status || true; fi
	done
	[ -x "$BIN_DIR/sbnode" ] && "$BIN_DIR/sbnode" status --dir /var/lib/sbnode || true
}

case "$CMD" in
master) install_master ;;
node) install_node ;;
upgrade) upgrade ;;
uninstall) uninstall ;;
status) status ;;
*)
	sed -n '2,17p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || true
	die "usage: install.sh master|node|upgrade|uninstall|status [options]"
	;;
esac
