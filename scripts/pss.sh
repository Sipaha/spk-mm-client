#!/usr/bin/env bash
# Sums PSS (MB) of a process and all its descendants (Linux).
# Usage: scripts/pss.sh <pid>
set -euo pipefail
root="${1:?pid}"
pids="$root"
frontier="$root"
while [ -n "$frontier" ]; do
  next=""
  for p in $frontier; do
    kids=$(cat /proc/"$p"/task/*/children 2>/dev/null || true)
    next="$next $kids"
  done
  frontier=$(echo $next)
  pids="$pids $frontier"
done
total=0
for p in $pids; do
  kb=$(LC_ALL=C awk '/^Pss:/{print $2}' /proc/"$p"/smaps_rollup 2>/dev/null || echo 0)
  mb=$(LC_ALL=C awk -v kb="$kb" 'BEGIN{printf "%.1f", kb/1024}')
  printf "%8s MB  %s %s\n" "$mb" "$p" "$(tr '\0' ' ' < /proc/"$p"/cmdline | cut -c1-80)"
  total=$((total + kb))
done
total_mb=$(LC_ALL=C awk -v kb="$total" 'BEGIN{printf "%.1f", kb/1024}')
printf "TOTAL PSS: %s MB\n" "$total_mb"
