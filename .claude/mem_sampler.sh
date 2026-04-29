#!/usr/bin/env bash
# Sample gala.exe memory every 0.5s, log timestamp + RSS in MB.
set -u
deadline=$((SECONDS + ${1:-300}))
while [ $SECONDS -lt $deadline ]; do
    tasklist //FI "IMAGENAME eq gala.exe" //NH 2>/dev/null \
      | awk '/^gala.exe/ {
            pid=$2; kb=$(NF-1);
            gsub(/[^0-9]/,"",kb);
            if (kb+0 > 0) printf "%s pid=%s mem=%dMB\n", strftime("%H:%M:%S"), pid, kb/1024
        }'
    sleep 0.5
done
