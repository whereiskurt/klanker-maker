package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// IdentityRowGetItemAPI is the minimal DynamoDB GetItem view used to read the operator
// identity row. *dynamodb.Client satisfies it. (DoctorDeps.DynamoClient is
// DescribeTable-only, so a separate narrow view is required.)
type IdentityRowGetItemAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// operatorIdentitySandboxID is the identities-table hash key for the operator row.
const operatorIdentitySandboxID = "operator"

// checkOperatorIdentity verifies the operator's public-key row exists in the
// {prefix}-identities table. Without it, every operator-signed action — km slack test,
// km email --from operator — fails with unknown_sender, and the only signal an operator
// gets is a generic bridge not-OK. This check names the exact problem and the fix.
//
// It catches the drift directly regardless of cause, including the incident-2026-06-14
// case where a km init wedged on a later module (a dynamodb-slack-threads GSI backfill)
// and never reached the identity publish, AND the unattributed row-deletion seen in that
// incident (the operator's private signing key in SSM survived; only the DDB row was
// gone). The companion `km doctor --republish-operator-identity` republishes from that
// surviving SSM key.
//
// resourcePrefix is used to resolve the operator signing-key SSM path (the same
// SigningKeyPath the publisher writes). A transient DDB/SSM read error degrades to WARN
// (a read failure must never report the platform healthy).
func checkOperatorIdentity(ctx context.Context, ddb IdentityRowGetItemAPI, ssmClient SSMReadAPI, identityTable, resourcePrefix string) CheckResult {
	const name = "Operator identity"

	rowExists, hasPubKey, readErr := operatorIdentityRow(ctx, ddb, identityTable)
	if readErr != nil {
		return CheckResult{Name: name, Status: CheckWarn,
			Message: fmt.Sprintf("could not read operator row from %s: %v", identityTable, readErr)}
	}
	ssmKeyPresent := operatorSigningKeyPresent(ctx, ssmClient, resourcePrefix)

	switch {
	case hasPubKey:
		return CheckResult{Name: name, Status: CheckOK,
			Message: fmt.Sprintf("operator public-key row present in %s", identityTable)}

	case !rowExists && ssmKeyPresent:
		// The exact incident signature: private key in SSM, no public-key row in DDB.
		return CheckResult{Name: name, Status: CheckWarn,
			Message: fmt.Sprintf(
				"operator public-key row missing from %s — km slack test / km email --from operator will fail with unknown_sender. "+
					"Run 'km init' (idempotent) to republish, or 'km doctor --republish-operator-identity'.",
				identityTable)}

	case rowExists && !hasPubKey:
		return CheckResult{Name: name, Status: CheckWarn,
			Message: fmt.Sprintf(
				"operator row in %s has no public_key — operator-signed actions fail with unknown_sender. "+
					"Run 'km init' (idempotent) to republish, or 'km doctor --republish-operator-identity'.",
				identityTable)}

	default:
		// Neither the row nor the SSM signing key exists — identity never provisioned.
		return CheckResult{Name: name, Status: CheckWarn,
			Message: fmt.Sprintf(
				"operator identity not provisioned (no row in %s, no signing key in SSM) — run 'km init'.",
				identityTable)}
	}
}

// operatorIdentityRow reports whether the operator row exists and whether it carries a
// non-empty public_key. A read error is returned so the caller can WARN rather than
// silently treat a throttle/permission error as "absent".
func operatorIdentityRow(ctx context.Context, ddb IdentityRowGetItemAPI, table string) (rowExists, hasPubKey bool, err error) {
	if ddb == nil {
		return false, false, nil
	}
	out, getErr := ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: operatorIdentitySandboxID},
		},
		ProjectionExpression: aws.String("public_key"),
	})
	if getErr != nil {
		return false, false, getErr
	}
	if out == nil || len(out.Item) == 0 {
		return false, false, nil
	}
	if v, ok := out.Item["public_key"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value != "" {
			return true, true, nil
		}
	}
	return true, false, nil
}

// operatorSigningKeyPresent reports whether the operator's Ed25519 signing key exists in
// SSM at the canonical SigningKeyPath. Any error (ParameterNotFound, throttle, access)
// is treated as "absent" for the purpose of choosing the WARN wording — the check never
// fails the doctor run on it.
func operatorSigningKeyPresent(ctx context.Context, ssmClient SSMReadAPI, resourcePrefix string) bool {
	if ssmClient == nil {
		return false
	}
	out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(kmaws.SigningKeyPath(resourcePrefix, operatorIdentitySandboxID)),
		WithDecryption: aws.Bool(false),
	})
	if err != nil || out == nil || out.Parameter == nil || out.Parameter.Value == nil {
		return false
	}
	return *out.Parameter.Value != ""
}
