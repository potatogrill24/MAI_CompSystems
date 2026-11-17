.PHONY: test build up down smoke recovery vet
build:
	go build -o bin/server ./cmd/server
test:
	go test -race ./...
vet:
	go vet ./...
up:
	docker compose up --build -d --wait
down:
	docker compose down
smoke:
	python3 scripts/smoke.py
recovery:
	python3 scripts/recovery.py
