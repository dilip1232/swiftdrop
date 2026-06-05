package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// StartLANScan periodically scans the local /24 subnet for SwiftDrop peers on
// the default port. This is a fallback for when mDNS is unavailable (common on
// Windows where the DNS Client service occupies port 5353).
//
// The scan uses fast TCP dial checks (200ms timeout) to find open ports, then
// probes only the responsive hosts via /api/me. Runs every 15 seconds.
func StartLANScan(ctx context.Context, self Identity, reg *PeerRegistry) {
	go func() {
		// Give mDNS a chance first.
		time.Sleep(5 * time.Second)
		scanSubnet(self, reg)

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				scanSubnet(self, reg)
			}
		}
	}()
	log.Printf("lanscan: subnet scanner started (fallback discovery)")
}

func scanSubnet(self Identity, reg *PeerRegistry) {
	localIP := LocalIP()
	if localIP == "" {
		return
	}
	ip := net.ParseIP(localIP).To4()
	if ip == nil {
		return
	}

	prefix := fmt.Sprintf("%d.%d.%d.", ip[0], ip[1], ip[2])
	portStr := fmt.Sprint(self.Port)

	var wg sync.WaitGroup
	// Limit concurrency to avoid flooding the network.
	sem := make(chan struct{}, 50)

	for i := 1; i < 255; i++ {
		target := fmt.Sprintf("%s%d", prefix, i)
		if target == localIP {
			continue
		}
		host := net.JoinHostPort(target, portStr)

		wg.Add(1)
		sem <- struct{}{}
		go func(h string) {
			defer wg.Done()
			defer func() { <-sem }()

			// Fast TCP check — skip hosts that aren't listening.
			conn, err := net.DialTimeout("tcp", h, 200*time.Millisecond)
			if err != nil {
				return
			}
			conn.Close()

			// Host is listening on our port — probe its identity.
			peer, err := ProbePeer(h)
			if err != nil || peer.ID == self.ID || reg.IsIgnored(peer.ID) {
				return
			}
			reg.Upsert(peer)
			reg.Remember(peer)
			go AnnounceToRemote(h, self)
		}(host)
	}
	wg.Wait()
}
