package common

import "sync/atomic"

var currentDirectTransport atomic.Pointer[DirectTransport]

func UseDirectTransport(transport *DirectTransport) {
	currentDirectTransport.Store(transport)
}
