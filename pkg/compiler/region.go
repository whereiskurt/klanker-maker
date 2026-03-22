package compiler

import "strings"

// RegionLabel converts a full AWS region name to a short label.
// Examples: us-east-1 -> use1, ca-central-1 -> cac1, ap-southeast-1 -> apse1
func RegionLabel(region string) string {
	parts := strings.Split(region, "-")
	if len(parts) < 3 {
		return region
	}

	// Take first char of each part + last digit
	// us-east-1 -> u + e + 1 = ue1... but defcon pattern is use1
	// Let's use: first two chars of direction + last digit
	direction := parts[0]  // us, eu, ap, ca, me, af, sa
	area := parts[1]       // east, west, central, south, north, southeast, northeast
	number := parts[2]     // 1, 2, 3

	// Abbreviate area
	areaShort := area
	switch area {
	case "east":
		areaShort = "e"
	case "west":
		areaShort = "w"
	case "central":
		areaShort = "c"
	case "south":
		areaShort = "s"
	case "north":
		areaShort = "n"
	case "southeast":
		areaShort = "se"
	case "northeast":
		areaShort = "ne"
	case "northwest":
		areaShort = "nw"
	case "southwest":
		areaShort = "sw"
	}

	return direction + areaShort + number
}
