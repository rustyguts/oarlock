.PHONY: up down logs build test screenshots screenshots-check

# Pinned to the installed @playwright/test version (web/package.json).
PLAYWRIGHT_IMAGE := mcr.microsoft.com/playwright:v1.61.0-noble
# Run the visual-regression suite inside the pinned Playwright image so renders
# are identical on every host (Linux, macOS, CI). $(1) is the bun script to run.
define run-playwright
	docker run --rm \
	  -e HOST_UID=$$(id -u) -e HOST_GID=$$(id -g) \
	  -v "$(CURDIR)":/repo -v /repo/web/node_modules \
	  -w /repo/web $(PLAYWRIGHT_IMAGE) \
	  bash -lc 'apt-get update -qq >/dev/null && apt-get install -y -qq unzip >/dev/null \
	    && curl -fsSL https://bun.sh/install | bash >/dev/null \
	    && export PATH=$$HOME/.bun/bin:$$PATH \
	    && bun install --frozen-lockfile \
	    && { bun run $(1); ec=$$?; chown -R $$HOST_UID:$$HOST_GID tests/__snapshots__ test-results 2>/dev/null || true; exit $$ec; }'
endef

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api

build:
	cd engine && go build ./...
	cd web && bun run build

test:
	cd engine && go test ./...

# Regenerate the Linux snapshot baselines (use on any host, incl. macOS).
screenshots:
	$(call run-playwright,test:ui:update)

# Verify the UI against committed baselines in the same image CI uses.
screenshots-check:
	$(call run-playwright,test:ui)
