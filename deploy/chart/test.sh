#!/usr/bin/env bash
# Helm chart checks: lint, render both modes, and assert the shape of the
# rendered manifests. Run from anywhere; CI runs this on every push.
set -euo pipefail
cd "$(dirname "$0")/oarlock"

KEY="0000000000000000000000000000000000000000000000000000000000000000"

echo "== helm lint"
helm lint . --set config.masterKey="$KEY"

fail() { echo "FAIL: $1" >&2; exit 1; }

echo "== template: simple mode (defaults + bundled postgres/valkey)"
simple=$(helm template smoke . --set config.masterKey="$KEY")
grep -q 'OARLOCK_MODE' <<<"$simple" || fail "simple: no OARLOCK_MODE"
grep -q 'value: "all"' <<<"$simple" || fail "simple: expected mode all"
grep -q 'kind: StatefulSet' <<<"$simple" || fail "simple: bundled postgres missing"
grep -q 'smoke-oarlock-valkey' <<<"$simple" || fail "simple: bundled valkey missing"
[ "$(grep -c 'value: "api"' <<<"$simple")" = 0 ] || fail "simple: unexpected api component"

echo "== template: scalable mode (external db, no bundles, ingress)"
scalable=$(helm template smoke . \
  --set mode=scalable \
  --set config.masterKey="$KEY" \
  --set postgres.enabled=false \
  --set valkey.enabled=false \
  --set config.databaseUrl=postgres://u:p@db:5432/oarlock \
  --set ingress.enabled=true \
  --set ingress.host=oarlock.example.com \
  --set replicas.api=3 --set replicas.worker=5)
grep -q 'value: "api"' <<<"$scalable" || fail "scalable: api component missing"
grep -q 'value: "worker"' <<<"$scalable" || fail "scalable: worker component missing"
grep -q 'replicas: 3' <<<"$scalable" || fail "scalable: api replicas not applied"
grep -q 'replicas: 5' <<<"$scalable" || fail "scalable: worker replicas not applied"
grep -q 'kind: Ingress' <<<"$scalable" || fail "scalable: ingress missing"
grep -q 'app.kubernetes.io/component: api' <<<"$scalable" || fail "scalable: service must target api"
! grep -q 'kind: StatefulSet' <<<"$scalable" || fail "scalable: bundled postgres should be off"
grep -q 'name: DATABASE_URL' <<<"$scalable" || fail "scalable: DATABASE_URL env missing"

echo "== template: existingSecret renders no chart Secret"
ext=$(helm template smoke . --set existingSecret=my-secret --set postgres.enabled=false)
! grep -q 'kind: Secret' <<<"$ext" || fail "existingSecret: chart Secret should not render"
grep -q 'name: my-secret' <<<"$ext" || fail "existingSecret: env should reference my-secret"

echo "== template: guardrails fail without required values"
if helm template smoke . >/dev/null 2>&1; then
  fail "template without masterKey should fail"
fi
if helm template smoke . --set config.masterKey="$KEY" --set postgres.enabled=false >/dev/null 2>&1; then
  fail "template without databaseUrl (external db) should fail"
fi
if helm template smoke . --set config.masterKey="$KEY" --set mode=bogus >/dev/null 2>&1; then
  fail "template with invalid mode should fail"
fi

echo "== kubectl client-side validation"
# kubectl's client dry-run still fetches OpenAPI schemas from a cluster, so
# this only runs where one is reachable (dev machines) — CI has none.
if command -v kubectl >/dev/null && kubectl version --request-timeout=3s >/dev/null 2>&1; then
  kubectl apply --dry-run=client -f - <<<"$simple" >/dev/null
  kubectl apply --dry-run=client -f - <<<"$scalable" >/dev/null
  echo "   ok"
else
  echo "   no kubectl or no reachable cluster; skipped"
fi

echo "ALL CHART CHECKS PASSED"
