package planreport

import "slices"

// GateResult is the output of Evaluate: whether the plan is blocked and
// which resources tripped the gate.
type GateResult struct {
	Blocked bool
	Trips   []Trip
}

// Trip represents a single resource that tripped the destroy-class gate.
//
// Action is one of "DESTROY", "REPLACE", "PARSE-FAIL". Type and Address are
// empty for parse-fail trips; Reason is populated only for parse-fail trips.
type Trip struct {
	Module  string
	Type    string // empty when ParseFailed
	Address string // empty when ParseFailed
	Action  string // "DESTROY" | "REPLACE" | "PARSE-FAIL"
	Reason  string // populated for parse-fail trips
}

// Evaluate runs the destroy-class gate over a slice of Reports.
//
// The algorithm is locked (CONTEXT.md decisions lines 117-135): any destroy OR
// replace of a type in ProtectedTypes appends a Trip; ParseFailed reports are
// conservative-tripped with Action="PARSE-FAIL". When acceptDestroys=true,
// Blocked is forced false but the Trips slice is STILL populated so the caller
// can print the override-active list (operator visibility contract).
func Evaluate(reports []Report, acceptDestroys bool) GateResult {
	var trips []Trip
	for _, r := range reports {
		if r.ParseFailed {
			trips = append(trips, Trip{
				Module: r.Module,
				Action: "PARSE-FAIL",
				Reason: "plan JSON parse failed — conservative trip",
			})
			continue
		}
		for _, ch := range r.Destroys {
			if slices.Contains(ProtectedTypes, ch.Type) {
				trips = append(trips, Trip{
					Module:  r.Module,
					Type:    ch.Type,
					Address: ch.Address,
					Action:  "DESTROY",
				})
			}
		}
		for _, ch := range r.Replaces {
			if slices.Contains(ProtectedTypes, ch.Type) {
				trips = append(trips, Trip{
					Module:  r.Module,
					Type:    ch.Type,
					Address: ch.Address,
					Action:  "REPLACE",
				})
			}
		}
	}
	blocked := len(trips) > 0 && !acceptDestroys
	return GateResult{Blocked: blocked, Trips: trips}
}
