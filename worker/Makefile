.PHONY: help sync run lint fmt fmt-check type test test-int clean

UV ?= uv

## Show available targets
help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Install deps (locked) including dev extras
sync: ## Install dependencies from uv.lock (with dev extras)
	$(UV) sync --extra dev

## Run the worker process (`uv run worker`)
run: ## Start the worker process
	$(UV) run worker

## Lint via ruff
lint: ## Run ruff check
	$(UV) run ruff check .
	$(UV) run ruff format --check .

## Format via ruff
fmt: ## Auto-format with ruff
	$(UV) run ruff format .
	$(UV) run ruff check --fix .

## Strict mypy type-check on worker/
type: ## Run mypy --strict over worker/
	$(UV) run mypy --strict worker/

## Run unit tests (skips @pytest.mark.integration)
test: ## Run unit tests
	$(UV) run pytest -x -q

## Run integration tests (testcontainers; needs Docker)
test-int: ## Run integration tests (requires Docker)
	$(UV) run pytest -x -q -m integration

## Remove caches and build artefacts
clean: ## Remove caches
	rm -rf .pytest_cache .mypy_cache .ruff_cache build dist
