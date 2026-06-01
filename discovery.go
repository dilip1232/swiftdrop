package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

const ServiceType = "_swiftdrop._tcp"

// StartDiscovery registers this device on mDNS and continuously browses for
// peers, keeping the registry up to date. It returns a shutdown func.
//
// Uses libp2p/zeroconf/v2, which sets SO_REUSEPORT so several instances can
// share the mDNS multicast socket on one host and interoperates with macOS's
// system mDNSResponder.
func StartDiscovery(ctx context.Context, id Identity, reg *PeerRegistry) (func(), error) {
	txt := []string{
		"id=" + id.ID,
		"name=" + id.Name,
		"platform=" + id.Platform,
	}

	server, err := zeroconf.Register(
		"SwiftDrop-"+id.ID, // unique instance name
		ServiceType,
		"local.",
		id.Port,
		txt,
		nil, // all interfaces
	)
	if err != nil {
		return nil, fmt.Errorf("mdns register: %w", err)
	}

	go browse(ctx, id, reg)

	return func() { server.Shutdown() }, nil
}

func browse(ctx context.Context, self Identity, reg *PeerRegistry) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		entries := make(chan *zeroconf.ServiceEntry, 16)
		go func() {
			for entry := range entries {
				p, ok := peerFromEntry(entry)
				if !ok || p.ID == self.ID {
					continue
				}
				reg.Upsert(p)
			}
		}()

		// Browse for a window, then repeat so peers that join/leave are
		// picked up. Stale peers are pruned by a last-seen window rather than
		// requiring a hit every sweep (mDNS sweeps are individually flaky).
		bctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if err := zeroconf.Browse(bctx, ServiceType, "local.", entries); err != nil {
			log.Printf("mdns browse: %v", err)
		}
		<-bctx.Done()
		cancel()

		// Removal is owned by the keepalive probe (reachability), not by
		// mDNS sweeps — a reachable device must never be pruned just because
		// an mDNS sweep missed it. As a safety net, drop peers neither seen by
		// mDNS nor refreshed by keepalive for a long window.
		reg.PruneStale(60 * time.Second)
	}
}

func peerFromEntry(e *zeroconf.ServiceEntry) (Peer, bool) {
	var id, name, platform string
	for _, t := range e.Text {
		switch {
		case strings.HasPrefix(t, "id="):
			id = strings.TrimPrefix(t, "id=")
		case strings.HasPrefix(t, "name="):
			name = strings.TrimPrefix(t, "name=")
		case strings.HasPrefix(t, "platform="):
			platform = strings.TrimPrefix(t, "platform=")
		}
	}
	if id == "" {
		return Peer{}, false
	}

	ip := pickIP(e)
	if ip == "" {
		return Peer{}, false
	}
	if name == "" {
		name = e.HostName
	}

	return Peer{
		ID:       id,
		Name:     name,
		Platform: platform,
		Host:     net.JoinHostPort(ip, fmt.Sprint(e.Port)),
	}, true
}

// pickIP prefers an IPv4 address; falls back to IPv6 if that's all there is.
func pickIP(e *zeroconf.ServiceEntry) string {
	for _, ip := range e.AddrIPv4 {
		if ip != nil {
			return ip.String()
		}
	}
	for _, ip := range e.AddrIPv6 {
		if ip != nil {
			return ip.String()
		}
	}
	return ""
}
