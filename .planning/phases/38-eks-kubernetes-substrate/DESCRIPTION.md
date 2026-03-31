# Phase 38: EKS / Kubernetes Substrate

## Problem

EC2 works but is heavy — one VM per sandbox. Docker Compose (Phase 37) works locally but has no production-grade orchestration. Kubernetes is the natural production substrate for container workloads: multi-tenant, auto-scaling, and already widely deployed.

EKS provides the AWS-native Kubernetes path. The same Pod spec could also target vanilla Kubernetes or other managed Kubernetes services (GKE, AKS) in the future.

## Goal

`km create profiles/claude-dev.yaml --substrate eks` provisions a sandbox as a Kubernetes Pod on an existing EKS cluster — with sidecar containers for DNS/HTTP proxy enforcement, IRSA for IAM, NetworkPolicy for egress control, and the same budget/audit/telemetry topology as EC2.

## Depends on

Phase 36 (km-sandbox base image) and Phase 37 (Docker substrate validates the container topology works)

## Design

### Substrate: `eks`

```yaml
spec:
  runtime:
    substrate: eks
    # instanceType maps to resource requests (t3.medium → 2 vCPU, 4GB)
    # spot maps to node affinity for spot node pools
    # region maps to EKS cluster selection
```

### Architecture: One Pod = One Sandbox

Each sandbox is a single Kubernetes Pod with 5 containers (same as Docker Compose / ECS):

```
Pod: km-sb-a1b2c3d4
├── main           (km-sandbox base image + entrypoint)
├── km-dns-proxy   (sidecar: DNS filtering on :5353)
├── km-http-proxy  (sidecar: HTTP/HTTPS filtering + MITM on :3128)
├── km-audit-log   (sidecar: audit trail)
└── km-tracing     (sidecar: OTEL collector)
```

Pods share a network namespace (localhost), so the container topology is identical to ECS `awsvpc` mode. Proxy env vars point to `localhost:3128`, DNS to `localhost:5353`.

### Compiler: `compileEKS()`

Generates a Kubernetes Pod manifest (YAML) instead of Terragrunt HCL:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: km-sb-a1b2c3d4
  namespace: km-sandboxes
  labels:
    km/sandbox-id: sb-a1b2c3d4
    km/profile: claude-dev
    km/substrate: eks
  annotations:
    km/ttl-expiry: "2026-03-30T12:00:00Z"
    km/budget-compute: "2.00"
    km/budget-ai: "5.00"
spec:
  serviceAccountName: km-sb-a1b2c3d4   # IRSA-bound
  terminationGracePeriodSeconds: 30

  # Node selection: spot vs on-demand
  nodeSelector:
    kubernetes.io/capacity-type: spot   # or "on-demand"

  # Init container: CA cert setup (runs before sidecars)
  initContainers:
    - name: ca-setup
      image: <ecr>/km-sandbox:latest
      command: ["/opt/km/setup-ca.sh"]
      volumeMounts:
        - name: ca-trust
          mountPath: /ca-output
      env:
        - name: KM_ARTIFACTS_BUCKET
          value: km-artifacts-12345

  containers:
    - name: main
      image: <ecr>/km-sandbox:latest
      workingDir: /workspace
      securityContext:
        runAsUser: 1000
        runAsGroup: 1000
        readOnlyRootFilesystem: false   # initCommands may need writes
      env:
        - name: KM_SANDBOX_ID
          value: sb-a1b2c3d4
        - name: HTTP_PROXY
          value: http://localhost:3128
        - name: HTTPS_PROXY
          value: http://localhost:3128
        - name: SSL_CERT_FILE
          value: /etc/pki/tls/certs/ca-bundle.crt
        - name: REQUESTS_CA_BUNDLE
          value: /etc/pki/tls/certs/ca-bundle.crt
        - name: NODE_EXTRA_CA_CERTS
          value: /etc/pki/tls/certs/ca-bundle.crt
        # ... all Phase 36 entrypoint env vars
      envFrom:
        - secretRef:
            name: km-sb-a1b2c3d4-secrets   # K8s Secret with API keys
      volumeMounts:
        - name: ca-trust
          mountPath: /etc/pki/ca-trust/source/anchors
          readOnly: true
        - name: workspace
          mountPath: /workspace
      resources:
        requests:
          cpu: "1000m"
          memory: "2Gi"
        limits:
          cpu: "2000m"
          memory: "4Gi"

    - name: km-dns-proxy
      image: <ecr>/km-dns-proxy:latest
      env:
        - name: ALLOWED_SUFFIXES
          value: ".amazonaws.com,.github.com,.npmjs.org"
        - name: UPSTREAM_DNS
          value: "169.254.20.10"   # kube-dns / CoreDNS
      resources:
        requests: { cpu: "50m", memory: "64Mi" }
        limits: { cpu: "100m", memory: "128Mi" }

    - name: km-http-proxy
      image: <ecr>/km-http-proxy:latest
      env:
        - name: ALLOWED_HOSTS
          value: "api.github.com,registry.npmjs.org"
        - name: KM_PROXY_CA_CERT
          valueFrom:
            secretKeyRef:
              name: km-platform-ca
              key: ca-pem-b64
        - name: KM_BUDGET_ENABLED
          value: "true"
        - name: KM_BUDGET_TABLE
          value: km-budgets
      resources:
        requests: { cpu: "100m", memory: "128Mi" }
        limits: { cpu: "200m", memory: "256Mi" }

    - name: km-audit-log
      image: <ecr>/km-audit-log:latest
      env:
        - name: AUDIT_LOG_DEST
          value: cloudwatch
        - name: CW_LOG_GROUP
          value: "/km/sandboxes/sb-a1b2c3d4/"
      resources:
        requests: { cpu: "25m", memory: "32Mi" }
        limits: { cpu: "50m", memory: "64Mi" }

    - name: km-tracing
      image: <ecr>/km-tracing:latest
      env:
        - name: OTEL_S3_BUCKET
          value: km-artifacts-12345
      resources:
        requests: { cpu: "25m", memory: "32Mi" }
        limits: { cpu: "50m", memory: "64Mi" }

  volumes:
    - name: ca-trust
      emptyDir: {}
    - name: workspace
      emptyDir:
        sizeLimit: 10Gi
```

### IAM: IRSA (IAM Roles for Service Accounts)

Each sandbox gets a Kubernetes ServiceAccount annotated with an IAM role ARN:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: km-sb-a1b2c3d4
  namespace: km-sandboxes
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::052251888500:role/km-sb-a1b2c3d4
```

The IAM role is the same scoped role as EC2 — region-locked, time-limited, Bedrock access. IRSA injects credentials via a projected service account token volume. No long-lived keys.

### Network enforcement: NetworkPolicy

Kubernetes NetworkPolicy replaces VPC Security Groups for egress control:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: km-sb-a1b2c3d4
  namespace: km-sandboxes
spec:
  podSelector:
    matchLabels:
      km/sandbox-id: sb-a1b2c3d4
  policyTypes:
    - Egress
  egress:
    # Allow DNS to kube-dns
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
      ports:
        - port: 53
          protocol: UDP
        - port: 53
          protocol: TCP
    # Allow HTTPS to AWS services (via proxy)
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
      ports:
        - port: 443
          protocol: TCP
        - port: 80
          protocol: TCP
```

The NetworkPolicy is the outer wall (like SGs). The DNS/HTTP proxy sidecars are the inner wall — they filter at the application layer even if the NetworkPolicy allows the IP.

### Namespace isolation

All sandboxes run in a dedicated `km-sandboxes` namespace:

- ResourceQuota per namespace (caps total CPU/memory across all sandboxes)
- LimitRange (default resource requests/limits per Pod)
- NetworkPolicy default-deny (no inter-pod traffic unless explicitly allowed)
- No access to other namespaces

### TTL and lifecycle

Two approaches for sandbox TTL:

**Option A — CronJob reaper:** A `km-reaper` CronJob runs every minute in the `km-system` namespace, checks Pod annotations for TTL expiry, and deletes expired Pods. Simple, familiar.

**Option B — Custom controller:** A lightweight Kubernetes controller watches Pods with `km/sandbox-id` labels, manages TTL schedules, and handles budget enforcement. More complex but more responsive.

Option A is sufficient for v1.

### Budget enforcement

Same as EC2: the budget-enforcer Lambda (or a Kubernetes CronJob equivalent) polls DynamoDB, checks spend, and suspends sandboxes at the limit:

- **Compute:** Track pod uptime × node instance cost (from spot pricing API)
- **AI:** HTTP proxy sidecar meters tokens (same as EC2)
- **Enforcement:** Delete the Pod (or annotate it for the reaper to handle)

### `km shell` for EKS

```bash
kubectl exec -it km-sb-a1b2c3d4 -c main -n km-sandboxes -- /bin/bash
```

`shell.go` detects `substrate: eks` and shells out to `kubectl exec` instead of SSM.

### EKS cluster prerequisites

The operator must have an existing EKS cluster. `km init` for EKS:

1. Creates the `km-sandboxes` namespace
2. Creates the `km-system` namespace (for reaper, platform components)
3. Deploys the default-deny NetworkPolicy
4. Creates the IRSA OIDC provider association
5. Pushes container images to ECR
6. Deploys the reaper CronJob

### km-config.yaml additions

```yaml
eks:
  cluster_name: my-eks-cluster
  namespace: km-sandboxes
  kubeconfig: ~/.kube/config     # or use in-cluster config
```

### Files to create/modify

| File | Change |
|------|--------|
| `pkg/profile/types.go` | Add `eks` to substrate enum |
| `pkg/compiler/compiler.go` | Add `compileEKS()` path |
| `pkg/compiler/k8s.go` | **New** — Pod, ServiceAccount, NetworkPolicy, Secret generation |
| `internal/app/cmd/create.go` | EKS substrate: `kubectl apply` |
| `internal/app/cmd/destroy.go` | EKS substrate: `kubectl delete` |
| `internal/app/cmd/shell.go` | EKS substrate: `kubectl exec` |
| `internal/app/cmd/list.go` | EKS substrate: list Pods by label |
| `internal/app/cmd/status.go` | EKS substrate: Pod describe |
| `internal/app/cmd/logs.go` | EKS substrate: `kubectl logs` |
| `internal/app/cmd/init.go` | EKS init: namespace, NetworkPolicy, IRSA, reaper |
| `infra/modules/eks-sandbox/` | **New** — Terraform for IRSA role per sandbox |
| `containers/reaper/` | **New** — TTL reaper CronJob |

### Testing

1. `km validate` accepts `substrate: eks`
2. `km create` with `substrate: eks` — Pod running with all 5 containers
3. `km shell <id>` — kubectl exec works
4. DNS enforcement — NXDOMAIN for blocked domains
5. HTTP enforcement — 403 for blocked hosts
6. IRSA — Pod can call Bedrock, can't call outside allowed regions
7. NetworkPolicy — Pod can't reach other Pods
8. TTL — Reaper deletes expired Pods
9. Budget — AI spend tracked, Pod deleted at limit
10. `km destroy <id>` — clean teardown (Pod, ServiceAccount, NetworkPolicy, IAM role)
