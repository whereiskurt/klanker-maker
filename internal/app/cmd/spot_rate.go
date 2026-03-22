package cmd

// staticSpotRate returns a conservative static hourly spot price (USD) for
// common EC2 instance types. Used as a fallback when the AWS Pricing API is
// unavailable or returns no data (GetSpotRate uses GetProducts which returns
// on-demand pricing; actual spot prices require DescribeSpotPriceHistory).
//
// Rates are approximate us-east-1 spot prices as of early 2026.
// Unknown instance types return a conservative default of $0.10/hr to ensure
// budget enforcement is not catastrophically wrong for unrecognized instance families.
func staticSpotRate(instanceType string) float64 {
	rates := map[string]float64{
		// t3 family
		"t3.nano":    0.0014,
		"t3.micro":   0.0026,
		"t3.small":   0.0052,
		"t3.medium":  0.0104,
		"t3.large":   0.0209,
		"t3.xlarge":  0.0418,
		"t3.2xlarge": 0.0835,

		// t3a family
		"t3a.nano":    0.0013,
		"t3a.micro":   0.0025,
		"t3a.small":   0.0050,
		"t3a.medium":  0.0100,
		"t3a.large":   0.0200,
		"t3a.xlarge":  0.0400,
		"t3a.2xlarge": 0.0800,

		// c5 family
		"c5.large":    0.0305,
		"c5.xlarge":   0.0609,
		"c5.2xlarge":  0.1219,
		"c5.4xlarge":  0.2437,
		"c5.9xlarge":  0.5483,
		"c5.18xlarge": 1.0966,

		// m5 family
		"m5.large":    0.0350,
		"m5.xlarge":   0.0700,
		"m5.2xlarge":  0.1400,
		"m5.4xlarge":  0.2800,
		"m5.8xlarge":  0.5600,
		"m5.12xlarge": 0.8400,
		"m5.16xlarge": 1.1200,
		"m5.24xlarge": 1.6800,

		// r5 family
		"r5.large":    0.0503,
		"r5.xlarge":   0.1006,
		"r5.2xlarge":  0.2012,
		"r5.4xlarge":  0.4024,
		"r5.8xlarge":  0.8048,
		"r5.12xlarge": 1.2071,
		"r5.16xlarge": 1.6095,
		"r5.24xlarge": 2.4143,

		// g4dn GPU family
		"g4dn.xlarge":   0.1578,
		"g4dn.2xlarge":  0.2254,
		"g4dn.4xlarge":  0.3612,
		"g4dn.8xlarge":  0.6515,
		"g4dn.16xlarge": 1.3030,
	}

	if rate, ok := rates[instanceType]; ok {
		return rate
	}

	// Conservative fallback for unknown types — $0.10/hr ensures budget enforcement
	// is not catastrophically wrong for unrecognized instance families.
	return 0.10
}
