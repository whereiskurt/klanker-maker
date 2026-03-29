# Sidecar build and deployment pipeline
# Targets:
#   sidecars       — cross-compile Go sidecars + upload binaries to S3
#   ecr-push       — build Docker images for all sidecars and push to ECR
#   ecr-login      — authenticate Docker to ECR
#   ecr-repos      — ensure ECR repositories exist
#   build-sidecars — cross-compile only (no S3 upload, for local testing)

GOOS        := linux
GOARCH      := amd64
CGO_ENABLED := 0

# Semantic version — read without bumping; bump happens in the bump-version target
KM_VERSION  := $(shell cat VERSION 2>/dev/null | tr -d '[:space:]')
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS      = -X github.com/whereiskurt/klankrmkr/pkg/version.Number=v$(KM_VERSION) \
               -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=$(GIT_COMMIT)

VERSION     ?= latest
REGION      ?= $(shell aws configure get region)
ACCOUNT_ID   = $(shell aws sts get-caller-identity --query Account --output text)
ECR_REGISTRY = $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com

# KM_ARTIFACTS_BUCKET must be set in the environment — no default
KM_ARTIFACTS_BUCKET ?=

SIDECARS := dns-proxy http-proxy audit-log

OTELCOL_VERSION ?= 0.120.0
OTELCOL_URL     := https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v$(OTELCOL_VERSION)/otelcol-contrib_$(OTELCOL_VERSION)_$(GOOS)_$(GOARCH).tar.gz

.PHONY: build build-km bump-version sidecars ecr-push ecr-login ecr-repos build-sidecars build-lambdas build-create-handler build-email-create-handler push-create-handler clean fetch-otelcol

## bump-version: increment the patch version in VERSION file
bump-version:
	@./scripts/bump-version.sh VERSION > /dev/null

## build: bump version then build the km CLI binary
build: bump-version
	$(eval KM_VERSION := $(shell cat VERSION | tr -d '[:space:]'))
	go build -ldflags '-X github.com/whereiskurt/klankrmkr/pkg/version.Number=v$(KM_VERSION) -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=$(GIT_COMMIT)' -o km ./cmd/km/
	@echo "Built: km v$(KM_VERSION) ($(GIT_COMMIT))"

## build-km: alias for build
build-km: build

## fetch-otelcol: download otelcol-contrib binary for EC2 tracing sidecar
fetch-otelcol:
	@mkdir -p build
	@if [ ! -f build/otelcol-contrib ]; then \
	  echo "Downloading otelcol-contrib v$(OTELCOL_VERSION)..."; \
	  curl -sL "$(OTELCOL_URL)" | tar xz -C build otelcol-contrib; \
	  chmod +x build/otelcol-contrib; \
	else \
	  echo "build/otelcol-contrib already exists (skip download)"; \
	fi

## sidecars: cross-compile Go sidecars + fetch otelcol-contrib + upload all to S3
sidecars: fetch-otelcol
	@if [ -z "$(KM_ARTIFACTS_BUCKET)" ]; then \
	  echo "ERROR: KM_ARTIFACTS_BUCKET is not set. Export it before running make sidecars."; \
	  exit 1; \
	fi
	@mkdir -p build
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/dns-proxy ./sidecars/dns-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/http-proxy ./sidecars/http-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/audit-log ./sidecars/audit-log/cmd/
	aws s3 cp build/dns-proxy  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy
	aws s3 cp build/http-proxy s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy
	aws s3 cp build/audit-log  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log
	aws s3 cp build/otelcol-contrib s3://$(KM_ARTIFACTS_BUCKET)/sidecars/otelcol-contrib
	aws s3 cp sidecars/tracing/config.yaml s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml
	@echo ""
	@echo "Sidecar artifacts uploaded to s3://$(KM_ARTIFACTS_BUCKET)/sidecars/"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/otelcol-contrib"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml"

## build-sidecars: cross-compile Go sidecars locally (no S3 upload)
build-sidecars:
	@mkdir -p build
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/dns-proxy ./sidecars/dns-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/http-proxy ./sidecars/http-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/audit-log ./sidecars/audit-log/cmd/

## ecr-login: authenticate Docker daemon to ECR
ecr-login:
	aws ecr get-login-password --region $(REGION) | \
	  docker login --username AWS --password-stdin $(ECR_REGISTRY)

## ecr-repos: ensure all sidecar and Lambda container ECR repositories exist
ecr-repos:
	@for name in km-dns-proxy km-http-proxy km-audit-log km-tracing km-create-handler; do \
	  aws ecr describe-repositories --region $(REGION) --repository-names $$name 2>/dev/null || \
	  aws ecr create-repository --region $(REGION) --repository-name $$name; \
	done

## clean: remove all build artifacts
clean:
	rm -f build/*.zip build/dns-proxy build/http-proxy build/audit-log build/otelcol-contrib build/bootstrap
	@echo "Build artifacts cleaned."

## build-lambdas: cross-compile Go Lambda binaries for arm64 and package as deployment zips
build-lambdas: clean bump-version
	@mkdir -p build
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/ttl-handler/
	cd build && zip -j ttl-handler.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/budget-enforcer/
	cd build && zip -j budget-enforcer.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/github-token-refresher/
	cd build && zip -j github-token-refresher.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap-email-create ./cmd/email-create-handler/
	cd build && cp bootstrap-email-create bootstrap && zip -j email-create-handler.zip bootstrap && rm bootstrap
	@echo ""
	@echo "Lambda deployment packages built:"
	@echo "  build/ttl-handler.zip"
	@echo "  build/budget-enforcer.zip"
	@echo "  build/github-token-refresher.zip"
	@echo "  build/email-create-handler.zip"

## build-create-handler: compile Go binaries needed for the create-handler container image
## Run this before `make push-create-handler`
build-create-handler:
	@mkdir -p build
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/km-create-handler ./cmd/create-handler/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/km ./cmd/km/
	@echo ""
	@echo "Create-handler binaries built:"
	@echo "  build/km-create-handler  (Lambda entry point)"
	@echo "  build/km                 (subprocess binary bundled in container)"

## build-email-create-handler: compile and zip the email-create-handler Lambda binary
build-email-create-handler:
	@mkdir -p build
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap-email-create ./cmd/email-create-handler/
	cd build && cp bootstrap-email-create bootstrap && zip -j email-create-handler.zip bootstrap && rm bootstrap
	@echo ""
	@echo "Email-create-handler package built:"
	@echo "  build/email-create-handler.zip"

## push-create-handler: build and push the create-handler container image to ECR
## Requires: make build-create-handler first, and ECR authentication (make ecr-login)
push-create-handler: ecr-login
	docker buildx build --platform linux/arm64 \
	  --file cmd/create-handler/Dockerfile \
	  --tag $(ECR_REGISTRY)/km-create-handler:$(VERSION) \
	  --push .

## ecr-push: build and push Docker images for all 4 sidecars to ECR
ecr-push: ecr-login ecr-repos
	docker buildx build --platform linux/amd64 \
	  --file sidecars/dns-proxy/Dockerfile \
	  --tag $(ECR_REGISTRY)/km-dns-proxy:$(VERSION) \
	  --push .
	docker buildx build --platform linux/amd64 \
	  --file sidecars/http-proxy/Dockerfile \
	  --tag $(ECR_REGISTRY)/km-http-proxy:$(VERSION) \
	  --push .
	docker buildx build --platform linux/amd64 \
	  --file sidecars/audit-log/Dockerfile \
	  --tag $(ECR_REGISTRY)/km-audit-log:$(VERSION) \
	  --push .
	docker buildx build --platform linux/amd64 \
	  --file sidecars/tracing/Dockerfile \
	  --tag $(ECR_REGISTRY)/km-tracing:$(VERSION) \
	  --push sidecars/tracing/
