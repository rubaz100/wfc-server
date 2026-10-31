package assets

import "sync"

var raceFeatures struct {
	sync.Once
	protocol int
}

// OnlineRaceProtocolVersion reports capabilities of the actual embedded bundle,
// rather than a linker flag alone. Invalid or older bundles stay unavailable.
func OnlineRaceProtocolVersion() int {
	raceFeatures.Do(func() {
		bundle, err := Open()
		if err != nil {
			return
		}
		for _, game := range bundle.Manifest.OnlineRacePayloads {
			if game == "RMCPD00" && len(bundle.Files["binary/payload."+game+".bin"]) != 0 {
				raceFeatures.protocol = bundle.Manifest.OnlineRaceProtocol
				return
			}
		}
	})
	return raceFeatures.protocol
}
