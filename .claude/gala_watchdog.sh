#!/usr/bin/env bash
# Kill any gala.exe whose RSS exceeds 4 GB.
# Loops once every 2 seconds and stops after $1 seconds (default 600).
set -u
deadline=$((SECONDS + ${1:-600}))
while [ $SECONDS -lt $deadline ]; do
    # tasklist CSV embeds commas inside the KB column ("20,431,264 K") so
    # awk -F, splits the wrong way. Use the table format instead and rely
    # on column positions; strip commas from the KB digit-run.
    tasklist //FI "IMAGENAME eq gala.exe" //NH 2>/dev/null \
      | awk '/^gala.exe/ {
            pid=$2;
            kb=$(NF-1);
            gsub(/[^0-9]/,"",kb);
            if (kb+0 > 0) print pid, kb;
        }' \
      | while read -r pid kb; do
            if [ "${kb:-0}" -gt 4194304 ]; then
                echo "$(date +%H:%M:%S) WATCHDOG: killing gala.exe pid=$pid mem=${kb}KB" >&2
                taskkill //F //PID "$pid" 2>&1
            else
                echo "$(date +%H:%M:%S) gala.exe pid=$pid mem=${kb}KB" >&2
            fi
        done
    sleep 2
done
echo "WATCHDOG: stopped after ${1:-600}s" >&2
