#!/bin/sh
# Runs the AI service ON THE HOST (not in Docker) for live tests through the host
# proxy on 127.0.0.1:8118 (that proxy refuses Docker subnets, see DECISIONS D11).
# It binds to the docker0 address only, so the tpai-test app container can reach it:
#   TEST_AI_SERVICE_URL=http://host.docker.internal:18190 ./stack.sh up   (app only)
set -eu
cd "$(dirname "$0")/.."
mkdir -p test-artifacts/hostai
(cd widget && npm run build >/dev/null)
(cd service && go build -o ../test-artifacts/hostai/aiservice ./cmd/aiservice)
set -a; [ -f .env ] && . ./.env; set +a
export AI_BIND="${AI_BIND:-172.17.0.1}" AI_PORT="${AI_PORT:-18190}" \
  AI_ACCOUNTING_URL="${AI_ACCOUNTING_URL_HOST:-http://127.0.0.1:18085}" \
  OPENROUTER_PROXY_URL=http://127.0.0.1:8118 SPEECH_PROXY_URL=http://127.0.0.1:8118 \
  AI_DATA_DIR="$PWD/test-artifacts/hostai/data" AI_WIDGET_DIR="$PWD/widget/dist" AI_KNOWLEDGE_DIR="$PWD/knowledge" \
  AI_RATE_LIMIT_PER_MINUTE=600
exec test-artifacts/hostai/aiservice
