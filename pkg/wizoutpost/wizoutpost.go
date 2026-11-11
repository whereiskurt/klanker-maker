// Package wizoutpost holds the settings, validation, and CloudFormation
// parameter mapping for the Wiz Outpost cloud-connector stack that km deploys
// from km-config.yaml. It makes no AWS calls.
package wizoutpost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// TemplateRelPath is the vendored Wiz template, relative to the repo root.
const TemplateRelPath = "infra/modules/wiz-outpost-connector/v1.1.0/wiz-outpost-connector.json"

// MaxInlineTemplateBytes is CloudFormation's TemplateBody size limit.
const MaxInlineTemplateBytes = 51200

const defaultAccessRoleName = "WizAccess-Role"

// Scanning toggles optional scan capabilities. A nil value keeps the
// template's own default.
type Scanning struct {
	Data             *bool `mapstructure:"data" yaml:"data,omitempty"`
	EKS              *bool `mapstructure:"eks" yaml:"eks,omitempty"`
	Lightsail        *bool `mapstructure:"lightsail" yaml:"lightsail,omitempty"`
	Serverless       *bool `mapstructure:"serverless" yaml:"serverless,omitempty"`
	CloudCost        *bool `mapstructure:"cloud_cost" yaml:"cloud_cost,omitempty"`
	RDSLogs          *bool `mapstructure:"rds_logs" yaml:"rds_logs,omitempty"`
	S3AWSLogs        *bool `mapstructure:"s3_aws_logs" yaml:"s3_aws_logs,omitempty"`
	S3KMSDecrypt     *bool `mapstructure:"s3_kms_decrypt" yaml:"s3_kms_decrypt,omitempty"`
	TerraformBuckets *bool `mapstructure:"terraform_buckets" yaml:"terraform_buckets,omitempty"`
	S3Inventory      *bool `mapstructure:"s3_inventory" yaml:"s3_inventory,omitempty"`
}

// Settings is the wiz.outpost block of km-config.yaml.
type Settings struct {
	TrustRoleARN          string   `mapstructure:"trust_role_arn" yaml:"trust_role_arn,omitempty"`
	OrchestratorAccountID string   `mapstructure:"orchestrator_account_id" yaml:"orchestrator_account_id,omitempty"`
	ExternalID            string   `mapstructure:"external_id" yaml:"external_id,omitempty"`
	RoleSuffix            string   `mapstructure:"role_suffix" yaml:"role_suffix,omitempty"`
	AccessRoleName        string   `mapstructure:"access_role_name" yaml:"access_role_name,omitempty"`
	Scanning              Scanning `mapstructure:"scanning" yaml:"scanning,omitempty"`
}

// RequiredParameters lists every template parameter this package sets or
// relies on (the contract test checks each exists in the vendored template).
var RequiredParameters = []string{
	"ExternalId", "RoleARN", "OrchestratorAccountID", "RoleSuffix", "WizRoleName", "isOrg",
	"CloudCostScanning", "DataScanning", "EKSScanning", "LightsailScanning", "RDSLogsScanning",
	"S3AWSLogsScanning", "S3KMSDecrypt", "ServerlessScanning", "TerraformBucketScanning", "S3InventoryScanning",
	"WizVersionLastUpdated",
}

// RequiredOutputs lists the stack outputs later tasks read.
var RequiredOutputs = []string{"WizAccessRoleARNID", "WizScannerRoleARNID", "ExternalID"}

var (
	roleARNRe    = regexp.MustCompile(`^arn:aws(-us-gov|-cn)?:iam::\d{12}:role/.+$`)
	accountRe    = regexp.MustCompile(`^\d{12}$`)
	externalIDRe = regexp.MustCompile(`^\S{8}-\S{4}-\S{4}-\S{4}-\S{12}$`)
)

// Validate checks required fields; errors name the full yaml key.
func (s Settings) Validate() error {
	if s.TrustRoleARN == "" {
		return fmt.Errorf("wiz.outpost.trust_role_arn is required")
	}
	if !roleARNRe.MatchString(s.TrustRoleARN) {
		return fmt.Errorf("wiz.outpost.trust_role_arn %q is not an IAM role ARN", s.TrustRoleARN)
	}
	if s.OrchestratorAccountID == "" {
		return fmt.Errorf("wiz.outpost.orchestrator_account_id is required")
	}
	if !accountRe.MatchString(s.OrchestratorAccountID) {
		return fmt.Errorf("wiz.outpost.orchestrator_account_id %q must be a 12-digit account id", s.OrchestratorAccountID)
	}
	if s.ExternalID == "" {
		return fmt.Errorf("wiz.outpost.external_id is required")
	}
	if !externalIDRe.MatchString(s.ExternalID) {
		return fmt.Errorf("wiz.outpost.external_id %q must look like xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", s.ExternalID)
	}
	if s.RoleSuffix == "" {
		return fmt.Errorf("wiz.outpost.role_suffix is required")
	}
	return nil
}

// AccessRoleNameOrDefault returns access_role_name or "WizAccess-Role".
func (s Settings) AccessRoleNameOrDefault() string {
	if s.AccessRoleName == "" {
		return defaultAccessRoleName
	}
	return s.AccessRoleName
}

// ScannerRoleName is the scanner role the template creates.
func (s Settings) ScannerRoleName() string { return "WizScannerRole-" + s.RoleSuffix }

// StackName is the CloudFormation stack name for an install prefix.
func StackName(prefix string) string { return prefix + "-wiz-outpost-connector" }

func toggle(v *bool, def bool) string {
	if v != nil {
		def = *v
	}
	if def {
		return "Enabled"
	}
	return "Disabled"
}

// Parameters maps the settings to CloudFormation parameters. Defaults mirror
// the template's own.
func (s Settings) Parameters() map[string]string {
	sc := s.Scanning
	return map[string]string{
		"ExternalId":              s.ExternalID,
		"RoleARN":                 s.TrustRoleARN,
		"OrchestratorAccountID":   s.OrchestratorAccountID,
		"RoleSuffix":              s.RoleSuffix,
		"WizRoleName":             s.AccessRoleNameOrDefault(),
		"isOrg":                   "Disabled",
		"CloudCostScanning":       toggle(sc.CloudCost, true),
		"DataScanning":            toggle(sc.Data, false),
		"EKSScanning":             toggle(sc.EKS, false),
		"LightsailScanning":       toggle(sc.Lightsail, false),
		"RDSLogsScanning":         toggle(sc.RDSLogs, true),
		"S3AWSLogsScanning":       toggle(sc.S3AWSLogs, true),
		"S3KMSDecrypt":            toggle(sc.S3KMSDecrypt, true),
		"ServerlessScanning":      toggle(sc.Serverless, true),
		"TerraformBucketScanning": toggle(sc.TerraformBuckets, true),
		"S3InventoryScanning":     toggle(sc.S3Inventory, false),
	}
}

// ExportJSON renders {"parameters":{...}} compactly with sorted keys.
func ExportJSON(s Settings) (string, error) {
	b, err := json.Marshal(map[string]map[string]string{"parameters": s.Parameters()})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TemplateVersion returns the vendored template's WizVersionLastUpdated
// parameter default (the version km deploys), read from repoRoot.
func TemplateVersion(repoRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, TemplateRelPath))
	if err != nil {
		return "", fmt.Errorf("read wiz template: %w", err)
	}
	var t struct {
		Parameters map[string]struct{ Default string }
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", fmt.Errorf("parse wiz template: %w", err)
	}
	p, ok := t.Parameters["WizVersionLastUpdated"]
	if !ok || p.Default == "" {
		return "", fmt.Errorf("wiz template has no WizVersionLastUpdated default")
	}
	return p.Default, nil
}
