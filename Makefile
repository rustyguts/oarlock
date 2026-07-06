.PHONY: up down logs build test

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
