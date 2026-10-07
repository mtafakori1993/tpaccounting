#!/bin/sh
# Removes the Tafakor AI touchpoints from the core application (see CHANGES_TO_CORE.md).
# Run from anywhere; it edits files in the repository this script lives in.
#   tafakor-ai/remove-touchpoints.sh          # remove touchpoints only (tags are harmless and kept)
#   tafakor-ai/remove-touchpoints.sh --tags   # also remove the data-ai-* tagging edits
# Afterwards delete the add-on itself:  rm -rf tafakor-ai
# and its data:  docker compose -f tafakor-ai/docker-compose.ai.yml down -v   (before deleting the folder)
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
python3 - "$@" <<'PY'
import os, re, sys, subprocess
root = os.getcwd()

def edit(path, pairs):
    p = os.path.join(root, path)
    s = open(p, encoding="utf-8").read()
    for old, new in pairs:
        if old in s:
            s = s.replace(old, new)
    open(p, "w", encoding="utf-8").write(s)

# (b) reverse proxy route + (c) header change
for f in ("backend/internal/server/aiproxy.go", "backend/internal/server/aiproxy_test.go"):
    if os.path.exists(f):
        os.remove(f)
edit("backend/internal/server/server.go", [
    ('\tr.Handle("/ai-api/*", aiProxy(cfg.AIServiceURL)) // Tafakor AI touchpoint (b)\n', ""),
    ("securityHeaders(cfg.CookieSecure, cfg.AIServiceURL != \"\")", "securityHeaders(cfg.CookieSecure)"),
])
edit("backend/internal/server/middleware.go", [
    ('''func securityHeaders(hsts, ai bool) func(http.Handler) http.Handler {
	// Tafakor AI touchpoint (c): push-to-talk needs the microphone on this origin.
	permissions := "camera=(), microphone=(), geolocation=()"
	if ai {
		permissions = "camera=(), microphone=(self), geolocation=()"
	}''', "func securityHeaders(hsts bool) func(http.Handler) http.Handler {"),
    ('h.Set("Permissions-Policy", permissions)', 'h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")'),
])
edit("backend/internal/platform/config/config.go", [
    ("\tAIServiceURL   string // Tafakor AI add-on; empty disables the /ai-api route\n", ""),
    ('\t\tAIServiceURL:   strings.TrimSpace(os.Getenv("AI_SERVICE_URL")),\n', ""),
])
edit("docker-compose.yml", [
    ("      # Tafakor AI add-on (optional): e.g. http://tafakor-ai:8090; empty = disabled.\n      AI_SERVICE_URL: ${AI_SERVICE_URL:-}\n", ""),
])
# (a) loader script
edit("frontend/index.html", [
    ('    <!-- Tafakor AI touchpoint (a): empty unless AI_SERVICE_URL is set on the server -->\n    <script src="/ai-api/loader.js" defer></script>\n', ""),
])

if "--tags" in sys.argv:
    # Tagging edits are marked with "Tafakor AI tag" comments / data-ai attributes; the
    # recorded patch is reverted with git if present.
    patch = os.path.join(root, "tafakor-ai", "tagging.patch")
    if os.path.exists(patch):
        subprocess.check_call(["git", "apply", "-R", patch])
    else:
        print("tagging.patch not found; tags left in place (they are harmless)")
print("Tafakor AI touchpoints removed.")
PY
