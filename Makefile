.PHONY: up down logs build image test chart-test

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f oarlock

build:
	cd engine && go build ./...
	cd web && bun run build

image:
	docker build -t oarlock:dev .

test:
	cd engine && go test ./...

chart-test:
	./deploy/chart/test.sh
