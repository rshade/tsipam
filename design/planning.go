package design

import (
	. "goa.design/goa/v3/dsl" //nolint:staticcheck,revive // goa's DSL is designed for dot-import
)

//nolint:gochecknoglobals // goa requires a package-level declaration for each API type.
var ComparisonEntry = Type("ComparisonEntry", func() {
	Description("One allocation and its registry-versus-live classification.")
	Attribute("kind", String, "Allocation primitive.", func() {
		Enum("site_id", "subnet_pool", "service_address")
	})
	Attribute("state", String, "Comparison classification.", func() {
		Enum("matched", "reserved_but_unadvertised", "unreserved_but_live", "conflict")
	})
	Attribute("key", String, "Numeric site ID, subnet CIDR, or service IPv4 address.")
	Attribute("expected", String, "Registry site label or service name, when reserved.")
	Attribute("observed", ArrayOf(String), "Sorted unique live device or service names.")
	Attribute("routes", ArrayOf(String), "Sorted unique advertised routes supporting the comparison.")
	Required("kind", "state", "key", "observed", "routes")
})

//nolint:gochecknoglobals // goa requires a package-level declaration for each API type.
var ComparisonReport = Type("ComparisonReport", func() {
	Description("A complete comparison of one registry snapshot and live allocation inventory.")
	Attribute("live_tailnet", String, "Live control-plane tailnet, independent of the registry alias.")
	Attribute("drift_count", Int, "Number of non-matched entries.", func() { Minimum(0) })
	Attribute("entries", ArrayOf(ComparisonEntry), "Deterministically ordered comparison entries.")
	Attribute("excluded", ArrayOf(String), "Allocation kinds with no live counterpart: udp_port.")
	Required("live_tailnet", "drift_count", "entries", "excluded")
})

func planningMethod() {
	Method("plan", func() {
		Description("Compare declared allocations with live control-plane state without modifying either.")
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("live_tailnet", String, "Tailscale tailnet whose live allocations are collected.")
			Required("tailnet", "live_tailnet")
		})
		Result(ComparisonReport)
		Error("comparison_failed", func() {
			Description("Registry or live inventory could not be compared completely.")
		})
		HTTP(func() {
			GET("/tailnet/{tailnet}/ipam/plan")
			Param("live_tailnet")
			Response(StatusOK)
			Response("comparison_failed", StatusBadGateway)
		})
	})
}
