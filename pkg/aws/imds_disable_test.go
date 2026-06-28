package aws_test

import "os"

// Tests in this package construct AWS clients; disabling EC2 instance-metadata
// probing keeps config.LoadDefaultConfig from paying a ~30s IMDS timeout
// off-instance, and static dummy creds short-circuit credential discovery.
// Mirrors internal/app/cmd/main_test.go. No real AWS call is made — tests mock
// at the client seam or fail before the call.
func init() {
	os.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	os.Setenv("AWS_ACCESS_KEY_ID", "testing")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "testing")
	os.Setenv("AWS_SESSION_TOKEN", "testing")
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
}
