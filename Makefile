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

VERSION     ?= latest
REGION      ?= $(shell aws configure get region)
ACCOUNT_ID   = $(shell aws sts get-caller-identity --query Account --output text)
ECR_REGISTRY = $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com

# KM_ARTIFACTS_BUCKET must be set in the environment — no default
KM_ARTIFACTS_BUCKET ?=

SIDECARS := dns-proxy http-proxy audit-log

.PHONY: sidecars ecr-push ecr-login ecr-repos build-sidecars build-lambdas

## sidecars: cross-compile Go sidecars and upload binaries + tracing config to S3
sidecars:
	@if [ -z "$(KM_ARTIFACTS_BUCKET)" ]; then \
	  echo "ERROR: KM_ARTIFACTS_BUCKET is not set. Export it before running make sidecars."; \
	  exit 1; \
	fi
	@mkdir -p build
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -o build/dns-proxy ./sidecars/dns-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -o build/http-proxy ./sidecars/http-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -o build/audit-log ./sidecars/audit-log/cmd/
	aws s3 cp build/dns-proxy  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy
	aws s3 cp build/http-proxy s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy
	aws s3 cp build/audit-log  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log
	aws s3 cp sidecars/tracing/config.yaml s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml
	@echo ""
	@echo "Sidecar artifacts uploaded to s3://$(KM_ARTIFACTS_BUCKET)/sidecars/"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml"

## build-sidecars: cross-compile Go sidecars locally (no S3 upload)
build-sidecars:
	@mkdir -p build
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -o build/dns-proxy ./sidecars/dns-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -o build/http-proxy ./sidecars/http-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -o build/audit-log ./sidecars/audit-log/cmd/

## ecr-login: authenticate Docker daemon to ECR
ecr-login:
	aws ecr get-login-password --region $(REGION) | \
	  docker login --username AWS --password-stdin $(ECR_REGISTRY)

## ecr-repos: ensure all sidecar ECR repositories exist
ecr-repos:
	@for name in km-dns-proxy km-http-proxy km-audit-log km-tracing; do \
	  aws ecr describe-repositories --region $(REGION) --repository-names $$name 2>/dev/null || \
	  aws ecr create-repository --region $(REGION) --repository-name $$name; \
	done

## build-lambdas: cross-compile Go Lambda binaries for arm64 and package as deployment zips
build-lambdas:
	@mkdir -p build
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/bootstrap ./cmd/ttl-handler/
	cd build && zip -j ttl-handler.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/bootstrap ./cmd/budget-enforcer/
	cd build && zip -j budget-enforcer.zip bootstrap && rm bootstrap
	@echo ""
	@echo "Lambda deployment packages built:"
	@echo "  build/ttl-handler.zip"
	@echo "  build/budget-enforcer.zip"

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
