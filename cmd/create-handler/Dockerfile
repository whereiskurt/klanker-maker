# Multi-stage Dockerfile for km-create-handler Lambda container image.
# Produces an arm64 container image for use with Lambda's provided:al2023-arm64 runtime.
#
# Bundles:
#   - bootstrap (km-create-handler binary — Lambda entry point)
#   - km binary (subprocess invoked by handler to run km create)
#   - terraform (arm64 binary for sandbox provisioning)
#   - terragrunt (arm64 binary for module orchestration)
#   - infra/ (Terraform modules + Terragrunt live hierarchy)
#
# Build with:
#   make build-create-handler   # compile Go binaries
#   make push-create-handler    # docker buildx build + push to ECR

# ============================================================
# Stage 1: Download terraform and terragrunt arm64 binaries
# ============================================================
FROM public.ecr.aws/amazonlinux/amazonlinux:2023 AS downloader

ARG TERRAFORM_VERSION=1.9.8
ARG TERRAGRUNT_VERSION=0.67.16

RUN yum install -y unzip curl && yum clean all

# Download Terraform arm64 binary from HashiCorp releases
RUN curl -fsSL \
    "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_arm64.zip" \
    -o /tmp/terraform.zip && \
    unzip /tmp/terraform.zip -d /usr/local/bin/ && \
    chmod +x /usr/local/bin/terraform && \
    rm /tmp/terraform.zip

# Download Terragrunt arm64 binary from GitHub releases
RUN curl -fsSL \
    "https://github.com/gruntwork-io/terragrunt/releases/download/v${TERRAGRUNT_VERSION}/terragrunt_linux_arm64" \
    -o /usr/local/bin/terragrunt && \
    chmod +x /usr/local/bin/terragrunt

# ============================================================
# Stage 2: Lambda runtime image
# ============================================================
FROM public.ecr.aws/lambda/provided:al2023-arm64

# Copy terraform and terragrunt from the downloader stage
COPY --from=downloader /usr/local/bin/terraform  /var/task/terraform
COPY --from=downloader /usr/local/bin/terragrunt /var/task/terragrunt

# Copy the create-handler binary as the Lambda entry point (bootstrap)
COPY build/km-create-handler /var/task/bootstrap

# Copy the km binary for use as a subprocess by the handler
COPY build/km /var/task/km

# Copy the entire infra/ directory so the handler has access to
# Terraform modules (infra/modules/) and the Terragrunt live hierarchy (infra/live/)
COPY infra/ /var/task/infra/

# Lambda entry point
CMD ["bootstrap"]
