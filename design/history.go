package design

import (
	. "goa.design/goa/v3/dsl" //nolint:staticcheck,revive // goa's DSL is designed for dot-import
)

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var RegistryEventValue = Type("RegistryEventValue", func() {
	Description("A reservation's stored value before or after a change.")
	Attribute("target", String, "Site of a site ID or subnet pool, service of an address, or device of a UDP port.")
	reservationMetadataAttributes()
	Required("target")
})

//nolint:gochecknoglobals // goa's DSL requires a package-level var per declared type/service/API; this is design source, not application state.
var RegistryEvent = Type("RegistryEvent", func() {
	Description("One append-only audit record of a registry change.")
	Attribute("at", String, "RFC 3339 UTC time of the change.", func() {
		Format(FormatDateTime)
	})
	Attribute("action", String, "Operation that made the change.", func() {
		Enum("reserve", "allocate", "release", "import")
	})
	Attribute("kind", String, "Registry map that changed.", reservationKinds)
	Attribute("key", String, "Canonical key of the reservation.")
	Attribute("site", String, "Site that scopes a UDP-port key; absent for other kinds.")
	Attribute("before", RegistryEventValue, "Stored value before the change; absent when the key was not reserved.")
	Attribute("after", RegistryEventValue, "Stored value after the change; absent when the key was released.")
	Attribute("actor", String, "Authenticated caller that made the change; absent for a local command.")
	Required("at", "action", "kind", "key")
})

func reservationKinds() {
	Enum("site_id", "subnet_pool", "service_address", "udp_port")
}

func historyMethod() {
	Method("list_history", func() {
		Description("List the audit events of one tailnet's registry, newest first.")
		Payload(func() {
			Attribute("tailnet", String, "Routing/storage key for the registry.")
			Attribute("kind", String, "Keep only events of this kind.", reservationKinds)
			Attribute("key", String, "Keep only events of this canonical key.")
			Attribute("since", String, "Keep only events at or after this RFC 3339 time.", func() {
				Format(FormatDateTime)
			})
			Attribute("limit", Int, "Most events to return.", func() {
				Default(100) //nolint:mnd // the documented default page size
				Minimum(1)
			})
			Required("tailnet")
		})
		Result(ArrayOf(RegistryEvent))
		Error("invalid_input", func() {
			Description("A filter is invalid.")
		})
		Error("unsupported", func() {
			Description("The selected registry backend keeps no history.")
		})
		HTTP(func() {
			GET("/tailnet/{tailnet}/ipam/history")
			Param("kind")
			Param("key")
			Param("since")
			Param("limit")
			Response(StatusOK)
			Response("invalid_input", StatusBadRequest)
			Response("unsupported", StatusNotImplemented)
		})
	})
}
