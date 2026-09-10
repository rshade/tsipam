package design

import (
	. "goa.design/goa/v3/dsl" //nolint:staticcheck,revive // goa's DSL is designed for dot-import
)

//nolint:gochecknoglobals // goa requires a package-level declaration for each API type.
var PolicyAuthored = Type("PolicyAuthored", func() {
	Description("One effective authored CIDR and its containing reservations.")
	Attribute("ipset", String, "Defining ipset name.", func() { Meta("struct:field:name", "IPSet") })
	Attribute("cidr", String, "Canonical effective CIDR.", func() { Meta("struct:field:name", "CIDR") })
	Attribute("state", String, "Authored classification.", func() { Enum("matched", "authored_but_unreserved") })
	Attribute("pools", ArrayOf(String), "Sorted containing reservation CIDRs.")
	Required("ipset", "cidr", "state", "pools")
})

//nolint:gochecknoglobals // goa requires a package-level declaration for each API type.
var PolicyReserved = Type("PolicyReserved", func() {
	Description("One reserved subnet pool and its referencing ipsets.")
	Attribute("cidr", String, "Reserved CIDR.", func() { Meta("struct:field:name", "CIDR") })
	Attribute("site", String, "Registry site label.")
	Attribute("state", String, "Reserved classification.", func() { Enum("matched", "reserved_but_unreferenced") })
	Attribute("ipsets", ArrayOf(String), "Sorted ipsets with overlapping effective address space.", func() {
		Meta("struct:field:name", "IPSets")
	})
	Required("cidr", "site", "state", "ipsets")
})

//nolint:gochecknoglobals // goa requires a package-level declaration for each API type.
var PolicyComparison = Type("PolicyComparison", func() {
	Description("Complete static comparison of authored ipsets and reserved subnet pools.")
	Attribute("drift_count", Int, "Number of unmatched rows in both directions.", func() { Minimum(0) })
	Attribute("authored", ArrayOf(PolicyAuthored), "Rows sorted by ipset name, then CIDR address and length.")
	Attribute("reserved", ArrayOf(PolicyReserved), "Rows sorted by CIDR address and length.")
	Required("drift_count", "authored", "reserved")
})

func policyCheckMethod() {
	Method("check_policy", func() {
		Description("Compare static ipset definitions with subnet reservations without modifying either.")
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("policy", String, "Local HuJSON policy contents; no server-side file path is accepted.")
			Required("tailnet", "policy")
		})
		Result(PolicyComparison)
		Error("policy_check_failed", func() {
			Description("Policy or registry input could not be compared completely.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/check-policy")
			Response(StatusOK)
			Response("policy_check_failed", StatusBadRequest)
		})
	})
}
