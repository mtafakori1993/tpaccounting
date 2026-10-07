#!/bin/sh
# Manage the throwaway Tafakor AI test stack (project "tpai-test" only).
set -eu
cd "$(dirname "$0")"
C="docker compose -p tpai-test -f docker-compose.test.yml --profile ai"
case "${1:-up}" in
  build) $C build ;;
  up)    $C up -d --build --wait ;;
  down)  $C down ;;
  # Deletes ONLY the tpai-test volumes created by this script.
  reset) $C down -v --remove-orphans ;;
  logs)  $C logs --tail=200 ;;
  *) echo "usage: $0 build|up|down|reset|logs"; exit 2 ;;
esac
