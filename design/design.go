// Package design is the goa DSL source for tail-ipam's ipam service. It is
// not compiled into the tail-ipam binary; `goa gen`/`goa example` read it to
// produce ../gen and the service scaffolding. See ../README.md.
package design

import (
	. "goa.design/goa/v3/dsl" //nolint:staticcheck,revive // goa's DSL is designed for dot-import
)

// reservationMetadataAttributes declares the optional ownership/tracking
// attributes shared by every reservation payload and result: owner,
// description, ticket, and reserved_at. Reusing this one function is what
// keeps the four fields from drifting between SiteIDReservation,
// SubnetPoolReservation, ServiceAddressReservation, and each reserve method's
// payload/result -- goa's DSL has no attribute-set inheritance, so a shared
// helper is the only way to declare the same optional fields in more than one
// place without hand-typing them apart.
//
//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var reservationMetadataAttributes = func() {
	Attribute("owner", String, "Operator or team that owns this reservation.")
	Attribute("description", String, "Free-text description of why this address space is reserved.")
	Attribute("ticket", String, "Tracking ticket or issue reference for this reservation.")
	Attribute("reserved_at", String, "RFC 3339 timestamp this reservation's metadata was recorded.", func() {
		Format(FormatDateTime)
	})
}

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var SiteIDReservation = Type("SiteIDReservation", func() {
	Description("A reserved 4via6 site ID.")
	Attribute("id", String, "Canonical site ID: a decimal integer, 0-65535.")
	Attribute("site", String, "Site name reserved for this ID.")
	reservationMetadataAttributes()
	Required("id", "site")
})

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var SubnetPoolReservation = Type("SubnetPoolReservation", func() {
	Description("A subnet CIDR pool reserved for a site.")
	Attribute("cidr", String, "Canonical IPv4 or IPv6 CIDR prefix.", func() {
		Meta("struct:field:name", "CIDR")
	})
	Attribute("site", String, "Site name reserved for this subnet pool.")
	reservationMetadataAttributes()
	Required("cidr", "site")
})

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var ServiceAddressReservation = Type("ServiceAddressReservation", func() {
	Description("An IPv4 host address reserved for a Tailscale Service.")
	Attribute("address", String, "Canonical bare IPv4 host address.")
	Attribute("service", String, "Canonical svc:<name> service identity.")
	reservationMetadataAttributes()
	Required("address", "service")
})

var _ = API("tail-ipam", func() {
	Title("tail-ipam address-space registry API")
	Description("Plans and allocates Tailscale address space through 4via6 site IDs, subnet CIDR pools, " +
		"and per-service IPv4 addresses. " +
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
			reservationMetadataAttributes()
			Required("tailnet", "id", "site")
		})
		Result(func() {
			Attribute("id", String, "Canonical site ID actually written.")
			Attribute("site", String, "Canonical site name actually written.")
			Attribute("changed", Boolean, "False when the exact id+site pair and metadata were already reserved.")
			reservationMetadataAttributes()
			Required("id", "site", "changed")
		})
		Error("collision", func() {
			Description("id is already reserved for a different site.")
		})
		Error("invalid_input", func() {
			Description("id, site, or reservation metadata is invalid.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/site-ids")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
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
		Error("invalid_input", func() {
			Description("id is not a valid 4via6 site ID.")
		})
		HTTP(func() {
			DELETE("/tailnet/{tailnet}/ipam/site-ids/{id}")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
		})
	})

	Method("list_subnet_pools", func() {
		Payload(func() {
			Attribute("tailnet", String,
				"Routing/storage key for the registry. May be a synthetic alias, not "+
					"necessarily the live Tailscale tailnet name.")
			Required("tailnet")
		})
		Result(ArrayOf(SubnetPoolReservation))
		HTTP(func() {
			GET("/tailnet/{tailnet}/ipam/subnet-pools")
			Response(StatusOK)
		})
	})

	Method("reserve_subnet_pool", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("cidr", String, "Canonical IPv4 or IPv6 CIDR prefix to reserve.", func() {
				Meta("struct:field:name", "CIDR")
			})
			Attribute("site", String, "Site name to reserve it for.")
			reservationMetadataAttributes()
			Required("tailnet", "cidr", "site")
		})
		Result(func() {
			Attribute("cidr", String, "Canonical subnet CIDR actually written.", func() {
				Meta("struct:field:name", "CIDR")
			})
			Attribute("site", String, "Canonical site name actually written.")
			Attribute("changed", Boolean, "False when the exact CIDR, site, and metadata were already reserved.")
			reservationMetadataAttributes()
			Required("cidr", "site", "changed")
		})
		Error("collision", func() {
			Description("cidr overlaps a subnet pool reserved for a different site.")
		})
		Error("invalid_input", func() {
			Description("cidr, site, or reservation metadata is invalid.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/subnet-pools")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
			Response("collision", StatusConflict)
		})
	})

	Method("release_subnet_pool", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("cidr", String, "Canonical IPv4 or IPv6 CIDR prefix to release.", func() {
				Meta("struct:field:name", "CIDR")
			})
			Required("tailnet", "cidr")
		})
		Result(func() {
			Attribute("cidr", String, "Canonical subnet CIDR released.", func() {
				Meta("struct:field:name", "CIDR")
			})
			Attribute("changed", Boolean, "False when cidr was not reserved.")
			Required("cidr", "changed")
		})
		Error("invalid_input", func() {
			Description("cidr is not a valid IPv4 or IPv6 CIDR prefix.")
		})
		HTTP(func() {
			// cidr is a query parameter, not a path segment: a CIDR contains "/"
			// and would split across path segments if it were one.
			DELETE("/tailnet/{tailnet}/ipam/subnet-pools")
			Param("cidr")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
		})
	})

	Method("list_service_addresses", func() {
		Payload(func() {
			Attribute("tailnet", String,
				"Routing/storage key for the registry. May be a synthetic alias, not "+
					"necessarily the live Tailscale tailnet name.")
			Required("tailnet")
		})
		Result(ArrayOf(ServiceAddressReservation))
		HTTP(func() {
			GET("/tailnet/{tailnet}/ipam/service-addresses")
			Response(StatusOK)
		})
	})

	Method("reserve_service_address", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("address", String, "Bare IPv4 host or exact IPv4 /32 prefix to reserve.")
			Attribute("service", String, "Bare service name or canonical svc:<name> identity.")
			reservationMetadataAttributes()
			Required("tailnet", "address", "service")
		})
		Result(func() {
			Attribute("address", String, "Canonical bare IPv4 host address actually written.")
			Attribute("service", String, "Canonical svc:<name> service identity actually written.")
			Attribute("changed", Boolean,
				"False when the exact address, service, and metadata were already reserved.")
			reservationMetadataAttributes()
			Required("address", "service", "changed")
		})
		Error("collision", func() {
			Description("address is already reserved for a different service.")
		})
		Error("invalid_input", func() {
			Description("address, service, or reservation metadata is invalid.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/service-addresses")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
			Response("collision", StatusConflict)
		})
	})

	Method("release_service_address", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("address", String, "Bare IPv4 host or exact IPv4 /32 prefix to release.")
			Required("tailnet", "address")
		})
		Result(func() {
			Attribute("address", String, "Canonical bare IPv4 host address released.")
			Attribute("changed", Boolean, "False when address was not reserved.")
			Required("address", "changed")
		})
		Error("invalid_input", func() {
			Description("address is not a bare IPv4 host or exact IPv4 /32 prefix.")
		})
		HTTP(func() {
			// address is a query parameter because an accepted /32 spelling
			// contains a slash and cannot safely occupy one path segment.
			DELETE("/tailnet/{tailnet}/ipam/service-addresses")
			Param("address")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
		})
	})
})
