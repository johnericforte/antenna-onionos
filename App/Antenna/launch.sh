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

# Milestone 2 note: once networking lands, this script must also export
#   SSL_CERT_FILE="$appdir/cacert.pem"
#   GODEBUG=tlsmlkem=0,tlssecpmlkem=0
# OnionOS ships without a usable system CA store on some Miyoo images, and the
# Miyoo kernel rejects Go's post-quantum TLS ClientHello.

chmod 0755 "$appdir/antenna" 2>/dev/null
"$appdir/antenna" >> "$logfile" 2>&1
