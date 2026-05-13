package profile

// instanceRAMGiB returns the RAM size in GiB for a known EC2 instance type, and
// ok=true when the type is present in the table. Unknown types return ok=false;
// callers should fail open (skip the check) rather than reject the profile —
// the table is best-effort and intentionally narrower than every EC2 type AWS
// offers. We add entries as profiles in this repo start using new instance types.
//
// Used by ValidateSemantic for the hibernation rootVolumeSize check: AWS
// dumps RAM to the root EBS volume on suspend, so the root volume must be
// strictly larger than RAM. Mirrors the static-table approach in
// internal/app/cmd/spot_rate.go.
//
// Only instance types AWS lists as hibernation-eligible (RAM ≤ 150 GiB) need
// be present here for the check to be useful, but a few larger sizes are
// included anyway since hibernation isn't the only future use.
func instanceRAMGiB(instanceType string) (int, bool) {
	table := map[string]int{
		// t2 family
		"t2.micro": 1, "t2.small": 2, "t2.medium": 4,
		"t2.large": 8, "t2.xlarge": 16, "t2.2xlarge": 32,

		// t3 family
		"t3.micro": 1, "t3.small": 2, "t3.medium": 4,
		"t3.large": 8, "t3.xlarge": 16, "t3.2xlarge": 32,

		// t3a family
		"t3a.micro": 1, "t3a.small": 2, "t3a.medium": 4,
		"t3a.large": 8, "t3a.xlarge": 16, "t3a.2xlarge": 32,

		// t4g family (ARM)
		"t4g.micro": 1, "t4g.small": 2, "t4g.medium": 4,
		"t4g.large": 8, "t4g.xlarge": 16, "t4g.2xlarge": 32,

		// m5 family
		"m5.large": 8, "m5.xlarge": 16, "m5.2xlarge": 32,
		"m5.4xlarge": 64, "m5.8xlarge": 128,

		// c5 family
		"c5.large": 4, "c5.xlarge": 8, "c5.2xlarge": 16,
		"c5.4xlarge": 32, "c5.9xlarge": 72,

		// r5 family
		"r5.large": 16, "r5.xlarge": 32, "r5.2xlarge": 64,
		"r5.4xlarge": 128,
	}
	ram, ok := table[instanceType]
	return ram, ok
}
