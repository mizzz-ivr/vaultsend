APP_NAME := vaultsend-api
DB_URL ?= postgres://vaultsend:vaultsend@localhost:5432/vaultsend?sslmode=disable

.PHONY: run run-worker run-cleanup-worker run-audit-worker web-install web-run web-lint web-typecheck web-build web-e2e test test-integration lint migrate-up migrate-down verify-migrations sqlc-generate container-build verify-operations verify-supply-chain verify-release-image check-operations-deploy deploy-operations

run:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

run-cleanup-worker:
	go run ./cmd/cleanup-worker

run-audit-worker:
	go run ./cmd/audit-worker

web-install:
	cd web && npm ci

web-run:
	cd web && npm run dev

web-lint:
	cd web && npm run lint

web-typecheck:
	cd web && npm run typecheck

web-build:
	cd web && npm run build

web-e2e:
	cd web && npm run e2e

test:
	go test ./...

test-integration:
	DATABASE_URL="$(DB_URL)" go test -tags=integration -count=1 -v ./internal/store

lint:
	go vet ./...

migrate-up:
	migrate -path db/migrations -database "$(DB_URL)" up

migrate-down:
	migrate -path db/migrations -database "$(DB_URL)" down 1

verify-migrations:
	DATABASE_URL="$(DB_URL)" bash scripts/verify-migrations.sh

sqlc-generate:
	sqlc generate

container-build:
	docker build --target runtime -t vaultsend:local .

verify-operations:
	bash scripts/verify-operations-config.sh

verify-supply-chain:
	bash scripts/verify-supply-chain.sh

verify-release-image:
	@test -n "$(VAULTSEND_IMAGE)" || (echo "VAULTSEND_IMAGEにdigest固定イメージを指定してください" >&2; exit 1)
	bash scripts/verify-release-image.sh "$(VAULTSEND_IMAGE)"

check-operations-deploy:
	@test -n "$(VAULTSEND_IMAGE)" || (echo "VAULTSEND_IMAGEにdigest固定イメージを指定してください" >&2; exit 1)
	bash scripts/deploy-verified-compose.sh --check "$(VAULTSEND_IMAGE)"

deploy-operations:
	@test -n "$(VAULTSEND_IMAGE)" || (echo "VAULTSEND_IMAGEにdigest固定イメージを指定してください" >&2; exit 1)
	bash scripts/deploy-verified-compose.sh --deploy "$(VAULTSEND_IMAGE)"
