package core

import (
	"context"
	"sync"
	"time"
)

// StartKeepalive makes the device list self-healing and independent of mDNS
// reliability: it probes every *known* device (ones seen via mDNS or added by
// IP, persisted across restarts) and shows the ones that are reachable right
// now. This means a reachable device always (re)appears automatically — after a
// restart, after a removal, or after the peer's IP changed and mDNS updated it.
//
// A device briefly removed by the user (ignore window) is skipped until it
// expires, then reappears if still reachable.
func StartKeepalive(ctx context.Context, reg *PeerRegistry, self Identity) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			known := reg.KnownList()
			sem := make(chan struct{}, 8) // max 8 concurrent probes
			var wg sync.WaitGroup
			for _, k := range known {
				if k.ID == self.ID || reg.IsIgnored(k.ID) {
					continue
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(k Peer) {
					defer func() { <-sem; wg.Done() }()
					probed, err := ProbePeer(k.Host)
					if err == nil && probed.ID == k.ID {
						probed.Manual = reg.IsManual(k.ID)
						reg.Upsert(probed)
						go AnnounceToRemote(k.Host, self)
					} else {
						reg.Remove(k.ID)
					}
				}(k)
			}
			wg.Wait()
		}
	}()
}
