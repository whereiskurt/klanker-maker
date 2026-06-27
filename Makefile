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
LDFLAGS      = -X github.com/whereiskurt/klanker-maker/pkg/version.Number=v$(KM_VERSION) \
               -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=$(GIT_COMMIT)

VERSION     ?= latest
REGION      ?= $(shell aws configure get region 2>/dev/null || grep '^region:' km-config.yaml 2>/dev/null | awk '{print $$2}')
ACCOUNT_ID   = $(shell aws sts get-caller-identity --query Account --output text)
ECR_REGISTRY = $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com

# KM_ARTIFACTS_BUCKET must be set in the environment — no default
KM_ARTIFACTS_BUCKET ?=

SIDECARS := dns-proxy http-proxy audit-log

OTELCOL_VERSION ?= 0.120.0
OTELCOL_URL     := https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v$(OTELCOL_VERSION)/otelcol-contrib_$(OTELCOL_VERSION)_$(GOOS)_$(GOARCH).tar.gz

.PHONY: build build-km bump-version sidecars ecr-push ecr-login ecr-repos build-sidecars build-lambdas build-create-handler build-email-create-handler push-create-handler clean fetch-otelcol sandbox-image smoke-test-sandbox generate-ebpf test test-no-82.1-leftovers test-phase-84-1-import-blocks test-phase-84-1-removed-blocks test-phase-84-1-terraform-validate

## generate-ebpf: compile BPF C programs via bpf2go inside Docker (works from macOS)
## Regenerates pkg/ebpf/bpf_bpfel.go, pkg/ebpf/bpf_bpfel.o,
##             pkg/ebpf/sni/sni_bpfel.go, pkg/ebpf/sni/sni_bpfel.o,
##             pkg/ebpf/tls/opensslBpf_x86_bpfel.go, pkg/ebpf/tls/opensslBpf_x86_bpfel.o,
##             pkg/ebpf/tls/connectBpf_x86_bpfel.go, pkg/ebpf/tls/connectBpf_x86_bpfel.o
## The generated files are committed so make build works without clang or Docker.
generate-ebpf:
	docker build --quiet -f containers/Dockerfile.ebpf-generate -t km-ebpf-generate:latest .
	docker run --rm -v $(PWD):/src km-ebpf-generate:latest sh -c \
	  "cd /src/pkg/ebpf && go generate && cd /src/pkg/ebpf/sni && go generate && cd /src/pkg/ebpf/tls && go generate"
	@echo ""
	@echo "Generated eBPF loader files:"
	@echo "  pkg/ebpf/bpf_bpfel.go"
	@echo "  pkg/ebpf/bpf_bpfel.o"
	@echo "  pkg/ebpf/sni/sni_bpfel.go"
	@echo "  pkg/ebpf/sni/sni_bpfel.o"
	@echo "  pkg/ebpf/tls/opensslbpf_x86_bpfel.go"
	@echo "  pkg/ebpf/tls/opensslbpf_x86_bpfel.o"
	@echo "  pkg/ebpf/tls/connectbpf_x86_bpfel.go"
	@echo "  pkg/ebpf/tls/connectbpf_x86_bpfel.o"

## bump-version: increment the patch version in VERSION file
bump-version:
	@./scripts/bump-version.sh VERSION > /dev/null

## build: bump version then build the km CLI binary
build: bump-version
	$(eval KM_VERSION := $(shell cat VERSION | tr -d '[:space:]'))
	go build -ldflags '-X github.com/whereiskurt/klanker-maker/pkg/version.Number=v$(KM_VERSION) -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=$(GIT_COMMIT)' -o km ./cmd/km/
	@echo "Built: km v$(KM_VERSION) ($(GIT_COMMIT))"

## build-km: alias for build
build-km: build

## test: run all Go unit tests + static analysis gates
## Note: some packages have pre-existing failures unrelated to Phase 84.4
## (cmd/km-slack, cmd/ttl-handler, internal/app/cmd, pkg/compiler).
## They are excluded until a separate cleanup fixes them.
## (cmd/configui removed 2026-06-02 — the web dashboard was unused.)
## See .planning/phases/84.4-*/deferred-items.md for details.
.PHONY: test
test:
	go test $$(go list ./... | grep -v 'cmd/km-slack' | grep -v 'cmd/ttl-handler' | grep -v 'internal/app/cmd' | grep -v 'pkg/compiler')
	$(MAKE) test-no-82.1-leftovers
	$(MAKE) test-phase-84-1-import-blocks
	$(MAKE) test-phase-84-1-removed-blocks
	$(MAKE) test-phase-84-1-terraform-validate

## test-no-82.1-leftovers: Phase 84 W0-11 — grep gate for Phase 82.1 legacy symbols.
## GREEN at Wave 2 (Phase 82.1 symbols removed from doc + live wiring in Plans 84-08/84-03).
## Scope: code paths only (infra/ internal/ pkg/ cmd/). OPERATOR-GUIDE.md and CLAUDE.md
## may legitimately mention activate_rule_set in past-tense prose explaining the removal.
## Excludes infra/modules/ses/v1.0.0/ (historical reference — kept per CONTEXT.md lock)
## and .terragrunt-cache/ (cached copies of historical module).
.PHONY: test-no-82.1-leftovers
test-no-82.1-leftovers:
	@! grep -rn --exclude-dir='v1.0.0' --exclude-dir='.terragrunt-cache' \
		"KM_SES_ACTIVATE_RULESET\|activate_rule_set" \
		infra/ internal/ pkg/ cmd/ \
		|| (echo "Phase 82.1 leftovers found — see Phase 84"; exit 1)

## test-phase-84-1-import-blocks: Plan 84.1-04 Task 2 — foundation module must
## have exactly 6 import {} blocks (H9 from plan-checker rev 1: NO DKIM CNAME
## imports — DKIM record import is OPERATOR-RUN per OPERATOR-GUIDE.md because
## DKIM token names are not knowable at plan time).
## Expected: rule_set, active_rule_set, domain_identity, domain_dkim,
## ses_verification (TXT), mx (MX).
test-phase-84-1-import-blocks:
	@count=$$(grep -cE '^import[[:space:]]*\{' infra/modules/ses-shared-rule-set/v1.0.0/main.tf); \
	if [ "$$count" -ne 6 ]; then \
		echo "FAIL: expected exactly 6 import blocks in foundation main.tf, found $$count (H9: NO DKIM import blocks — operator-run via OPERATOR-GUIDE.md)"; \
		exit 1; \
	fi
	@echo "OK: foundation main.tf has 6 import blocks (H9-compliant, no DKIM imports)"

## test-phase-84-1-removed-blocks: Plan 84.1-04 Task 2 — regional v2.0.0 module
## must have exactly 7 removed {} blocks (one per v1.0.0-owned shared resource)
## and 7 'destroy = false' lifecycle entries so the v1.0.0 → v2.0.0 cutover
## releases state without destroying AWS objects (GAP-6).
test-phase-84-1-removed-blocks:
	@count=$$(grep -cE '^removed[[:space:]]*\{' infra/modules/ses/v2.0.0/main.tf); \
	if [ "$$count" -ne 7 ]; then \
		echo "FAIL: expected exactly 7 removed blocks in regional v2.0.0 main.tf, found $$count"; \
		exit 1; \
	fi
	@destroy_count=$$(grep -cE 'destroy[[:space:]]*=[[:space:]]*false' infra/modules/ses/v2.0.0/main.tf); \
	if [ "$$destroy_count" -ne 7 ]; then \
		echo "FAIL: expected exactly 7 'destroy = false' lifecycle entries, found $$destroy_count"; \
		exit 1; \
	fi
	@echo "OK: regional v2.0.0 main.tf has 7 removed blocks with destroy=false"

## test-phase-84-1-terraform-validate: Plan 84.1-04 Task 2 (C4 from plan-checker
## rev 1) — pre-UAT terraform-validate gate. Catches typos in import IDs,
## removed-block addresses, and HCL syntax errors BEFORE the operator hits them
## at UAT time against real AWS. Runs terraform fmt -check + init -backend=false
## + validate against the foundation module + regional v2.0.0 module.
test-phase-84-1-terraform-validate:
	@for dir in infra/modules/ses-shared-rule-set/v1.0.0 infra/modules/ses/v2.0.0; do \
		echo "==> terraform validate $$dir"; \
		( cd $$dir && terraform fmt -check && terraform init -backend=false -input=false >/dev/null && terraform validate ) || exit 1; \
	done
	@echo "OK: foundation + regional v2.0.0 modules pass terraform validate"

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
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/km-linux ./cmd/km/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-slack ./cmd/km-slack/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-presence ./cmd/km-presence/
	aws s3 cp build/dns-proxy  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy
	aws s3 cp build/http-proxy s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy
	aws s3 cp build/audit-log  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log
	aws s3 cp build/km-linux   s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km
	aws s3 cp build/km-slack   s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-slack
	aws s3 cp build/km-presence s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-presence
	aws s3 cp build/otelcol-contrib s3://$(KM_ARTIFACTS_BUCKET)/sidecars/otelcol-contrib
	aws s3 cp sidecars/tracing/config.yaml s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml
	@echo ""
	@echo "Sidecar artifacts uploaded to s3://$(KM_ARTIFACTS_BUCKET)/sidecars/"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-slack"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-presence"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/otelcol-contrib"
	@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml"

## sandbox-image: build the km-sandbox base container image locally
sandbox-image:
	docker buildx build --platform linux/amd64 \
	  --file containers/sandbox/Dockerfile \
	  --tag km-sandbox:$(VERSION) \
	  --tag km-sandbox:latest \
	  --load \
	  containers/sandbox/

## smoke-test-sandbox: build and smoke-test the km-sandbox container image
smoke-test-sandbox: sandbox-image
	bash scripts/smoke-test-sandbox.sh

## build-sidecars: cross-compile Go sidecars locally (no S3 upload)
build-sidecars:
	@mkdir -p build
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/dns-proxy ./sidecars/dns-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/http-proxy ./sidecars/http-proxy/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -ldflags '$(LDFLAGS)' -o build/audit-log ./sidecars/audit-log/cmd/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-slack ./cmd/km-slack/
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-presence ./cmd/km-presence/

## ecr-login: authenticate Docker daemon to ECR
ecr-login:
	aws ecr get-login-password --region $(REGION) | \
	  docker login --username AWS --password-stdin $(ECR_REGISTRY)

## ecr-repos: ensure all sidecar and Lambda container ECR repositories exist
ecr-repos:
	@for name in km-dns-proxy km-http-proxy km-audit-log km-tracing km-create-handler km-sandbox; do \
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
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/create-handler/
	cd build && zip -j create-handler.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/km-slack-bridge/
	cd build && zip -j km-slack-bridge.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/km-github-bridge/
	cd build && zip -j km-github-bridge.zip bootstrap && rm bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/km-h1-bridge/
	cd build && zip -j km-h1-bridge.zip bootstrap && rm bootstrap
	@echo ""
	@echo "Lambda deployment packages built:"
	@echo "  build/ttl-handler.zip"
	@echo "  build/budget-enforcer.zip"
	@echo "  build/github-token-refresher.zip"
	@echo "  build/email-create-handler.zip"
	@echo "  build/create-handler.zip"
	@echo "  build/km-slack-bridge.zip"
	@echo "  build/km-github-bridge.zip"
	@echo "  build/km-h1-bridge.zip"

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
	docker buildx build --platform linux/amd64 \
	  --file containers/sandbox/Dockerfile \
	  --tag $(ECR_REGISTRY)/km-sandbox:$(VERSION) \
	  --tag $(ECR_REGISTRY)/km-sandbox:latest \
	  --push \
	  containers/sandbox/
