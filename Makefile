.PHONY: build dev lint migrations-check test typecheck verify

build:
	pnpm build
	go build ./services/api/cmd/api ./services/api/cmd/migrate ./services/worker/cmd/worker

dev:
	pnpm dev

lint:
	pnpm lint

migrations-check:
	pnpm migrations:check

test:
	pnpm test

typecheck:
	pnpm typecheck

verify: lint typecheck test migrations-check build
