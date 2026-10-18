#!/bin/sh
# BusyBox service entrypoint for the observed JetKVM /userdata/init.d boot hook.
set -eu
ROOT=/userdata/jetkvm-status-agent
STATE=/tmp/jetkvm-status-agent

running() {
    [ -f "$STATE/supervisor.pid" ] || return 1
    service_pid=$(cat "$STATE/supervisor.pid" 2>/dev/null) || return 1
    case "$service_pid" in ''|*[!0-9]*) return 1;; esac
    [ -r "/proc/$service_pid/cmdline" ] || return 1
    tr '\000' ' ' < "/proc/$service_pid/cmdline" | grep -q "$ROOT/service.sh run"
}

case "${1:-}" in
    start)
        if running; then exit 0; fi
        mkdir -p "$STATE"
        chmod 700 "$STATE"
        setsid sh "$ROOT/service.sh" run </dev/null >/dev/null 2>&1 &
        ;;
    run)
        if ! mkdir "$STATE/lock" 2>/dev/null; then exit 1; fi
        echo $$ > "$STATE/supervisor.pid"
        stopping=0
        child=
        trap 'stopping=1; if [ -n "$child" ]; then kill "$child" 2>/dev/null || :; fi' INT TERM
        trap 'rm -f "$STATE/supervisor.pid" "$STATE/agent.pid"; rmdir "$STATE/lock" 2>/dev/null || :' EXIT
        set -a
        . "$ROOT/agent.env"
        set +a
        while [ "$stopping" = 0 ]; do
            # RAM-only log, replaced on every child restart. No per-message logs.
            "$ROOT/jetkvm-status-agent" -config "$ROOT/config.json" > "$STATE/last.log" 2>&1 &
            child=$!
            echo "$child" > "$STATE/agent.pid"
            wait "$child" || :
            if [ "$stopping" = 1 ]; then wait "$child" 2>/dev/null || :; break; fi
            sleep 3 &
            child=$!
            wait "$child" || :
        done
        ;;
    stop)
        if running; then
            kill "$service_pid"
            count=0
            while running && [ "$count" -lt 15 ]; do sleep 1; count=$((count+1)); done
            if running; then echo 'Service did not stop' >&2; exit 1; fi
        fi
        ;;
    restart)
        sh "$0" stop
        sh "$0" start
        ;;
    status)
        if running; then echo running; else echo stopped; exit 1; fi
        ;;
    *) echo "Usage: $0 {start|stop|restart|status}" >&2; exit 1;;
esac
