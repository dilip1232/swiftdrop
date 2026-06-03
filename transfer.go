package core

import (
	"context"
	"crypto/hmac"
	hmacsha "crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// CountingReader tallies bytes read so the UI can show live send progress.
type CountingReader struct {
	R  io.Reader
	Tr *Transfer
}

func (c *CountingReader) Read(p []byte) (int, error) {
	// Block while the transfer is paused.
	c.Tr.PauseMu.Lock()
	ch := c.Tr.PauseCh
	c.Tr.PauseMu.Unlock()
	if ch != nil {
		<-ch // blocks until channel is closed (resume)
	}
	n, err := c.R.Read(p)
	if n > 0 {
		atomic.AddInt64(&c.Tr.Sent, int64(n))
	}
	return n, err
}

// FileInfo is a staged file the UI shows before sending.
type FileInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// FileInfos stats each path, skipping anything that can't be read or is a
// directory, so the UI only ever stages real, sendable files.
func FileInfos(paths []string) []FileInfo {
	out := make([]FileInfo, 0, len(paths))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		out = append(out, FileInfo{Path: p, Name: filepath.Base(p), Size: fi.Size()})
	}
	return out
}

// SendFileByPath is the fast path: Go opens the file itself and streams it
// straight to the peer — no browser, no buffering the file in memory. The
// transfer is tracked so progress survives the drawer being closed.
func SendFileByPath(peer Peer, self Identity, path string, trk *Tracker) {
	name := filepath.Base(path)

	f, err := os.Open(path)
	if err != nil {
		trk.Finish(trk.Start(name, 0, peer.Name, "send"), err)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		trk.Finish(trk.Start(name, 0, peer.Name, "send"), err)
		return
	}

	tr := trk.Start(name, fi.Size(), peer.Name, "send")
	tr.FilePath = path
	tr.PeerID = peer.ID
	ctx, cancel := context.WithCancel(context.Background())
	trk.SetCancel(tr, cancel)
	defer cancel()

	key := Pairs.IsPaired(peer.ID)
	if key != nil {
		// Encrypted send — stream through AES-256-GCM.
		// GCM tags provide integrity; no separate SHA-256 needed.
		pr, pw := io.Pipe()
		go func() {
			pw.CloseWithError(EncryptStream(pw, &CountingReader{R: f, Tr: tr}, key))
		}()
		err = SendToPeerWithOpts(ctx, peer, self, name, pr, EncryptedSize(fi.Size()), fi.Size(), true)
	} else {
		// Unencrypted fallback (handleSendPath already rejects unpaired peers).
		var body io.Reader = &CountingReader{R: f, Tr: tr}
		err = SendToPeerWithOpts(ctx, peer, self, name, body, fi.Size(), fi.Size(), false)
	}

	if err != nil {
		log.Printf("sendFileByPath %q to %s FAILED: %v", name, peer.Name, err)
	} else {
		log.Printf("sendFileByPath %q to %s OK (%s)", name, peer.Name, HumanSize(fi.Size()))
		Notify("SwiftDrop", fmt.Sprintf("Sent %s (%s) to %s", name, HumanSize(fi.Size()), peer.Name))
	}
	trk.Finish(tr, err)
}

// TransferClient is tuned for throughput on the LAN: keep-alives on, generous
// timeouts (large files), and no response-body limits.
var TransferClient = &http.Client{
	Timeout: 0, // no overall timeout; large files can take a while
	Transport: &http.Transport{
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 3 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // detect stalled peers
		DisableCompression:    true,             // we move mostly-incompressible bytes
		TLSHandshakeTimeout:   10 * time.Second,
		WriteBufferSize:       256 * 1024,
		ReadBufferSize:        256 * 1024,
	},
}

// SendToPeerWithOpts streams body straight to the peer's /inbox without
// buffering the whole file. contentLength may be -1 if unknown (chunked).
// When encrypted is true, the X-Encrypted header is set so the receiver knows
// to decrypt. Canceling ctx aborts the in-flight request.
func SendToPeerWithOpts(ctx context.Context, peer Peer, self Identity, filename string, body io.Reader, contentLength int64, originalSize int64, encrypted bool, sha256hash ...string) error {
	target := fmt.Sprintf("http://%s/inbox", peer.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return err
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Filename", filename)
	req.Header.Set("X-From", self.Name)
	req.Header.Set("X-From-ID", self.ID)
	if originalSize > 0 {
		req.Header.Set("X-File-Size", strconv.FormatInt(originalSize, 10))
	}
	if encrypted {
		req.Header.Set("X-Encrypted", "aes-gcm")
	}
	if len(sha256hash) > 0 && sha256hash[0] != "" {
		req.Header.Set("X-SHA256", sha256hash[0])
	}
	// HMAC sender authentication: sign fromID|filename|timestamp with shared key.
	if key := Pairs.IsPaired(peer.ID); key != nil {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(hmacsha.New, key)
		mac.Write([]byte(self.ID + "|" + filename + "|" + ts))
		req.Header.Set("X-Auth-HMAC", hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-Auth-Time", ts)
	}

	resp, err := TransferClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("peer returned %d: %s", resp.StatusCode, string(msg))
	}
	return nil
}

// LocalIP returns this machine's LAN IPv4 address (for display in the UI), or
// "" if none is found.
func LocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		s := ip.String()
		if strings.HasPrefix(s, "192.168.") || strings.HasPrefix(s, "10.") || strings.HasPrefix(s, "172.") {
			return s
		}
	}
	return ""
}

// ProbePeer fetches host/api/me to confirm a manually-entered device is a
// reachable SwiftDrop instance and to learn its real identity.
// probeClient is reused across keepalive probes so TCP connections are pooled.
var probeClient = &http.Client{Timeout: 4 * time.Second}

func ProbePeer(host string) (Peer, error) {
	resp, err := probeClient.Get(fmt.Sprintf("http://%s/api/me", host))
	if err != nil {
		return Peer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Peer{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var id Identity
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		return Peer{}, fmt.Errorf("not a SwiftDrop device")
	}
	return Peer{
		ID:       id.ID,
		Name:     id.Name,
		Platform: id.Platform,
		Host:     host,
		Manual:   true,
	}, nil
}

// AnnounceToRemote tells a remote peer about us so they add us to their device
// list. This makes discovery bidirectional even when mDNS is one-way.
// announceClient is a shared HTTP client for lightweight announce/probe calls.
var announceClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        16,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		TLSHandshakeTimeout: 3 * time.Second,
	},
}

func AnnounceToRemote(peerHost string, self Identity) {
	selfIP := LocalIP()
	if selfIP == "" {
		return
	}
	selfHost := fmt.Sprintf("%s:%d", selfIP, self.Port)
	body := fmt.Sprintf(`{"host":%q}`, selfHost)
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/api/peers/add", peerHost), strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := announceClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// HumanSize renders a byte count compactly for logs/notifications.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// SafeFilename strips any path components a peer might send, keeping only the
// base name so an inbound transfer can never escape the download directory.
func SafeFilename(name string) string {
	name = filepath.Base(filepath.FromSlash(name))
	if unesc, err := url.PathUnescape(name); err == nil {
		name = filepath.Base(unesc)
	}
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return "received-file"
	}
	return name
}
