package assets

import "sync"

var raceFeatures struct {
	sync.Once
	protocol int
}

var telemetryFeatures struct {
	sync.Once
	protocol int
}

var knockdownFeatures struct {
	sync.Once
	protocol int
}

// KnockdownPoolProtocolVersion is independent of the existing CPU wire format.
// Only a bundle carrying the dedicated menu/capability path advertises it.
func KnockdownPoolProtocolVersion() int {
	knockdownFeatures.Do(func() {
		bundle, err := Open()
		if err != nil || bundle.Manifest.OnlineRaceProtocol != 3 {
			return
		}
		for _, game := range bundle.Manifest.KnockdownPoolPayloads {
			if game == "RMCPD00" && len(bundle.Files["binary/payload."+game+".bin"]) != 0 {
				knockdownFeatures.protocol = bundle.Manifest.KnockdownPoolProtocol
				return
			}
		}
	})
	return knockdownFeatures.protocol
}

// RaceTelemetryProtocolVersion reports the live-view producer embedded in the
// regular payload bundle. It is independent of CPU/Knockout negotiation.
func RaceTelemetryProtocolVersion() int {
	telemetryFeatures.Do(func() {
		bundle, err := Open()
		if err != nil {
			return
		}
		for _, game := range bundle.Manifest.RaceTelemetryPayloads {
			if game == "RMCPD00" && len(bundle.Files["binary/payload."+game+".bin"]) != 0 {
				telemetryFeatures.protocol = bundle.Manifest.RaceTelemetryProtocol
				return
			}
		}
	})
	return telemetryFeatures.protocol
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
