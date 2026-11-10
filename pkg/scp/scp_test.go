package scp

import (
	"encoding/json"
	"strings"
	"testing"
)

func baseInput() Input {
	return Input{
		ResourcePrefix:       "km",
		ApplicationAccountID: "111122223333",
		Region:               "us-east-1",
		RegionLabel:          "use1",
	}
}

func stmt(t *testing.T, p Policy, sid string) Statement {
	t.Helper()
	for _, s := range p.Statement {
		if s.Sid == sid {
			return s
		}
	}
	t.Fatalf("statement %s not found", sid)
	return Statement{}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestRender_DefaultShape(t *testing.T) {
	p, err := Render(baseInput())
	if err != nil {
		t.Fatal(err)
	}
	var sids []string
	for _, s := range p.Statement {
		sids = append(sids, s.Sid)
	}
	want := "DenyInfraAndStorage,DenyInstanceMutation,DenyIAMEscalation,DenySSMPivot,DenyOrgDiscovery,DenyOutsideRegion"
	if got := strings.Join(sids, ","); got != want {
		t.Fatalf("sids = %s, want %s", got, want)
	}
	if n := len(p.JSON()); n > MaxBytes {
		t.Fatalf("default policy is %d bytes", n)
	}
	sso := "arn:aws:iam::111122223333:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*"
	for _, sid := range []string{"DenyInfraAndStorage", "DenyInstanceMutation", "DenyIAMEscalation", "DenySSMPivot", "DenyOutsideRegion"} {
		if !contains(stmt(t, p, sid).Condition["ArnNotLike"]["aws:PrincipalARN"], sso) {
			t.Errorf("%s does not exempt operator SSO", sid)
		}
	}
	if stmt(t, p, "DenyOrgDiscovery").Condition != nil {
		t.Error("DenyOrgDiscovery must be unconditional")
	}
}

// The pre-v3 policy trusted patterns a sandbox role could match. Each must now
// be rejected, and none may appear in the rendered default.
func TestRender_RejectsPatternsASandboxRoleCanMatch(t *testing.T) {
	for _, pat := range []string{
		"arn:aws:iam::*:role/*-lifecycle-*",   // beaten by metadata.prefix: lifecycle
		"arn:aws:iam::*:role/*-provisioner-*", // beaten by metadata.prefix: provisioner
		"arn:aws:iam::*:role/*-ec2spot-ssm-*", // IS the sandbox role
		"arn:aws:iam::*:role/*",
	} {
		in := baseInput()
		in.TrustedRoles = []string{pat}
		if _, err := Render(in); err == nil || !strings.Contains(err.Error(), "sandbox instance role") {
			t.Errorf("%s: want sandbox-role rejection, got %v", pat, err)
		}
	}
	p, _ := Render(baseInput())
	doc := string(p.JSON())
	for _, gone := range []string{"-lifecycle-", "-provisioner-", "-ec2spot-ssm-", "-github-token-refresher-", "-ecs-spot-handler"} {
		if strings.Contains(doc, gone) {
			t.Errorf("default policy still trusts %s", gone)
		}
	}
}

func TestRender_ExtrasLandOnlyWhereScoped(t *testing.T) {
	in := baseInput()
	all := "arn:aws:iam::111122223333:role/ci-deploy"
	iamOnly := "arn:aws:iam::111122223333:role/kmv-github-deploy"
	ssmOnly := "arn:aws:iam::111122223333:role/ops-runbook"
	in.TrustedRoles = []string{all}
	in.TrustedIAMRoles = []string{iamOnly}
	in.TrustedSSMRoles = []string{ssmOnly}
	in.ExtraRegions = []string{"ca-central-1"}
	p, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	for sid, want := range map[string][3]bool{ // all, iamOnly, ssmOnly
		"DenyInfraAndStorage":  {true, false, false},
		"DenyInstanceMutation": {true, false, false},
		"DenyIAMEscalation":    {true, true, false},
		"DenySSMPivot":         {false, false, true},
		"DenyOutsideRegion":    {true, false, false},
	} {
		got := stmt(t, p, sid).Condition["ArnNotLike"]["aws:PrincipalARN"]
		for i, arn := range []string{all, iamOnly, ssmOnly} {
			if contains(got, arn) != want[i] {
				t.Errorf("%s: trusts %s = %v, want %v", sid, arn, contains(got, arn), want[i])
			}
		}
	}
	regions := stmt(t, p, "DenyOutsideRegion").Condition["StringNotEquals"]["aws:RequestedRegion"]
	if strings.Join(regions, ",") != "us-east-1,ca-central-1" {
		t.Errorf("regions = %v", regions)
	}
}

func TestRender_RejectsMalformedInput(t *testing.T) {
	cases := map[string]func(*Input){
		"bad arn":       func(in *Input) { in.TrustedIAMRoles = []string{"kmv-github-deploy"} },
		"user not role": func(in *Input) { in.TrustedRoles = []string{"arn:aws:iam::111122223333:user/bob"} },
		"bad region":    func(in *Input) { in.ExtraRegions = []string{"canada"} },
		"no account":    func(in *Input) { in.ApplicationAccountID = "" },
		"no region":     func(in *Input) { in.Region = "" },
	}
	for name, mut := range cases {
		in := baseInput()
		mut(&in)
		if _, err := Render(in); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestRender_SizeGuard(t *testing.T) {
	in := baseInput()
	for i := 0; i < 80; i++ {
		in.TrustedRoles = append(in.TrustedRoles, "arn:aws:iam::111122223333:role/some-long-role-name-number-"+string(rune('a'+i%26))+strings.Repeat("x", i))
	}
	if _, err := Render(in); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("want size error, got %v", err)
	}
}

func TestRender_RegionExemptionsCoverInstanceRole(t *testing.T) {
	p, _ := Render(baseInput())
	na := stmt(t, p, "DenyOutsideRegion").NotAction
	for _, a := range []string{"bedrock:*", "bedrock-mantle:*", "aws-marketplace:*", "sts:*"} {
		if !contains(na, a) {
			t.Errorf("region lock does not exempt %s", a)
		}
	}
	if contains(stmt(t, p, "DenyInfraAndStorage").Action, "ec2:CreateTags") {
		t.Error("ec2:CreateTags must not be denied (RunInstances with tags needs it)")
	}
}

func TestArnLike(t *testing.T) {
	cases := []struct {
		p, v string
		ok   bool
	}{
		{"arn:aws:iam::*:role/*-ttl-handler", "arn:aws:iam::1:role/km-ttl-handler", true},
		{"arn:aws:iam::*:role/*-ttl-handler", "arn:aws:iam::1:role/km-ttl-handler-x", false},
		{"arn:aws:iam::*:role/*-lifecycle-*", "arn:aws:iam::1:role/km-ec2spot-ssm-lifecycle-0123abcd-use1", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"*", "", true},
	}
	for _, c := range cases {
		if got := ArnLike(c.p, c.v); got != c.ok {
			t.Errorf("ArnLike(%q,%q)=%v", c.p, c.v, got)
		}
	}
}

func TestCompare(t *testing.T) {
	in := baseInput()
	in.TrustedIAMRoles = []string{"arn:aws:iam::111122223333:role/kmv-github-deploy"}
	want, _ := Render(in)
	live, _ := Render(baseInput())

	// Reorder and reformat the same document: still equivalent.
	var generic map[string]any
	_ = json.Unmarshal(want.JSON(), &generic)
	stmts := generic["Statement"].([]any)
	stmts[0], stmts[5] = stmts[5], stmts[0]
	reordered, _ := json.MarshalIndent(generic, "", "    ")
	if d, err := Compare(want.JSON(), reordered); err != nil || len(d) != 0 {
		t.Fatalf("reordered doc differs: %v %v", d, err)
	}

	d, err := Compare(want.JSON(), live.JSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 || d[0].Sid != "DenyIAMEscalation" || len(d[0].Missing) != 1 ||
		!strings.Contains(d[0].Missing[0], "kmv-github-deploy") || len(d[0].Extra) != 0 {
		t.Fatalf("diff = %+v", d)
	}
}

// AWS returns a single condition value as a bare string and condition keys in
// whatever case the author wrote; both must compare equal.
func TestCompare_SingleValueAndKeyCase(t *testing.T) {
	a := []byte(`{"Statement":[{"Sid":"X","Effect":"Deny","Action":["a:b"],"Resource":"*","Condition":{"StringNotEquals":{"aws:RequestedRegion":["us-east-1"]}}}]}`)
	b := []byte(`{"Statement":[{"Sid":"X","Effect":"Deny","Action":"a:b","Resource":["*"],"Condition":{"StringNotEquals":{"aws:requestedregion":"us-east-1"}}}]}`)
	if d, err := Compare(a, b); err != nil || len(d) != 0 {
		t.Fatalf("got %v %v", d, err)
	}
}
