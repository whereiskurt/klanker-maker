locals {
  # =========================================================================
  # Sandbox Identity
  # The profile compiler writes this file with sandbox-specific values.
  # DO NOT edit manually — this template is the structural pattern only.
  # =========================================================================

  sandbox_id = "SANDBOX_ID_PLACEHOLDER"

  # Substrate selects which Terraform module path to use:
  #   "ec2spot"     -> infra/modules/ec2spot/v1.0.0
  #   "ecs-cluster" -> infra/modules/ecs-cluster/v1.0.0 (followed by ecs-task, ecs-service)
  substrate_module = "ecs-cluster"

  # =========================================================================
  # ECS Task Configuration (for ecs substrate)
  # The profile compiler populates containers[] with:
  #   1. main container (the workload image)
  #   2. dns-proxy sidecar
  #   3. http-proxy sidecar
  #   4. audit-log sidecar
  #   5. tracing sidecar
  # =========================================================================

  task = {
    name         = "km-sandbox-${local.sandbox_id}"
    regions      = ["us-east-1"]
    cluster_name = "sandbox"
    task_cpu     = 1024  # Total CPU units across main + 4 sidecars
    task_memory  = 2048  # Total memory across main + 4 sidecars

    containers = [
      # Main workload container — populated by profile compiler
      {
        name               = "main"
        image              = "MAIN_IMAGE_PLACEHOLDER"
        cpu                = 512
        memory             = 1024
        memory_reservation = 512
        essential          = true
        command            = []

        readonly_root_filesystem = false

        environment = [
          {
            name  = "SANDBOX_ID"
            value = local.sandbox_id
          },
          {
            name  = "KM_LABEL"
            value = "km"
          }
        ]

        secrets = []

        port_mappings = []

        depends_on = [
          {
            container_name = "dns-proxy"
            condition      = "START"
          },
          {
            container_name = "http-proxy"
            condition      = "START"
          }
        ]

        log_stream_prefix = "main"
      },

      # DNS Proxy sidecar — intercepts all DNS queries for allowlist enforcement
      # Image and config populated by Phase 2 profile compiler
      {
        name               = "dns-proxy"
        image              = "DNS_PROXY_IMAGE_PLACEHOLDER"
        cpu                = 128
        memory             = 256
        memory_reservation = 128
        essential          = true
        command            = []

        readonly_root_filesystem = false

        environment = [
          {
            name  = "SANDBOX_ID"
            value = local.sandbox_id
          }
        ]

        secrets = []
        port_mappings = []
        depends_on    = []
        log_stream_prefix = "dns-proxy"
      },

      # HTTP Proxy sidecar — intercepts HTTP/HTTPS traffic for allowlist enforcement
      # Image and config populated by Phase 2 profile compiler
      {
        name               = "http-proxy"
        image              = "HTTP_PROXY_IMAGE_PLACEHOLDER"
        cpu                = 128
        memory             = 256
        memory_reservation = 128
        essential          = true
        command            = []

        readonly_root_filesystem = false

        environment = [
          {
            name  = "SANDBOX_ID"
            value = local.sandbox_id
          }
        ]

        secrets = []
        port_mappings = []
        depends_on    = []
        log_stream_prefix = "http-proxy"
      },

      # Audit Log sidecar — aggregates and ships audit events to CloudWatch
      # Image and config populated by Phase 2 profile compiler
      {
        name               = "audit-log"
        image              = "AUDIT_LOG_IMAGE_PLACEHOLDER"
        cpu                = 64
        memory             = 128
        memory_reservation = 64
        essential          = false
        command            = []

        readonly_root_filesystem = false

        environment = [
          {
            name  = "SANDBOX_ID"
            value = local.sandbox_id
          }
        ]

        secrets = []
        port_mappings = []
        depends_on    = []
        log_stream_prefix = "audit-log"
      },

      # Tracing sidecar — collects and ships distributed traces (e.g., OpenTelemetry)
      # Image and config populated by Phase 2 profile compiler
      {
        name               = "tracing"
        image              = "TRACING_IMAGE_PLACEHOLDER"
        cpu                = 64
        memory             = 128
        memory_reservation = 64
        essential          = false
        command            = []

        readonly_root_filesystem = false

        environment = [
          {
            name  = "SANDBOX_ID"
            value = local.sandbox_id
          }
        ]

        secrets = []
        port_mappings = []
        depends_on    = []
        log_stream_prefix = "tracing"
      }
    ]
  }

  # ECS Service Configuration
  service = {
    name          = "km-sandbox-${local.sandbox_id}"
    regions       = ["us-east-1"]
    cluster_name  = "sandbox"
    task_family   = "km-sandbox-${local.sandbox_id}"
    desired_count = 1

    service_discovery = {
      name           = "km-sandbox-${local.sandbox_id}"
      container_name = "main"
      ttl            = 10
    }
  }

  # =========================================================================
  # Module inputs passed to terragrunt.hcl
  # =========================================================================

  module_inputs = {
    ecs_clusters = [
      {
        name            = "sandbox"
        region          = "us-east-1"
        enable_insights = false
      }
    ]

    ecs_tasks = [local.task]

    ecs_services = [local.service]
  }
}
