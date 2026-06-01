package main

import (
	"context"
	"log"
	"time"
)

// startNetworkWatcher runs discovery and restarts it whenever this machine's
// LAN IP changes (e.g. switching Wi-Fi networks), clearing stale peers from the
// old network so the device list and IPs reflect the current network.
func startNetworkWatcher(ctx context.Context, id Identity, reg *peerRegistry) {
	go func() {
		last := localIP()
		stop, err := startDiscovery(ctx, id, reg)
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

			cur := localIP()
			if cur == last {
				continue
			}
			log.Printf("network changed (%s -> %s); restarting discovery", last, cur)
			last = cur
			if stop != nil {
				stop()
			}
			reg.clearMDNS()
			stop, err = startDiscovery(ctx, id, reg)
			if err != nil {
				log.Printf("discovery restart: %v", err)
			}
		}
	}()
}
