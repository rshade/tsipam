// Package design is the goa DSL source for tsipam's ipam service. It is
// not compiled into the tsipam binary; `goa gen`/`goa example` read it to
// produce ../gen and the service scaffolding. See ../README.md.
package design

import (
	. "goa.design/goa/v3/dsl" //nolint:staticcheck,revive // goa's DSL is designed for dot-import
)

// reservationMetadataAttributes declares the optional ownership/tracking
// attributes shared by every reservation payload and result: owner,
// description, ticket, and reserved_at. Reusing this one function is what
// keeps the four fields from drifting between SiteIDReservation,
// SubnetPoolReservation, ServiceAddressReservation, and each metadata-bearing reserve method's
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

// dryRunAttribute declares the rehearsal switch every mutating payload takes.
//
//nolint:gochecknoglobals // shared attribute declaration, like reservationMetadataAttributes; this is design source, not application state.
var dryRunAttribute = func() {
	Attribute("dry_run", Boolean, "Report the outcome a real call would have and change nothing.", func() {
		Default(false)
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

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var UDPPortReservation = Type("UDPPortReservation", func() {
	Description("A UDP listen port reserved for a device within one site/firewall boundary.")
	Attribute("site", String, "Canonical site/firewall-boundary label.")
	Attribute("port", String, "Canonical decimal UDP listen port, 1024-65535.")
	Attribute("device", String, "Canonical device label that owns the port within the site.")
	Required("site", "port", "device")
})

var _ = API("tsipam", func() {
	Title("tsipam address-space registry API")
	Description("Plans and allocates Tailscale address space through 4via6 site IDs, subnet CIDR pools, " +
		"per-service IPv4 addresses, and per-device UDP listen ports. " +
		"Paths are tailnet-scoped (/tailnet/{tailnet}/ipam/...) to mirror Tailscale's own " +
		"control-plane API convention.")
})

var _ = Service("ipam", func() {
	Description("Reserve, release, and list address-space and UDP-port allocations for one tailnet's registry.")
	planningMethod()
	policyCheckMethod()

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
			dryRunAttribute()
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
			dryRunAttribute()
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
			Param("dry_run")
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
			dryRunAttribute()
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

	Method("allocate_subnet_pool", func() {
		Description("Reserve the first free child CIDR of a pool for a site in one locked " +
			"read-decide-write transaction. Registry reservations for every site and the " +
			"caller-supplied live routes both count as taken.")
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("pool", String, "IPv4 or IPv6 CIDR prefix to allocate from; masked to its network address.")
			Attribute("size", Int,
				"Prefix length of the child to allocate, between the pool's length and the address width.")
			Attribute("site", String, "Site name to reserve the child for.")
			Attribute("live_taken", ArrayOf(String),
				"CIDR prefixes advertised by live devices, counted as taken alongside registry reservations.")
			reservationMetadataAttributes()
			dryRunAttribute()
			Required("tailnet", "pool", "size", "site")
		})
		Result(func() {
			Attribute("cidr", String, "Canonical subnet CIDR actually reserved.", func() {
				Meta("struct:field:name", "CIDR")
			})
			Attribute("site", String, "Canonical site name actually written.")
			Attribute("changed", Boolean,
				"False when ticket named an existing reservation for the site inside the pool, "+
					"which was returned unchanged.")
			reservationMetadataAttributes()
			Required("cidr", "site", "changed")
		})
		Error("exhausted", func() {
			Description("No child of the requested size is free inside the pool.")
		})
		Error("invalid_input", func() {
			Description("pool, size, site, a live route, or reservation metadata is invalid.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/subnet-pools/allocate")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
			Response("exhausted", StatusUnprocessableEntity)
		})
	})

	Method("release_subnet_pool", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("cidr", String, "Canonical IPv4 or IPv6 CIDR prefix to release.", func() {
				Meta("struct:field:name", "CIDR")
			})
			dryRunAttribute()
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
			Param("dry_run")
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
			dryRunAttribute()
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
			dryRunAttribute()
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
			Param("dry_run")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
		})
	})

	Method("list_udp_ports", func() {
		Payload(func() {
			Attribute("tailnet", String,
				"Routing/storage key for the registry. May be a synthetic alias, not "+
					"necessarily the live Tailscale tailnet name.")
			Attribute("site", String, "Optional site/firewall-boundary label to filter. "+
				"An absent or empty site parameter means no filter: returns reservations from all sites.")
			Required("tailnet")
		})
		Result(ArrayOf(UDPPortReservation))
		Error("invalid_input", func() {
			Description("site is not a valid site/firewall-boundary label.")
		})
		HTTP(func() {
			GET("/tailnet/{tailnet}/ipam/udp-ports")
			Param("site")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
		})
	})

	Method("reserve_udp_port", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("site", String, "Site/firewall-boundary label for the reservation.")
			Attribute("port", String, "Decimal UDP listen port to reserve, 1024-65535.")
			Attribute("device", String, "Device label to reserve the port for.")
			dryRunAttribute()
			Required("tailnet", "site", "port", "device")
		})
		Result(func() {
			Attribute("site", String, "Canonical site label actually written.")
			Attribute("port", String, "Canonical decimal UDP port actually written.")
			Attribute("device", String, "Canonical device label actually written.")
			Attribute("changed", Boolean, "False when the exact site, port, and device tuple was already reserved.")
			Required("site", "port", "device", "changed")
		})
		Error("collision", func() {
			Description("port is already reserved for a different device within the site.")
		})
		Error("invalid_input", func() {
			Description("site, port, or device is invalid.")
		})
		HTTP(func() {
			POST("/tailnet/{tailnet}/ipam/udp-ports")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
			Response("collision", StatusConflict)
		})
	})

	Method("release_udp_port", func() {
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("site", String, "Site/firewall-boundary label containing the reservation.")
			Attribute("port", String, "Decimal UDP listen port to release, 1024-65535.")
			dryRunAttribute()
			Required("tailnet", "site", "port")
		})
		Result(func() {
			Attribute("site", String, "Canonical site label containing the released port.")
			Attribute("port", String, "Canonical decimal UDP port released.")
			Attribute("changed", Boolean, "False when the site and port were not reserved.")
			Required("site", "port", "changed")
		})
		Error("invalid_input", func() {
			Description("site or port is invalid.")
		})
		HTTP(func() {
			DELETE("/tailnet/{tailnet}/ipam/udp-ports")
			Param("site")
			Param("port")
			Param("dry_run")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
		})
	})
})
