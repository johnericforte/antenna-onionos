#!/bin/sh
# OnionOS entry point. Keep this thin: it sets up the environment, runs the
# binary, and always cleans up after itself.

appdir=/mnt/SDCARD/App/Antenna
logfile="$appdir/antenna.log"

# The log lives on a removable FAT card, so mode bits are not a real
# confidentiality boundary -- but create files conservatively anyway and cap
# the log so it cannot grow without bound.
umask 077
if [ -f "$logfile" ]; then
    size=$(wc -c < "$logfile" 2>/dev/null)
    case "$size" in
        ''|*[!0-9]*) ;;
        *) [ "$size" -gt 262144 ] && mv -f "$logfile" "$logfile.1" ;;
    esac
fi

cleanup() {
    status=$?
    trap - 0 1 2 15
    rm -f /tmp/stay_awake
    exit "$status"
}
trap cleanup 0
trap 'exit 130' 1 2 15

# Stop Onion from sleeping while the user is browsing.
touch /tmp/stay_awake

# Tracing. Off for a release: the log then carries failures only, which is what
# a user has to send when something goes wrong. Set this to 1 to record every
# step instead, including the ffplay command line and whatever ffplay printed.
# A debug file next to this script turns it on without editing anything.
ANTENNA_DEBUG=0
[ -f "$appdir/debug" ] && ANTENNA_DEBUG=1
export ANTENNA_DEBUG

# archive.org forces TLS on every path, so all three of these matter.
#
# Some Miyoo images ship without a usable system CA store, so carry our own.
if [ -f "$appdir/cacert.pem" ]; then
    SSL_CERT_FILE="$appdir/cacert.pem"
    export SSL_CERT_FILE
else
    echo "warning: $appdir/cacert.pem is missing, TLS will fail" >&2
fi

# The Miyoo kernel rejects Go's post-quantum ClientHello, which fails the
# handshake before any certificate is even looked at.
GODEBUG=tlsmlkem=0,tlssecpmlkem=0
export GODEBUG

# Certificate validity is checked against the clock, and this device has no
# battery-backed RTC. A clock stuck in the past fails every handshake, and the
# error it produces reads like a network fault, so say so plainly.
year=$(date +%Y 2>/dev/null)
case "$year" in
    ''|*[!0-9]*) ;;
    *) [ "$year" -lt 2024 ] && echo "warning: clock reads $year, TLS will fail until it is set" >&2 ;;
esac

chmod 0755 "$appdir/antenna" 2>/dev/null
"$appdir/antenna" >> "$logfile" 2>&1
