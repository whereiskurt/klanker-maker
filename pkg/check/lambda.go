package check

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	lambdapkg "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// waitFunctionUpdated polls GetFunction until the function's LastUpdateStatus
// leaves "InProgress", so a following UpdateFunctionConfiguration does not race
// an in-flight UpdateFunctionCode (Lambda returns ResourceConflictException /
// HTTP 409 while an update is in progress). Best-effort and bounded: on timeout
// it returns so the caller still attempts the update (which then surfaces any
// real error). Mock GetFunction implementations that return a nil/empty
// Configuration fall through immediately.
func waitFunctionUpdated(ctx context.Context, client LambdaClient, functionName string) {
	for i := 0; i < 30; i++ {
		out, err := client.GetFunction(ctx, &lambdapkg.GetFunctionInput{
			FunctionName: aws.String(functionName),
		})
		if err != nil || out.Configuration == nil ||
			out.Configuration.LastUpdateStatus != lambdatypes.LastUpdateStatusInProgress {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// LambdaClient is the subset of the AWS Lambda API used by pkg/check.
// Satisfied by *lambdapkg.Client; an interface for test injection.
type LambdaClient interface {
	CreateFunction(ctx context.Context, params *lambdapkg.CreateFunctionInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.CreateFunctionOutput, error)
	UpdateFunctionCode(ctx context.Context, params *lambdapkg.UpdateFunctionCodeInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.UpdateFunctionCodeOutput, error)
	UpdateFunctionConfiguration(ctx context.Context, params *lambdapkg.UpdateFunctionConfigurationInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.UpdateFunctionConfigurationOutput, error)
	GetFunction(ctx context.Context, params *lambdapkg.GetFunctionInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.GetFunctionOutput, error)
	DeleteFunction(ctx context.Context, params *lambdapkg.DeleteFunctionInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.DeleteFunctionOutput, error)
	Invoke(ctx context.Context, params *lambdapkg.InvokeInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.InvokeOutput, error)
}

// NewLambdaClient constructs an AWS Lambda client from an aws.Config.
func NewLambdaClient(awsCfg aws.Config) LambdaClient {
	return lambdapkg.NewFromConfig(awsCfg)
}

// DeployInput holds all parameters for deploying (create or update) a check Lambda.
type DeployInput struct {
	// FunctionName is the full AWS Lambda function name (e.g. "km-check-qotd").
	FunctionName string
	// RoleARN is the IAM role ARN for the Lambda ({prefix}-check-runner role).
	RoleARN string
	// Memory is the Lambda memory in MB (default 256).
	Memory int32
	// Timeout is the Lambda timeout in seconds (default 30).
	Timeout int32
	// Env is the static environment variables to set (non-secret keys).
	// KM_CHECK_NAME, KM_ARTIFACTS_BUCKET, KM_CHECK_TRIGGER, KM_CHECK_SECRET_PATHS
	// must be included by the caller.
	Env map[string]string
	// Tags are applied to the Lambda function.
	Tags map[string]string

	// --- Zip path (PackageType=Zip, default) ---
	// ZipBytes holds the zip archive when it is <=50 MB. Mutually exclusive with S3Bucket/S3Key.
	ZipBytes []byte
	// S3Bucket is the S3 bucket holding the zip when it is >50 MB.
	S3Bucket string
	// S3Key is the S3 key for the large zip. Used only when S3Bucket != "".
	S3Key string

	// --- Container (PackageType=Image, --image opt-in) ---
	// ImageURI is the ECR image URI. When non-empty, PackageType=Image is used.
	ImageURI string
}

// DeployFunction creates the Lambda function if absent; otherwise updates the
// function code and configuration (two separate SDK calls, as required by AWS).
// For --image packages: PackageType=Image, no Handler/Runtime.
// For zip packages: PackageType=Zip, Runtime=python3.13, arm64, Handler=_km_check_bootstrap.handler.
func DeployFunction(ctx context.Context, client LambdaClient, in DeployInput) (functionARN string, err error) {
	// Check if function exists.
	_, getErr := client.GetFunction(ctx, &lambdapkg.GetFunctionInput{
		FunctionName: aws.String(in.FunctionName),
	})

	if getErr != nil {
		// Create path.
		return createFunction(ctx, client, in)
	}
	// Update path (two calls required by AWS).
	return updateFunction(ctx, client, in)
}

func createFunction(ctx context.Context, client LambdaClient, in DeployInput) (string, error) {
	mem, timeout := defaultMemTimeout(in)
	envVars := buildEnvMap(in.Env)
	tags := in.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	createIn := &lambdapkg.CreateFunctionInput{
		FunctionName: aws.String(in.FunctionName),
		Role:         aws.String(in.RoleARN),
		MemorySize:   aws.Int32(mem),
		Timeout:      aws.Int32(timeout),
		Environment:  &lambdatypes.Environment{Variables: envVars},
		Tags:         tags,
	}

	if in.ImageURI != "" {
		// Container Lambda.
		createIn.PackageType = lambdatypes.PackageTypeImage
		createIn.Code = &lambdatypes.FunctionCode{ImageUri: aws.String(in.ImageURI)}
	} else {
		// Zip Lambda.
		createIn.PackageType = lambdatypes.PackageTypeZip
		createIn.Runtime = lambdatypes.RuntimePython313
		createIn.Architectures = []lambdatypes.Architecture{lambdatypes.ArchitectureArm64}
		createIn.Handler = aws.String(BootstrapHandler)
		createIn.Code = buildZipCode(in)
	}

	out, err := client.CreateFunction(ctx, createIn)
	if err != nil {
		return "", fmt.Errorf("Lambda CreateFunction %q: %w", in.FunctionName, err)
	}
	return aws.ToString(out.FunctionArn), nil
}

func updateFunction(ctx context.Context, client LambdaClient, in DeployInput) (string, error) {
	// Call 1: UpdateFunctionCode.
	var codeOut *lambdapkg.UpdateFunctionCodeOutput
	var err error
	if in.ImageURI != "" {
		codeOut, err = client.UpdateFunctionCode(ctx, &lambdapkg.UpdateFunctionCodeInput{
			FunctionName: aws.String(in.FunctionName),
			ImageUri:     aws.String(in.ImageURI),
		})
	} else if in.S3Bucket != "" {
		codeOut, err = client.UpdateFunctionCode(ctx, &lambdapkg.UpdateFunctionCodeInput{
			FunctionName: aws.String(in.FunctionName),
			S3Bucket:     aws.String(in.S3Bucket),
			S3Key:        aws.String(in.S3Key),
		})
	} else {
		codeOut, err = client.UpdateFunctionCode(ctx, &lambdapkg.UpdateFunctionCodeInput{
			FunctionName: aws.String(in.FunctionName),
			ZipFile:      in.ZipBytes,
		})
	}
	if err != nil {
		return "", fmt.Errorf("Lambda UpdateFunctionCode %q: %w", in.FunctionName, err)
	}

	// UpdateFunctionCode puts the function into LastUpdateStatus=InProgress; the
	// immediately-following config update would 409 (ResourceConflictException).
	// Wait for it to settle first.
	waitFunctionUpdated(ctx, client, in.FunctionName)

	// Call 2: UpdateFunctionConfiguration.
	mem, timeout := defaultMemTimeout(in)
	envVars := buildEnvMap(in.Env)
	cfgIn := &lambdapkg.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(in.FunctionName),
		MemorySize:   aws.Int32(mem),
		Timeout:      aws.Int32(timeout),
		Environment:  &lambdatypes.Environment{Variables: envVars},
	}
	if in.ImageURI == "" {
		// Zip-only: set runtime/arch/handler on config update.
		cfgIn.Runtime = lambdatypes.RuntimePython313
		cfgIn.Handler = aws.String(BootstrapHandler)
	}
	if _, err := client.UpdateFunctionConfiguration(ctx, cfgIn); err != nil {
		return "", fmt.Errorf("Lambda UpdateFunctionConfiguration %q: %w", in.FunctionName, err)
	}

	return aws.ToString(codeOut.FunctionArn), nil
}

// UpdateTriggerEnv fetches the current function configuration, merges the new
// triggerJSON into the existing env as KM_CHECK_TRIGGER, then calls
// UpdateFunctionConfiguration. Used by km check sync.
func UpdateTriggerEnv(ctx context.Context, client LambdaClient, functionName, triggerJSON string) error {
	// Fetch current config.
	cfgOut, err := client.GetFunction(ctx, &lambdapkg.GetFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		return fmt.Errorf("UpdateTriggerEnv GetFunction %q: %w", functionName, err)
	}

	// Merge existing env + updated trigger.
	existing := map[string]string{}
	if cfgOut.Configuration != nil && cfgOut.Configuration.Environment != nil {
		for k, v := range cfgOut.Configuration.Environment.Variables {
			existing[k] = v
		}
	}
	existing["KM_CHECK_TRIGGER"] = triggerJSON

	// Settle any in-flight update before reconfiguring (avoid 409).
	waitFunctionUpdated(ctx, client, functionName)

	_, err = client.UpdateFunctionConfiguration(ctx, &lambdapkg.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
		Environment:  &lambdatypes.Environment{Variables: existing},
	})
	if err != nil {
		return fmt.Errorf("UpdateTriggerEnv UpdateFunctionConfiguration %q: %w", functionName, err)
	}
	return nil
}

// InvokeFunction invokes a check Lambda synchronously and returns the response
// payload bytes.
func InvokeFunction(ctx context.Context, client LambdaClient, functionName string, payload map[string]interface{}) ([]byte, error) {
	payloadBytes := []byte{}
	if payload != nil {
		var err error
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("InvokeFunction marshal payload: %w", err)
		}
	}
	out, err := client.Invoke(ctx, &lambdapkg.InvokeInput{
		FunctionName:   aws.String(functionName),
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        payloadBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("Lambda Invoke %q: %w", functionName, err)
	}
	if out.FunctionError != nil && *out.FunctionError != "" {
		return out.Payload, fmt.Errorf("Lambda function error (%s): %s", *out.FunctionError, string(out.Payload))
	}
	return out.Payload, nil
}

// DeleteFunction deletes a check Lambda function.
func DeleteFunction(ctx context.Context, client LambdaClient, functionName string) error {
	_, err := client.DeleteFunction(ctx, &lambdapkg.DeleteFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		return fmt.Errorf("Lambda DeleteFunction %q: %w", functionName, err)
	}
	return nil
}

// FunctionName returns the AWS Lambda function name for a check: {prefix}-check-{name}.
func FunctionName(prefix, name string) string {
	return fmt.Sprintf("%s-check-%s", prefix, name)
}

// --- helpers ---

func defaultMemTimeout(in DeployInput) (mem, timeout int32) {
	mem = in.Memory
	if mem <= 0 {
		mem = 256
	}
	timeout = in.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	return
}

func buildEnvMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func buildZipCode(in DeployInput) *lambdatypes.FunctionCode {
	if in.S3Bucket != "" {
		return &lambdatypes.FunctionCode{
			S3Bucket: aws.String(in.S3Bucket),
			S3Key:    aws.String(in.S3Key),
		}
	}
	return &lambdatypes.FunctionCode{ZipFile: in.ZipBytes}
}
