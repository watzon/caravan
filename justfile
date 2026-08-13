# Build the ignored SPA before any Go command that embeds it.
web-build:
    cd web && if [ ! -d node_modules ]; then npm ci; fi
    cd web && npm run build

# Install frontend deps if needed, without a production build.
web-install:
    cd web && if [ ! -d node_modules ]; then npm ci; fi

# Ensure web/dist exists for go:embed without a production rebuild every start.
ensure-web-dist:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f web/dist/index.html ]; then
        just web-build
    fi

# Vite HMR on :5173, proxying /api to the Go server.
web-dev: web-install
    cd web && npm run dev -- --host 127.0.0.1 --port 5173 --strictPort --clearScreen false

# Air live-reload for the Go API; proxies the SPA to Vite for HMR.
go-dev: ensure-web-dist
    #!/usr/bin/env bash
    set -euo pipefail
    PATH="$(go env GOPATH)/bin:${PATH}"
    export PATH
    export CARAVAN_DEV_UI="${CARAVAN_DEV_UI:-http://127.0.0.1:5173}"
    if ! command -v air >/dev/null 2>&1; then
        echo "air is required for just go-dev / just dev. Install it with:" >&2
        echo "  go install github.com/air-verse/air@latest" >&2
        exit 1
    fi
    air -c .air.toml

# Frontend HMR + backend live-reload. Open http://127.0.0.1:8677
dev: web-install ensure-web-dist
    #!/usr/bin/env bash
    set -euo pipefail
    PATH="$(go env GOPATH)/bin:${PATH}"
    export PATH
    export CARAVAN_DEV_UI="${CARAVAN_DEV_UI:-http://127.0.0.1:5173}"

    if ! command -v air >/dev/null 2>&1; then
        echo "air is required for just dev. Install it with:" >&2
        echo "  go install github.com/air-verse/air@latest" >&2
        exit 1
    fi

    echo
    echo "  Caravan dev"
    echo "    UI (HMR)  http://127.0.0.1:8677"
    echo "    Vite      http://127.0.0.1:5173"
    echo "    API       http://127.0.0.1:8677/api/v1"
    echo

    (cd web && npm run dev -- --host 127.0.0.1 --port 5173 --strictPort --clearScreen false) &
    vite_pid=$!
    air -c .air.toml &
    air_pid=$!

    cleanup() {
        trap - INT TERM EXIT
        kill "${vite_pid}" "${air_pid}" 2>/dev/null || true
        wait "${vite_pid}" 2>/dev/null || true
        wait "${air_pid}" 2>/dev/null || true
    }
    trap cleanup INT TERM EXIT

    # bash 3.2 (macOS /bin/bash) has no wait -n.
    while kill -0 "${vite_pid}" 2>/dev/null && kill -0 "${air_pid}" 2>/dev/null; do
        sleep 1
    done
    exit 1
