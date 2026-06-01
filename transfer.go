package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// countingReader tallies bytes read so the UI can show live send progress.
type countingReader struct {
	r  io.Reader
	tr *Transfer
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		atomic.AddInt64(&c.tr.Sent, int64(n))
	}
	return n, err
}

// FileInfo is a staged file the UI shows before sending.
type FileInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// fileInfos stats each path, skipping anything that can't be read or is a
// directory, so the UI only ever stages real, sendable files.
func fileInfos(paths []string) []FileInfo {
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

// sendFileByPath is the fast path: Go opens the file itself and streams it
// straight to the peer — no browser, no buffering the file in memory. The
// transfer is tracked so progress survives the drawer being closed.
func sendFileByPath(peer Peer, self Identity, path string, trk *tracker) {
	name := filepath.Base(path)

	f, err := os.Open(path)
	if err != nil {
		trk.finish(trk.start(name, 0, peer.Name, "send"), err)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		trk.finish(trk.start(name, 0, peer.Name, "send"), err)
		return
	}

	tr := trk.start(name, fi.Size(), peer.Name, "send")
	ctx, cancel := context.WithCancel(context.Background())
	trk.setCancel(tr, cancel)
	defer cancel()

	// Compute SHA-256 for integrity verification on the receiver side.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Printf("sendFileByPath %q hash failed: %v", name, err)
		trk.finish(tr, err)
		return
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		trk.finish(tr, err)
		return
	}

	var body io.Reader = &countingReader{r: f, tr: tr}
	size := fi.Size()

	err = sendToPeerWithOpts(ctx, peer, self, name, body, size, fi.Size(), false, hash)
	if err != nil {
		log.Printf("sendFileByPath %q to %s FAILED: %v", name, peer.Name, err)
	} else {
		log.Printf("sendFileByPath %q to %s OK (%s)", name, peer.Name, humanSize(fi.Size()))
		notify("SwiftDrop", fmt.Sprintf("Sent %s (%s) to %s", name, humanSize(fi.Size()), peer.Name))
	}
	trk.finish(tr, err)
}

// transferClient is tuned for throughput on the LAN: keep-alives on, generous
// timeouts (large files), and no response-body limits.
var transferClient = &http.Client{
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

// sendToPeerWithOpts streams body straight to the peer's /inbox without
// buffering the whole file. contentLength may be -1 if unknown (chunked).
// When encrypted is true, the X-Encrypted header is set so the receiver knows
// to decrypt. Canceling ctx aborts the in-flight request.
func sendToPeerWithOpts(ctx context.Context, peer Peer, self Identity, filename string, body io.Reader, contentLength int64, originalSize int64, encrypted bool, sha256hash ...string) error {
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

	resp, err := transferClient.Do(req)
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

// localIP returns this machine's LAN IPv4 address (for display in the UI), or
// "" if none is found.
func localIP() string {
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

// probePeer fetches host/api/me to confirm a manually-entered device is a
// reachable SwiftDrop instance and to learn its real identity.
func probePeer(host string) (Peer, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/api/me", host))
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

// announceToRemote tells a remote peer about us so they add us to their device
// list. This makes discovery bidirectional even when mDNS is one-way.
func announceToRemote(peerHost string, self Identity) {
	selfIP := localIP()
	if selfIP == "" {
		return
	}
	selfHost := fmt.Sprintf("%s:%d", selfIP, self.Port)
	body := fmt.Sprintf(`{"host":%q}`, selfHost)
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/api/peers/add", peerHost), strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// notify shows a desktop notification. On macOS it uses nativeNotify (cgo)
// so the notification shows SwiftDrop's icon instead of Script Editor's.
func notify(title, message string) {
	if runtime.GOOS == "darwin" {
		go nativeNotify(title, message)
		return
	}
	// Fallback for other platforms.
	script := fmt.Sprintf("display notification %q with title %q sound name \"Glass\"", message, title)
	_ = exec.Command("osascript", "-e", script).Start()
}

// humanSize renders a byte count compactly for logs/notifications.
func humanSize(n int64) string {
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

// safeFilename strips any path components a peer might send, keeping only the
// base name so an inbound transfer can never escape the download directory.
func safeFilename(name string) string {
	name = filepath.Base(filepath.FromSlash(name))
	if unesc, err := url.PathUnescape(name); err == nil {
		name = filepath.Base(unesc)
	}
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return "received-file"
	}
	return name
}
