package registry

// LoadForPlanning reads one registry snapshot and validates every allocation
// map that has a live control-plane counterpart. UDP-port validation remains
// isolated: plan/check cannot compare configured listen ports with live state.
func LoadForPlanning(path string) (*Registry, error) {
	reg, err := Load(path)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{siteIDsKey, subnetPoolsKey, serviceAddressesKey} {
		if mapErr := reg.mapError(key); mapErr != nil {
			return nil, mapErr
		}
	}
	return reg, nil
}
