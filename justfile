# Build the ignored SPA before any Go command that embeds it.
web-build:
    cd web && if [ ! -d node_modules ]; then npm ci; fi
    cd web && npm run build

# Start the Caravan server with Air live reload.
dev: web-build
    air -c .air.toml
