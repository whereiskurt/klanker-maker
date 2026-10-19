package bridge_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func awsString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

var ifNotExistsClause = regexp.MustCompile(`(\w+)\s*=\s*if_not_exists\(\s*(\w+)\s*,\s*(:\w+)\s*\)`)

// applyIfNotExistsSet evaluates an UpdateExpression of the form
// "SET a = if_not_exists(a, :x), b = if_not_exists(b, :y)" against row, the way
// DynamoDB would. Any other clause shape fails the test, so a later change to
// the expression cannot slip past these semantic checks unexamined.
func applyIfNotExistsSet(t *testing.T, row map[string]dynamodbtypes.AttributeValue, in *dynamodb.UpdateItemInput) {
	t.Helper()
	if in.ConditionExpression != nil {
		t.Fatalf("unexpected ConditionExpression %q", *in.ConditionExpression)
	}
	expr := strings.TrimSpace(awsString(in.UpdateExpression))
	if !strings.HasPrefix(expr, "SET ") {
		t.Fatalf("UpdateExpression %q: want a SET expression", expr)
	}
	body := strings.TrimPrefix(expr, "SET ")
	matches := ifNotExistsClause.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("UpdateExpression %q: want attr = if_not_exists(attr, :v) clauses", expr)
	}
	if rest := strings.Trim(ifNotExistsClause.ReplaceAllString(body, ""), ", "); rest != "" {
		t.Fatalf("UpdateExpression %q has clauses other than attr = if_not_exists(attr, :v): %q", expr, rest)
	}
	for _, m := range matches {
		if m[1] != m[2] {
			t.Fatalf("UpdateExpression clause %q: attribute and if_not_exists target differ", m[0])
		}
		v, ok := in.ExpressionAttributeValues[m[3]]
		if !ok {
			t.Fatalf("UpdateExpression references %s with no ExpressionAttributeValues entry", m[3])
		}
		if _, exists := row[m[1]]; !exists {
			row[m[1]] = v
		}
	}
}
