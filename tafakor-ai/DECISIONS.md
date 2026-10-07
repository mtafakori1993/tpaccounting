# Decisions

Each entry: decision — alternatives — reason.

D1. **Reverse-proxy route lives in the Go server** (`/ai-api/*` → `AI_SERVICE_URL`), enabled only when `AI_SERVICE_URL` is set.
Alternatives: add nginx/Caddy in front. Reason: the repo has no reverse proxy; adding one would change deployment for every user.

D2. **Disabled = harmless.** When `AI_SERVICE_URL` is empty the core answers `/ai-api/loader.js` with an empty script (200)
and every other `/ai-api/*` with 404. Alternative: no route at all (the `<script>` tag would 404 in the console).
Reason: the main app must behave exactly as before, without console errors.

D3. **Storage: SQLite via `modernc.org/sqlite`** (pure Go, CGO off, WAL) behind `store.Repository`. Alternative: mattn/go-sqlite3
(needs CGO). Reason: static binary, same build style as the core.

D4. **The AI service has its own Go module** (`tafakor-ai/service/go.mod`) and never imports core packages.
The Jalali algorithm is re-implemented (small, tested) rather than imported. Reason: removability / isolation.

D5. **Session validation on every request** via `GET /api/v1/auth/me` with the forwarded `tp_session` cookie; no cache.
Alternative: short TTL cache. Reason: spec says every request; the call is cheap and keeps revocation immediate.

D6. **CSRF is checked by the AI service too** (double submit: `tp_csrf` cookie must equal `X-CSRF-Token`) on every
state-changing `/ai-api` request, and the same token is forwarded to the accounting API.

D7. **Proxy scope.** One `http.Transport` per external provider with `Proxy` set to that provider's `*_PROXY_URL`;
the accounting client uses a transport with `Proxy: nil` (explicit bypass, ignores `HTTP(S)_PROXY` env). An extra
`AI_NO_PROXY` list is honoured for provider URLs that are internal. No silent fallback to a direct route.

D8. **Docker networking.** The AI container joins the core compose network (`tpaccounting_internal`, configurable) so the
core reaches it as `http://tafakor-ai:8090` and it reaches the core as `http://app:8085`; the host proxy is reached as
`host.docker.internal:8118` via `extra_hosts: host-gateway`.

D9. **Foreign currency.** Accounting documents have no currency field (base currency only). For a foreign-currency
receipt the assistant always asks for the FX rate (human approval forced), books the converted base amount and writes
the original amount/currency/rate into the description. Alternative: refuse FX. Reason: matches the app's model.

D10. **OpenRouter key handling.** Provided at runtime by the user; stored only in the gitignored `tafakor-ai/.env`,
never in code, docs, logs or commits.

D11. **Host proxy refuses Docker subnets (found during testing).** The proxy on host port 8118 answers
`403 Forbidden` to CONNECT requests coming from Docker bridge networks (172.x) and works from 127.0.0.1, i.e. it
has a source-address ACL. The compose file still targets `host.docker.internal:8118` as required; the operator must
allow the Docker subnet in the proxy (e.g. privoxy `permit-access 172.16.0.0/12`) — listed under "Needs human
verification". The agent did not change the host proxy. Live model tests were run with the service binary on the host
(`test/run-host-ai.sh`, bound to the docker0 address only), which really goes through 127.0.0.1:8118.
Alternatives: `network_mode: host` for the container, or a TCP relay. Rejected: they would change production
networking or work around the proxy's access control.

D12. **GOPROXY build argument.** proxy.golang.org returns 403 for some modules from this network; the AI image takes
`GOPROXY` as a build arg (default proxy.golang.org) so operators can use a mirror (tests used goproxy.cn).
