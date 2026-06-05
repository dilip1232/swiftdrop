package core

import (
	"context"
	"log"
	"time"
)

// StartNetworkWatcher runs discovery and restarts it whenever this machine's
// LAN IP changes (e.g. switching Wi-Fi networks), clearing stale peers from the
// old network so the device list and IPs reflect the current network.
func StartNetworkWatcher(ctx context.Context, id Identity, reg *PeerRegistry) {
	go func() {
		last := LocalIP()
		stop, err := StartDiscovery(ctx, id, reg)
		if err != nil {
			log.Printf("discovery: %v", err)
		}

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				if stop != nil {
					stop()
				}
				return
			case <-ticker.C:
			}

			cur := LocalIP()
			if cur == last {
				continue
			}
			log.Printf("network changed (%s -> %s); restarting discovery", last, cur)
			last = cur
			if stop != nil {
				stop()
			}
			reg.ClearMDNS()
			stop, err = StartDiscovery(ctx, id, reg)
			if err != nil {
				log.Printf("discovery restart: %v", err)
			}
		}
	}()
}
