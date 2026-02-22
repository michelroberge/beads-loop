#!/bin/sh
pids=$(pgrep -x beads-loop 2>/dev/null)
if [ -z "$pids" ]; then
  echo "no beads-loop processes found"
  exit 0
fi
echo "killing: $pids"
kill $pids
echo "done"
