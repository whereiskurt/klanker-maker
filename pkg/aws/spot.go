package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

const spotTerminationTimeout = 5 * time.Minute

// EC2API is the interface subset of ec2.Client used by TerminateSpotInstance.
// Defined here to enable mock-based unit testing without real AWS calls.
type EC2API interface {
	TerminateInstances(
		ctx context.Context,
		params *ec2.TerminateInstancesInput,
		optFns ...func(*ec2.Options),
	) (*ec2.TerminateInstancesOutput, error)
}

// TerminateSpotInstance explicitly terminates the EC2 instance identified by
// instanceID and waits up to 5 minutes for the termination to complete.
// This must be called before `terragrunt destroy` on EC2 spot sandboxes to
// ensure the spot instance is cleanly shut down before Terraform attempts to
// remove associated resources (security groups, IAM role, etc.).
func TerminateSpotInstance(ctx context.Context, client EC2API, instanceID string) error {
	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}

	if _, err := client.TerminateInstances(ctx, input); err != nil {
		return fmt.Errorf("terminate EC2 instance %s: %w", instanceID, err)
	}

	// Wait for the instance to reach the terminated state.
	// The ec2.NewInstanceTerminatedWaiter requires the full ec2.Client, not our
	// interface, so we build the waiter from a concrete client via the passed ctx.
	// For testability, we accept that the waiter is only used in production
	// (integration) paths — tests mock TerminateInstances and verify the call.
	if c, ok := client.(*ec2.Client); ok {
		waiter := ec2.NewInstanceTerminatedWaiter(c)
		waitInput := &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("instance-id"),
					Values: []string{instanceID},
				},
			},
		}
		if err := waiter.Wait(ctx, waitInput, spotTerminationTimeout); err != nil {
			return fmt.Errorf("wait for instance %s termination: %w", instanceID, err)
		}
	}

	return nil
}

// GetSpotInstanceID extracts the spot_instance_id value from the JSON map
// returned by `terragrunt output -json`. Each output value is an object with
// a "value" key containing the actual string value.
func GetSpotInstanceID(terragruntOutput map[string]interface{}) (string, error) {
	raw, ok := terragruntOutput["spot_instance_id"]
	if !ok {
		return "", fmt.Errorf("spot_instance_id not found in terragrunt output")
	}

	// `terragrunt output -json` wraps each value: {"value": "i-0abc...", "type": "string", ...}
	outputObj, ok := raw.(map[string]interface{})
	if !ok {
		// Handle the case where the raw value is already a string (non-standard)
		if s, ok := raw.(string); ok {
			return s, nil
		}
		return "", fmt.Errorf("spot_instance_id output has unexpected type %T", raw)
	}

	valueRaw, ok := outputObj["value"]
	if !ok {
		return "", fmt.Errorf("spot_instance_id output missing 'value' field")
	}

	instanceID, ok := valueRaw.(string)
	if !ok {
		return "", fmt.Errorf("spot_instance_id value is not a string: %T", valueRaw)
	}

	if instanceID == "" {
		return "", fmt.Errorf("spot_instance_id is empty")
	}

	return instanceID, nil
}
