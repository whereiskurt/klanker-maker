"""
km ECS Fargate Spot Interruption Handler

Triggered by EventBridge when an ECS task enters STOPPING state with
stopCode=SpotInterruption. Executes the km-upload-artifacts script inside
the stopping container via ECS Exec before the task is reclaimed.

Fargate provides ~30 seconds between STOPPING and SIGKILL — artifact upload
must complete within this window.
"""
import boto3
import json
import os

ecs = boto3.client("ecs")
BUCKET = os.environ["ARTIFACT_BUCKET"]


def handler(event, context):
    detail = event["detail"]
    task_arn = detail["taskArn"]
    cluster_arn = detail["clusterArn"]

    # Extract sandbox ID from task group.
    # Task group is set to "sandbox:{sandbox-id}" by the compiler.
    group = detail.get("group", "")
    sandbox_id = group.replace("sandbox:", "") if group.startswith("sandbox:") else None

    if not sandbox_id:
        print(f"No sandbox ID found in task group: {group!r}")
        return {"statusCode": 200, "body": "no sandbox ID"}

    print(f"Spot interruption detected for sandbox {sandbox_id}, task {task_arn}")

    # Execute artifact upload command inside the stopping container.
    # ECS Exec requires enableExecuteCommand=true on the ECS service (set by compiler).
    # The km-upload-artifacts script is installed by the compiler into the main container.
    try:
        response = ecs.execute_command(
            cluster=cluster_arn,
            task=task_arn,
            container="main",
            interactive=False,
            command="/opt/km/bin/km-upload-artifacts",
        )
        session = response.get("session", {})
        print(
            f"Artifact upload initiated for {sandbox_id}: "
            f"session_id={session.get('sessionId', 'unknown')}"
        )
    except ecs.exceptions.ClusterNotFoundException:
        print(f"Cluster not found: {cluster_arn}")
    except ecs.exceptions.TaskNotFoundException:
        print(f"Task not found (may have already terminated): {task_arn}")
    except Exception as e:
        # Best-effort: log and continue — the task is stopping regardless
        print(f"Failed to execute artifact upload for {sandbox_id}: {type(e).__name__}: {e}")

    return {"statusCode": 200, "body": f"processed {sandbox_id}"}
