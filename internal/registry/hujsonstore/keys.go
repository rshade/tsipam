package hujsonstore

const (
	siteIDsKey          = "siteIDs"
	subnetPoolsKey      = "subnetPools"
	serviceAddressesKey = "serviceAddresses"
	udpPortsKey         = "udpPorts"

	emptyDocument = "{}\n"
	utf8BOM       = "\xef\xbb\xbf"

	attrSiteIDCount         = "site_id_count"
	attrSubnetPoolCount     = "subnet_pool_count"
	attrServiceAddressCount = "service_address_count"
	attrUDPPortCount        = "udp_port_count"
	registryLoadedMessage   = "registry loaded"
	registryCreatedMessage  = "registry created"
	decodingRegistryPrefix  = "decoding registry: %w"
)
