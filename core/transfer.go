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
	"sync"
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

// FileInfo is a staged file or folder the UI shows before sending.
type FileInfo struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	IsFolder  bool   `json:"is_folder,omitempty"`
	FileCount int    `json:"file_count,omitempty"`
}

// FileInfos stats each path. Directories are included with their total size
// and a "folder" flag so the UI can stage them for folder transfer.
func FileInfos(paths []string) []FileInfo {
	out := make([]FileInfo, 0, len(paths))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			totalSize, fileCount := dirStats(p)
			out = append(out, FileInfo{
				Path:      p,
				Name:      filepath.Base(p),
				Size:      totalSize,
				IsFolder:  true,
				FileCount: fileCount,
			})
		} else {
			out = append(out, FileInfo{Path: p, Name: filepath.Base(p), Size: fi.Size()})
		}
	}
	return out
}

// dirStats walks a directory and returns the total size and file count.
func dirStats(root string) (totalSize int64, fileCount int) {
	filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		totalSize += fi.Size()
		fileCount++
		return nil
	})
	return
}

// SendFileByPath is the fast path: Go opens the file itself and streams it
// straight to the peer — no browser, no buffering the file in memory. The
// transfer is tracked so progress survives the drawer being closed.
func SendFileByPath(peer Peer, self Identity, path string, trk *Tracker) {
	path = filepath.Clean(path)
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
		err = SendToPeerWithOpts(ctx, peer, self, name, pr, EncryptedSize(fi.Size()), fi.Size(), true, "")
	} else {
		// Unencrypted fallback (handleSendPath already rejects unpaired peers).
		var body io.Reader = &CountingReader{R: f, Tr: tr}
		err = SendToPeerWithOpts(ctx, peer, self, name, body, fi.Size(), fi.Size(), false, "")
	}

	if err != nil {
		log.Printf("sendFileByPath %q to %s FAILED: %v", name, peer.Name, err)
	} else {
		log.Printf("sendFileByPath %q to %s OK (%s)", name, peer.Name, HumanSize(fi.Size()))
		Notify("SwiftDrop", fmt.Sprintf("Sent %s (%s) to %s", name, HumanSize(fi.Size()), peer.Name))
	}
	trk.Finish(tr, err)
}

// SendFolderByPath sends each file in a folder individually in parallel.
// No zip — files are streamed as-is with relative path headers.
func SendFolderByPath(peer Peer, self Identity, dirPath string, trk *Tracker) {
	name := filepath.Base(dirPath)
	totalSize, fileCount := dirStats(dirPath)
	if fileCount == 0 {
		trk.Finish(trk.Start(name, 0, peer.Name, "send"), fmt.Errorf("empty folder"))
		return
	}

	tr := trk.Start("📁 "+name, totalSize, peer.Name, "send")
	tr.FilePath = dirPath
	tr.PeerID = peer.ID
	tr.Status = "preparing"
	ctx, cancel := context.WithCancel(context.Background())
	trk.SetCancel(tr, cancel)
	defer cancel()

	// Phase 1: announce folder → get consent + session token.
	session, err := announceFolderToPeer(ctx, peer, self, name, totalSize, fileCount)
	if err != nil {
		log.Printf("sendFolderByPath %q announce failed: %v", name, err)
		trk.Finish(tr, err)
		return
	}

	// Phase 2: walk and send each file in parallel (max 4 concurrent).
	tr.Status = "sending"
	atomic.StoreInt64(&tr.Sent, 0)

	type fileEntry struct {
		path string
		rel  string
		size int64
	}
	var files []fileEntry
	filepath.Walk(dirPath, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil || fi.IsDir() {
			return nil
		}
		if fi.Name() == ".DS_Store" || strings.HasPrefix(fi.Name(), "._") {
			return nil
		}
		rel, _ := filepath.Rel(dirPath, path)
		rel = filepath.ToSlash(rel)
		files = append(files, fileEntry{path, rel, fi.Size()})
		return nil
	})

	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, 4)
		errOnce  sync.Once
		firstErr error
		okCount  int64
	)
	for _, fe := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(fe fileEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := sendFolderFileToPeer(ctx, peer, self, fe.rel, fe.path, fe.size, session, tr); err != nil {
				errOnce.Do(func() { firstErr = err })
				log.Printf("folder-file %s/%s to %s FAILED: %v", name, fe.rel, peer.Name, err)
			} else {
				atomic.AddInt64(&okCount, 1)
			}
		}(fe)
	}
	wg.Wait()

	// Phase 3: signal folder done (include cancel flag so receiver knows).
	cancelled := tr.Status == "canceled" || (ctx.Err() != nil)
	sendFolderDone(ctx, peer, self, name, session, cancelled)

	if firstErr != nil {
		log.Printf("sendFolderByPath %q to %s: %d/%d sent, first err: %v", name, peer.Name, okCount, len(files), firstErr)
	} else {
		log.Printf("sendFolderByPath %q (%d files, %s) to %s OK", name, fileCount, HumanSize(totalSize), peer.Name)
		Notify("SwiftDrop", fmt.Sprintf("Sent folder %s (%d files) to %s", name, fileCount, peer.Name))
	}
	trk.Finish(tr, firstErr)
}

// announceFolderToPeer sends a lightweight announcement to get consent and
// a session token before streaming individual files.
func announceFolderToPeer(ctx context.Context, peer Peer, self Identity, folderName string, totalSize int64, fileCount int) (string, error) {
	target := fmt.Sprintf("http://%s/inbox", peer.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, http.NoBody)
	if err != nil {
		return "", err
	}
	req.ContentLength = 0
	req.Header.Set("X-Filename", folderName)
	req.Header.Set("X-From", self.Name)
	req.Header.Set("X-From-ID", self.ID)
	req.Header.Set("X-File-Size", strconv.FormatInt(totalSize, 10))
	req.Header.Set("X-Folder-Announce", "true")
	req.Header.Set("X-Folder-Count", strconv.Itoa(fileCount))

	if key := Pairs.IsPaired(peer.ID); key != nil {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(hmacsha.New, key)
		mac.Write([]byte(self.ID + "|" + folderName + "|" + ts))
		req.Header.Set("X-Auth-HMAC", hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-Auth-Time", ts)
	}

	resp, err := TransferClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("folder announce: %d: %s", resp.StatusCode, string(msg))
	}
	var result struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("folder announce: bad response: %w", err)
	}
	if result.Session == "" {
		return "", fmt.Errorf("folder announce: empty session")
	}
	return result.Session, nil
}

// sendFolderFileToPeer sends a single file belonging to a folder session.
func sendFolderFileToPeer(ctx context.Context, peer Peer, self Identity, relPath, filePath string, fileSize int64, session string, tr *Transfer) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Use sanitized relPath (/ → _) as the HMAC filename so each file in
	// the folder gets a unique HMAC even if base names collide across subdirs.
	hmacName := strings.ReplaceAll(relPath, "/", "_")
	target := fmt.Sprintf("http://%s/inbox", peer.Host)

	key := Pairs.IsPaired(peer.ID)
	var body io.Reader
	var contentLength int64
	var encrypted bool

	if key != nil {
		pr, pw := io.Pipe()
		go func() {
			pw.CloseWithError(EncryptStream(pw, &CountingReader{R: f, Tr: tr}, key))
		}()
		body = pr
		contentLength = EncryptedSize(fileSize)
		encrypted = true
	} else {
		body = &CountingReader{R: f, Tr: tr}
		contentLength = fileSize
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return err
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Filename", hmacName)
	req.Header.Set("X-From", self.Name)
	req.Header.Set("X-From-ID", self.ID)
	req.Header.Set("X-File-Size", strconv.FormatInt(fileSize, 10))
	req.Header.Set("X-Folder-Session", session)
	req.Header.Set("X-Folder-Rel", relPath)
	if encrypted {
		req.Header.Set("X-Encrypted", "aes-gcm-v2")
	}

	if key != nil {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(hmacsha.New, key)
		mac.Write([]byte(self.ID + "|" + hmacName + "|" + ts))
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

// sendFolderDone tells the receiver the folder transfer is complete.
// Uses a fresh context so the signal reaches the receiver even if the
// transfer was cancelled on the sender side.
func sendFolderDone(_ context.Context, peer Peer, self Identity, folderName, session string, cancelled bool) {
	doneCtx, doneCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer doneCancel()

	target := fmt.Sprintf("http://%s/inbox", peer.Host)
	req, err := http.NewRequestWithContext(doneCtx, http.MethodPost, target, http.NoBody)
	if err != nil {
		return
	}
	req.ContentLength = 0
	req.Header.Set("X-Filename", folderName)
	req.Header.Set("X-From", self.Name)
	req.Header.Set("X-From-ID", self.ID)
	req.Header.Set("X-Folder-Done", session)
	if cancelled {
		req.Header.Set("X-Folder-Cancelled", "true")
	}

	if key := Pairs.IsPaired(peer.ID); key != nil {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(hmacsha.New, key)
		mac.Write([]byte(self.ID + "|" + folderName + "|" + ts))
		req.Header.Set("X-Auth-HMAC", hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-Auth-Time", ts)
	}

	resp, err := TransferClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// TransferClient is tuned for throughput on the LAN: keep-alives on, generous
// timeouts (large files), and no response-body limits.
var TransferClient = &http.Client{
	Timeout: 0, // no overall timeout; large files can take a while
	Transport: &http.Transport{
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 3 * time.Second,
		ResponseHeaderTimeout: 0,    // disabled: receiver may unzip folders before replying
		DisableCompression:    true, // we move mostly-incompressible bytes
		TLSHandshakeTimeout:   10 * time.Second,
		WriteBufferSize:       256 * 1024,
		ReadBufferSize:        256 * 1024,
	},
}

// SendToPeerWithOpts streams body straight to the peer's /inbox without
// buffering the whole file. contentLength may be -1 if unknown (chunked).
// When encrypted is true, the X-Encrypted header is set so the receiver knows
// to decrypt. Canceling ctx aborts the in-flight request.
func SendToPeerWithOpts(ctx context.Context, peer Peer, self Identity, filename string, body io.Reader, contentLength int64, originalSize int64, encrypted bool, sha256hash string) error {
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
		req.Header.Set("X-Encrypted", "aes-gcm-v2")
	}
	if sha256hash != "" {
		req.Header.Set("X-SHA256", sha256hash)
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
	// Validate host is a private/loopback IP to prevent SSRF.
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		h = host
		port = ""
	}
	ip := net.ParseIP(h)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()) {
		return Peer{}, fmt.Errorf("probe blocked: %s is not a LAN address", h)
	}
	// Rebuild host from validated IP + port so tainted input is never used in the URL.
	validatedHost := h
	if port != "" {
		validatedHost = net.JoinHostPort(h, port)
	}
	resp, err := probeClient.Get(fmt.Sprintf("http://%s/api/me", validatedHost))
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
		Host:     validatedHost,
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

// SafePath validates that dest is contained within baseDir after cleaning.
// Returns the cleaned path and true if safe, or ("", false) if the path
// escapes the base directory (path traversal).
func SafePath(baseDir, dest string) (string, bool) {
	cleaned := filepath.Clean(dest)
	base := filepath.Clean(baseDir) + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, base) && cleaned != filepath.Clean(baseDir) {
		return "", false
	}
	return cleaned, true
}
