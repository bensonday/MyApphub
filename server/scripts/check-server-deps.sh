#!/bin/sh
set -eu

go_bin="${GO:-go}"
dependencies="$($go_bin list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./cmd/apphub-server)"
unexpected="$(printf '%s\n' "$dependencies" | awk 'NF && $0 !~ /^myapphub\/server($|\/)/')"

if [ -n "$unexpected" ]; then
  echo "apphub-server contains non-standard dependencies:" >&2
  echo "$unexpected" >&2
  exit 1
fi

web_references="$(grep -R -n -E 'APPHUB_WEB_DIR|\.\./web|web/dist' cmd internal Makefile .env.example 2>/dev/null || true)"
if [ -n "$web_references" ]; then
  echo "apphub-server contains a forbidden Web project reference:" >&2
  echo "$web_references" >&2
  exit 1
fi

echo "apphub-server dependency boundary: standard library + project packages only; no Web project references"
