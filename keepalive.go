package main

import (
	"context"
	"time"
)

// startKeepalive makes the device list self-healing and independent of mDNS
// reliability: it probes every *known* device (ones seen via mDNS or added by
// IP, persisted across restarts) and shows the ones that are reachable right
// now. This means a reachable device always (re)appears automatically — after a
// restart, after a removal, or after the peer's IP changed and mDNS updated it.
//
// A device briefly removed by the user (ignore window) is skipped until it
// expires, then reappears if still reachable.
func startKeepalive(ctx context.Context, reg *peerRegistry, self Identity) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for _, k := range reg.knownList() {
				if k.ID == self.ID || reg.isIgnored(k.ID) {
					continue
				}
				probed, err := probePeer(k.Host)
				if err == nil && probed.ID == k.ID {
					probed.Manual = reg.isManual(k.ID)
					reg.upsert(probed) // visible + refreshes known host
					go announceToRemote(k.Host, self)
				} else {
					reg.remove(k.ID) // unreachable here; mDNS may re-find at a new host
				}
			}
		}
	}()
}
