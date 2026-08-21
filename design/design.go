// Package design is the goa DSL source for tail-ipam's ipam service. It is
// not compiled into the tail-ipam binary; `goa gen`/`goa example` read it to
// produce ../gen and the service scaffolding. See ../README.md.
package design

import (
	. "goa.design/goa/v3/dsl" //nolint:staticcheck,revive // goa's DSL is designed for dot-import
)

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var SiteIDReservation = Type("SiteIDReservation", func() {
	Description("A reserved 4via6 site ID.")
	Attribute("id", String, "Canonical site ID: a decimal integer, 0-65535.")
	Attribute("site", String, "Site name reserved for this ID.")
	Required("id", "site")
})

var _ = API("tail-ipam", func() {
	Title("tail-ipam address-space registry API")
	Description("Plans and allocates Tailscale address space, starting with 4via6 site IDs. " +
		"Paths are tailnet-scoped (/tailnet/{tailnet}/ipam/...) to mirror Tailscale's own " +
		"control-plane API convention.")
})

var _ = Service("ipam", func() {
	Description("Reserve, release, and list address-space allocations for one tailnet's registry.")

	Method("list_site_ids", func() {
		Payload(func() {
			Attribute("tailnet", String,
				"Routing/storage key for the registry. May be a synthetic alias, not "+
					"necessarily the live Tailscale tailnet name.")
			Required("tailnet")
		})
		Result(ArrayOf(SiteIDReservation))
		HTTP(func() {
			GET("/tailnet/{tailnet}/ipam/site-ids")
			Response(StatusOK)
		})
	})

	Method("reserve_site_id", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("id", String, "4via6 site ID to reserve.")
			Attribute("site", String, "Site name to reserve it for.")
			Required("tailnet", "id", "site")
		})
		Result(func() {
			Attribute("id", String, "Canonical site ID actually written.")
			Attribute("site", String, "Canonical site name actually written.")
			Attribute("changed", Boolean, "False when the exact id+site pair was already reserved.")
			Required("id", "site", "changed")
		})
		Error("collision", func() {
			Description("id is already reserved for a different site.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/site-ids")
			Response(StatusOK)
			Response("collision", StatusConflict)
		})
	})

	Method("release_site_id", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("id", String, "4via6 site ID to release.")
			Required("tailnet", "id")
		})
		Result(func() {
			Attribute("id", String, "Canonical site ID released.")
			Attribute("changed", Boolean, "False when id was not reserved.")
			Required("id", "changed")
		})
		HTTP(func() {
			DELETE("/tailnet/{tailnet}/ipam/site-ids/{id}")
			Response(StatusOK)
		})
	})
})
