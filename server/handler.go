package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
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

type server struct {
	pin         string
	token       string
	albums      []Album
	batches     []Batch
	mediaPort   int
	mu          sync.RWMutex
	thumbs      map[int][]byte   // pre-generated composite thumbnails
	videoThumbs map[int][]byte   // pre-generated video thumbnails
	thumbTiles  map[int][][]byte // pre-generated single-image tiles per album (/api/thumb/{i}/{n})

	authFails       int
	authLockedUntil time.Time
	activeDownloads int32 // atomic: number of running download goroutines
}

// batchProgress tracks ZIP generation progress for SSE notifications.
type batchProgress struct {
	mu          sync.Mutex
	sent        int64 // bytes written so far
	total       int64 // total bytes expected
	files       int   // total file count
	done        bool
	cancelled   bool
	currentFile string // file currently being written
	batchID     string
}

// countingResponseWriter wraps an io.Writer and counts bytes written.
type countingResponseWriter struct {
	io.Writer
	n int64
}

func (c *countingResponseWriter) Write(p []byte) (int, error) {
	n, err := c.Writer.Write(p)
	c.n += int64(n)
	return n, err
}

func (p *batchProgress) update(sent int64, currentFile string) {
	p.mu.Lock()
	p.sent = sent
	p.currentFile = currentFile
	p.mu.Unlock()
}

func (p *batchProgress) finish() {
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
}

func (p *batchProgress) cancel() {
	p.mu.Lock()
	p.cancelled = true
	p.done = true
	p.mu.Unlock()
}

func (p *batchProgress) snapshot() (sent, total int64, files int, done, cancelled bool, currentFile string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sent, p.total, p.files, p.done, p.cancelled, p.currentFile
}

var (
	progressMap       = make(map[string]*batchProgress)
	progressMapMu     sync.RWMutex
	cancelMap         = make(map[string]context.CancelFunc)
	cancelMapMu       sync.Mutex
	canceledBatches   = make(map[string]time.Time)
	canceledBatchesMu sync.RWMutex
)

func setBatchProgress(batchID string, p *batchProgress) {
	p.batchID = batchID
	progressMapMu.Lock()
	if existing, ok := progressMap[batchID]; ok && !existing.done {
		// Already in progress, skip
		progressMapMu.Unlock()
		return
	}
	progressMap[batchID] = p
	progressMapMu.Unlock()
}

func getBatchProgress(batchID string) *batchProgress {
	progressMapMu.RLock()
	defer progressMapMu.RUnlock()
	return progressMap[batchID]
}

func registerCancel(batchID string, cancel context.CancelFunc) {
	cancelMapMu.Lock()
	cancelMap[batchID] = cancel
	cancelMapMu.Unlock()
}

func unregisterCancel(batchID string) {
	cancelMapMu.Lock()
	delete(cancelMap, batchID)
	cancelMapMu.Unlock()
}

func cancelBatch(batchID string) bool {
	cancelMapMu.Lock()
	cancel, ok := cancelMap[batchID]
	if ok {
		delete(cancelMap, batchID)
	}
	cancelMapMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func markBatchCanceled(batchID string) {
	canceledBatchesMu.Lock()
	canceledBatches[batchID] = time.Now()
	canceledBatchesMu.Unlock()
	log.Printf("Batch %s marked as canceled — subsequent requests will be rejected", batchID)
}

func isBatchCanceled(batchID string) bool {
	canceledBatchesMu.RLock()
	t, ok := canceledBatches[batchID]
	canceledBatchesMu.RUnlock()
	if ok && time.Since(t) > 30*time.Minute {
		canceledBatchesMu.Lock()
		delete(canceledBatches, batchID)
		canceledBatchesMu.Unlock()
		return false
	}
	return ok
}

func clearCanceledBatches() {
	canceledBatchesMu.Lock()
	canceledBatches = make(map[string]time.Time)
	canceledBatchesMu.Unlock()
}

func registerHandlers(mux *http.ServeMux, pin, token string, albums []Album, webDir string, mediaPort int) http.Handler {
	s := &server{
		pin:         pin,
		token:       token,
		albums:      albums,
		mediaPort:   mediaPort,
		thumbs:      make(map[int][]byte),
		videoThumbs: make(map[int][]byte),
		thumbTiles:  make(map[int][][]byte),
	}

	// Pre-generate composite thumbnails in background
	go s.pregenerateThumbs()

	mux.HandleFunc("/api/auth", s.handleAuth)
	mux.HandleFunc("/api/albums", s.authRequired(s.handleAlbums))
	mux.HandleFunc("/api/thumb/", s.authRequired(s.handleThumb))
	mux.HandleFunc("/api/videothumb/", s.authRequired(s.handleVideoThumb))
	mux.HandleFunc("/api/select", s.authRequired(s.handleSelect))
	mux.HandleFunc("/api/batches", s.authRequired(s.handleBatches))
	mux.HandleFunc("/api/batch/", s.handleBatch)
	mux.HandleFunc("/api/progress-poll", s.handleProgressPoll)
	mux.HandleFunc("/api/cancel", s.authRequired(s.handleCancel))
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	// Wrap the entire mux with CORS + Private Network Access middleware.
	// Chrome requires Access-Control-Allow-Private-Network: true when
	// accessing private network (192.168.x.x) from a non-private context.
	// Without this, Chrome returns ERR_ADDRESS_UNREACHABLE.
	return corsMiddleware(mux)
}

// corsMiddleware adds CORS headers and handles OPTIONS preflight requests
// for Chrome Private Network Access (PNA) compliance.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS headers for all responses
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400") // Cache preflight for 24h
		// Chrome Private Network Access: allow requests from public/non-private contexts
		w.Header().Set("Access-Control-Allow-Private-Network", "true")

		// Handle preflight OPTIONS requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func extractToken(r *http.Request) string {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
	}
	return token
}

// writeErr 写语言无关的 error code 响应 (spec ui-localization Req5:
// 服务端只回 code, 前端按当前 locale 翻译, 不含任何中英文 message 文案).
func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q}`, code)
}

// writeErrDetail 同上, 附 detail 字段(底层技术信息, 原样保留不翻译, 如扫描失败的原始 error).
func writeErrDetail(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(struct {
		Code   string `json:"code"`
		Detail string `json:"detail,omitempty"`
	}{code, detail})
	w.Write(b)
}

func (s *server) authRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		s.mu.RLock()
		valid := subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
		s.mu.RUnlock()

		if !valid {
			writeErr(w, http.StatusUnauthorized, "E_UNAUTHORIZED")
			return
		}
		next(w, r)
	}
}

func (s *server) handleAuth(w http.ResponseWriter, r *http.Request) {
	log.Printf("POST /api/auth from %s UA=%q", r.RemoteAddr, r.UserAgent())
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "E_METHOD_NOT_ALLOWED")
		return
	}

	// Rate limit: 5 failures → 60s lockout
	s.mu.Lock()
	if time.Now().Before(s.authLockedUntil) {
		s.mu.Unlock()
		log.Printf("POST /api/auth: rate-limited (locked)")
		writeErr(w, http.StatusTooManyRequests, "E_RATE_LIMITED")
		return
	}
	s.mu.Unlock()

	var req struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("POST /api/auth: bad JSON: %v", err)
		writeErr(w, http.StatusBadRequest, "E_BAD_REQUEST")
		return
	}

	if subtle.ConstantTimeCompare([]byte(req.PIN), []byte(s.pin)) != 1 {
		s.mu.Lock()
		s.authFails++
		if s.authFails >= 5 {
			s.authLockedUntil = time.Now().Add(60 * time.Second)
			s.authFails = 0
		}
		s.mu.Unlock()
		log.Printf("POST /api/auth: wrong PIN (fails=%d)", s.authFails)
		writeErr(w, http.StatusForbidden, "E_PIN_INVALID")
		return
	}

	s.mu.Lock()
	s.authFails = 0
	s.mu.Unlock()

	log.Printf("POST /api/auth: OK, issued token")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": s.token})
}

func (s *server) handleAlbums(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	albums := make([]Album, len(s.albums))
	copy(albums, s.albums)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"albums": albums,
	})
}

func (s *server) pregenerateThumbs() {
	s.mu.RLock()
	type thumbJob struct {
		isVideo bool
		index   int
		files   []string
	}
	var jobs []thumbJob
	for i, a := range s.albums {
		if a.Thumb == "composite" && len(a.ThumbFiles) > 0 {
			jobs = append(jobs, thumbJob{index: i, files: a.ThumbFiles})
		}
		if len(a.VideoThumbFiles) > 0 {
			jobs = append(jobs, thumbJob{index: i, files: a.VideoThumbFiles, isVideo: true})
		}
	}
	s.mu.RUnlock()

	for _, job := range jobs {
		// 单图 tiles: 每张源图独立 resize → JPEG, 供 /api/thumb/{i}/{n} 单格
		// 加载填满 grid (composite 只 1 张填不满多格). 仅图片相册生成 (视频相册
		// 走 videothumb, 无单图 tiles 需求).
		if !job.isVideo {
			if tiles := s.generateThumbTiles(job.files); len(tiles) > 0 {
				s.mu.Lock()
				s.thumbTiles[job.index] = tiles
				s.mu.Unlock()
			}
		}
		composite, err := s.generateCompositeThumb(job.files)
		if err != nil {
			log.Printf("thumb gen failed for album %d: %v", job.index, err)
			continue
		}
		s.mu.Lock()
		if job.isVideo {
			s.videoThumbs[job.index] = composite
		} else {
			s.thumbs[job.index] = composite
		}
		s.mu.Unlock()
	}
	log.Printf("Pre-generated %d composite thumbnails", len(jobs))
}

// handleThumb serves the pre-generated thumbnail image for an album.
//
//	GET /api/thumb/{albumIndex}        → 2x2 composite (legacy, 整个相册一张)
//	GET /api/thumb/{albumIndex}/{n}    → 第 n 张单图 tile (150×150), 让前端
//	                                     按容器宽度加载多张填满 thumb grid
func (s *server) handleThumb(w http.ResponseWriter, r *http.Request) {
	rest := r.URL.Path[len("/api/thumb/"):]
	idStr := rest
	tileN := -1
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		idStr = rest[:idx]
		n, err := strconv.Atoi(rest[idx+1:])
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "E_INVALID_ID")
			return
		}
		tileN = n
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "E_INVALID_ID")
		return
	}

	s.mu.RLock()
	if id < 0 || id >= len(s.albums) {
		s.mu.RUnlock()
		writeErr(w, http.StatusNotFound, "E_NOT_FOUND")
		return
	}

	// 单图 tile 路径: 越界返回 404 (前端 onerror 移除该格子).
	if tileN >= 0 {
		tiles := s.thumbTiles[id]
		if tileN >= len(tiles) {
			s.mu.RUnlock()
			writeErr(w, http.StatusNotFound, "E_NO_THUMBNAIL")
			return
		}
		data := tiles[tileN]
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data)
		return
	}

	// 默认: composite
	data, ok := s.thumbs[id]
	s.mu.RUnlock()

	if !ok {
		writeErr(w, http.StatusNotFound, "E_NO_THUMBNAIL")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

// handleVideoThumb serves the pre-generated video thumbnail for an album.
// GET /api/videothumb/{albumIndex}?token=xxx
func (s *server) handleVideoThumb(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/api/videothumb/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "E_INVALID_ID")
		return
	}

	s.mu.RLock()
	if id < 0 || id >= len(s.albums) {
		s.mu.RUnlock()
		writeErr(w, http.StatusNotFound, "E_NOT_FOUND")
		return
	}
	s.mu.RUnlock()

	s.mu.RLock()
	data, ok := s.videoThumbs[id]
	s.mu.RUnlock()

	if !ok {
		writeErr(w, http.StatusNotFound, "E_NO_VIDEO_THUMBNAIL")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

func (s *server) handleSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "E_METHOD_NOT_ALLOWED")
		return
	}

	var req struct {
		Paths []string `json:"paths"`
		Since int64    `json:"since"` // unix timestamp, 0 = no filter
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "E_BAD_REQUEST")
		return
	}

	// Validate paths against known albums
	s.mu.RLock()
	validPaths := make(map[string]bool, len(s.albums))
	for _, a := range s.albums {
		validPaths[a.Path] = true
	}
	s.mu.RUnlock()
	var filtered []string
	for _, p := range req.Paths {
		if validPaths[p] {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"file_count":  0,
			"batch_count": 0,
		})
		return
	}

	// Reject if a download goroutine is still running
	if atomic.LoadInt32(&s.activeDownloads) > 0 {
		writeErr(w, http.StatusConflict, "E_DOWNLOAD_IN_PROGRESS")
		return
	}

	since := int64(0)
	batches, err := scanDirectories(filtered, since)
	if err != nil {
		writeErrDetail(w, http.StatusInternalServerError, "E_SCAN_FAILED", err.Error())
		return
	}

	// Plan D (T-big-3): both Free and Pro share the same batch planner.
	// §5.2 v3 explicitly says Free includes video ("原视频字节级保留
	// 4K/60fps"). The legacy "Free filters videos" branch was a leftover
	// from the abandoned Plan A design (§5.1) and contradicted the spec.
	// Free/Pro 差异只在 Pro 三大钩子 (完整性/增量/文件夹), 不在传输内容.
	// single-zip-trust-tcp §1.2.1: collapse every selected file into ONE
	// batch. The browser downloads a single ZIP via one <a>.click() —
	// multi-batch looping is blocked by browser auto-download limits.
	// Free + Pro both run through the same planner.
	var allFiles []FileEntry
	for _, b := range batches {
		allFiles = append(allFiles, b.Files...)
	}
	if single := planSingleBatch(allFiles, BatchOpts{}); single != nil {
		batches = []Batch{*single}
	} else {
		batches = nil
	}

	s.mu.Lock()
	s.batches = batches
	s.mu.Unlock()
	clearCanceledBatches()

	totalFiles := 0
	for _, b := range batches {
		totalFiles += len(b.Files)
	}
	log.Printf("Selected %d dirs → %d files in %d batches", len(req.Paths), totalFiles, len(batches))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"file_count":  totalFiles,
		"batch_count": len(batches),
	})
}

// bigFileInfo is the safe, browser-facing projection of the standalone
// batch's single biggest file. FullPath is deliberately omitted so the
// phone's on-disk layout never leaks over the wire.
type bigFileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
}

func (s *server) handleBatches(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type batchInfo struct {
		ID        string `json:"id"`
		AlbumName string `json:"album_name"`
		FileCount int    `json:"file_count"`
		TotalSize int64  `json:"total_size"`
		LiveCount int    `json:"live_count"`

		// Plan D (T-big-3 § 3.3): only emitted for >1GB standalone batches.
		// omitempty keeps small batches compact (saves bandwidth, § 3.3.2).
		// biggest_file intentionally exposes only safe metadata (no FullPath)
		// — absolute paths leak Android filesystem layout to the browser.
		BiggestFile          *bigFileInfo `json:"biggest_file,omitempty"`
		IsBig                bool         `json:"big_file,omitempty"`
		EstimatedWiFiSeconds int          `json:"estimated_wifi_seconds,omitempty"`
		EstimatedUSBSeconds  int          `json:"estimated_usb_seconds,omitempty"`
	}

	result := make([]batchInfo, len(s.batches))
	for i, b := range s.batches {
		liveCount := 0
		for _, f := range b.Files {
			if f.IsLive {
				liveCount++
			}
		}
		result[i] = batchInfo{
			ID:        b.ID,
			AlbumName: b.AlbumName,
			FileCount: len(b.Files),
			TotalSize: b.TotalSize,
			LiveCount: liveCount,
		}
		if b.BigFile && b.BiggestFile != nil {
			result[i].IsBig = true
			result[i].BiggestFile = &bigFileInfo{
				Name:    filepath.Base(b.BiggestFile.Path),
				Size:    b.BiggestFile.Size,
				ModTime: b.BiggestFile.ModTime,
			}
			result[i].EstimatedWiFiSeconds = b.EstimatedWiFiSeconds
			result[i].EstimatedUSBSeconds = b.EstimatedUSBSeconds
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// padToZipSize back-fills zero bytes so the response body matches the
// declared Content-Length. 铁律 2: ANY underflow (manifest reserved-size
// drift, HEIC 3x estimation miss) MUST be back-filled — otherwise the
// browser download progress freezes at 99% indefinitely because the body
// is shorter than Content-Length. After padding, the progress entry is
// removed so the /api/progress-poll handler stops returning stale state.
//
// Used by the /api/batch ZIP-streaming endpoint.
func padToZipSize(w io.Writer, written, zipSize int64, batchID string) {
	if written > zipSize {
		log.Printf("WARNING: batch %s actual ZIP %d > estimated %d", batchID, written, zipSize)
	}
	if written < zipSize {
		pad := make([]byte, 32*1024)
		for remain := zipSize - written; remain > 0; {
			chunk := remain
			if chunk > int64(len(pad)) {
				chunk = int64(len(pad))
			}
			if _, err := w.Write(pad[:chunk]); err != nil {
				break
			}
			remain -= chunk
		}
	}
	scheduleProgressCleanup(batchID)
}

// scheduleProgressCleanup delays deletion of a progressMap entry so the polling
// client (handleProgressPoll, 1s interval) has a window to read the terminal
// done/cancelled state. Deleting immediately after writeBatchZip finishes
// races the polling loop: getBatchProgress returns nil → the poll response
// reports sent=0, done=false forever → the browser UI never sees completion
// and stays stuck on "取消传输" + "已暂停" even though the ZIP is fully
// delivered. 15s = ~15 poll cycles, ample headroom; batchID is unique per
// download so the delay never blocks the next one.
func scheduleProgressCleanup(batchID string) {
	go func() {
		time.Sleep(15 * time.Second)
		progressMapMu.Lock()
		delete(progressMap, batchID)
		progressMapMu.Unlock()
	}()
}

func (s *server) handleBatch(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	s.mu.RLock()
	valid := subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
	s.mu.RUnlock()
	if !valid {
		writeErr(w, http.StatusUnauthorized, "E_UNAUTHORIZED")
		return
	}

	atomic.AddInt32(&s.activeDownloads, 1)
	defer atomic.AddInt32(&s.activeDownloads, -1)

	idStr := r.URL.Path[len("/api/batch/"):]

	s.mu.RLock()
	var batch Batch
	found := false
	for i := range s.batches {
		if s.batches[i].ID == idStr {
			batch = s.batches[i]
			batch.Files = append([]FileEntry(nil), s.batches[i].Files...)
			found = true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		writeErr(w, http.StatusNotFound, "E_BATCH_NOT_FOUND")
		return
	}

	if isBatchCanceled(batch.ID) {
		log.Printf("Rejected download for canceled batch %s", batch.ID)
		writeErr(w, http.StatusGone, "E_DOWNLOAD_CANCELLED")
		return
	}

	flatMode := false

	// Free 模式下载文件名固定为 photos.zip (single-zip-trust-tcp: 一个相册一个 ZIP).
	filename := "photos.zip"
	displayName := "photos.zip"

	// Free 模式: 不做 HEIC 转换 / GPS 抹除 / Live Photo 拆分 / 智能重命名.
	// 所有 Pro 选项恒为零值, 文件按原始字节 + 原始文件名 (safeZipName 保留目录结构) 打包.
	zipOpts := ZipWriteOptions{
		FlatMode:      flatMode,
		SmartRename:   false,
		LivePhotoMode: "preserve",
	}
	// single-zip-trust-tcp §1.2.6: manifest.json 写入 ZIP 尾部, 供 Free verify.js
	// 读取 (size + SHA-256 校验). SessionID/BatchID 仅供客户端诊断关联.
	zipOpts.EmitManifest = true
	zipOpts.BatchID = batch.ID
	zipOpts.SessionID = cheapUUID()
	zipSize := calculateZipSize(batch.Files, zipOpts)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	registerCancel(batch.ID, cancel)
	defer unregisterCancel(batch.ID)
	// spec free-throughput: Free and Pro both run unthrottled. The legacy
	// "Free 5MB/s to push Pro conversion" was Plan A (§5.1, abandoned) and
	// contradicted §5.2 "不限速". throttleWriter is kept in archiver.go
	// for a potential v2 user-selectable rate cap, but no caller wires it.
	cw := &ctxWriter{w: w, ctx: ctx, cancel: cancel}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, filename, url.PathEscape(displayName)))

	// single-zip-trust-tcp §1: 中断 = 整个 ZIP 不完整, 必须重新下载.
	// Accept-Ranges: none 明确告知 Chrome/Edge 不支持断点续传, 阻止浏览器在
	// 下载中断后用 Range 请求续传 → 服务端返回 200 全量内容 → 浏览器当新下载.
	// (根因: Edge/Chrome 默认开启自动续传, 服务端 cancelBatch 让 HTTP 响应中断,
	// 浏览器用 Range 续传, 服务端忽略 Range 直接返回 200 → 看起来像新下载开始)
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", zipSize))

	wc := &countingResponseWriter{Writer: cw}
	if err := writeBatchZip(wc, &batch, s.mediaPort, zipSize, zipOpts, true, ctx, cancel); err != nil {
		log.Printf("zip error for batch %s: %v", batch.ID, err)
		// Force-close the TCP connection so the browser download manager
		// detects cancellation immediately (critical when browser paused
		// the download and TCP send buffer is full, blocking Go's flush).
		if ctx.Err() != nil {
			// Delay cleanup: SSE handler needs time to read cancelled=true.
			// If SSE reads it first, it deletes the entry; otherwise this cleans up.
			go func() {
				time.Sleep(3 * time.Second)
				progressMapMu.Lock()
				delete(progressMap, batch.ID)
				progressMapMu.Unlock()
			}()
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, hijackErr := hj.Hijack(); hijackErr == nil {
					if tc, ok := conn.(*net.TCPConn); ok {
						tc.SetLinger(0)
					}
					conn.Close()
				}
			}
			return
		}
		// Non-cancellation error: defer cleanup so SSE can still push the
		// terminal state (same race as padToZipSize — see scheduleProgressCleanup).
		scheduleProgressCleanup(batch.ID)
		return
	}
	padToZipSize(cw, wc.n, zipSize, batch.ID)
}

// handleProgressPoll returns the current batch progress as a JSON snapshot.
// 前端轮询端点 (替代 SSE 长连接): 每次 GET 返回当前 sent/total/done,
// 避免 SSE 长连接在大文件下载并发下被 TCP send buffer 积压导致进度卡顿.
func (s *server) handleProgressPoll(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	s.mu.RLock()
	valid := subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
	s.mu.RUnlock()
	if !valid {
		writeErr(w, http.StatusUnauthorized, "E_UNAUTHORIZED")
		return
	}
	batchID := r.URL.Query().Get("batch")
	if batchID == "" {
		writeErr(w, http.StatusBadRequest, "E_MISSING_BATCH_PARAM")
		return
	}
	type snapshot struct {
		BatchID   string `json:"batch_id"`
		Sent      int64  `json:"sent"`
		Total     int64  `json:"total"`
		Files     int    `json:"files"`
		Done      bool   `json:"done"`
		Cancelled bool   `json:"cancelled"`
		File      string `json:"file"`
	}
	w.Header().Set("Content-Type", "application/json")
	p := getBatchProgress(batchID)
	if p == nil {
		json.NewEncoder(w).Encode(snapshot{BatchID: batchID})
		return
	}
	sent, total, files, done, cancelled, currentFile := p.snapshot()
	json.NewEncoder(w).Encode(snapshot{
		BatchID: batchID, Sent: sent, Total: total, Files: files,
		Done: done, Cancelled: cancelled, File: currentFile,
	})
}

func (s *server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "E_METHOD_NOT_ALLOWED")
		return
	}
	var req struct {
		BatchID string `json:"batch_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "E_BAD_REQUEST")
		return
	}
	if req.BatchID == "" {
		writeErr(w, http.StatusBadRequest, "E_MISSING_BATCH_ID")
		return
	}
	cancelled := cancelBatch(req.BatchID)
	log.Printf("CANCEL_API: batch=%s found=%v", req.BatchID, cancelled)
	if cancelled {
		markBatchCanceled(req.BatchID)
	}
	log.Printf("Cancel request for batch %s: cancelled=%v", req.BatchID, cancelled)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"cancelled": cancelled})
}

const cellSize = 150

// generateCompositeThumb fetches up to 4 images from MediaFileServer
// and composites them into a 2x2 grid thumbnail.
func (s *server) generateCompositeThumb(paths []string) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, cellSize*2, cellSize*2))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	positions := [4]image.Point{
		{0, 0}, {cellSize, 0},
		{0, cellSize}, {cellSize, cellSize},
	}

	for i, path := range paths {
		if i >= 4 {
			break
		}
		img, err := s.fetchAndDecodeThumb(path)
		if err != nil {
			continue
		}
		resized := resizeImage(img, cellSize)
		draw.Draw(canvas, image.Rect(positions[i].X, positions[i].Y,
			positions[i].X+cellSize, positions[i].Y+cellSize),
			resized, image.Point{}, draw.Over)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// generateThumbTiles 生成每张源图的独立 cellSize×cellSize JPEG 缩略图,
// 供 /api/thumb/{i}/{n} 单格加载. 与 generateCompositeThumb 的单格逻辑一致,
// 但每张独立返回不合成, 让前端 grid 按容器宽度填满 (composite 只 1 张填不满).
func (s *server) generateThumbTiles(paths []string) [][]byte {
	var tiles [][]byte
	for _, path := range paths {
		if len(tiles) >= 8 {
			break
		}
		img, err := s.fetchAndDecodeThumb(path)
		if err != nil {
			continue
		}
		resized := resizeImage(img, cellSize)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 80}); err == nil {
			tiles = append(tiles, buf.Bytes())
		}
	}
	return tiles
}

// fetchAndDecodeThumb reads an image or extracts a video frame for thumbnails.
func (s *server) fetchAndDecodeThumb(path string) (image.Image, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if videoExtensions[ext] {
		return s.fetchVideoFrameThumb(path)
	}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		img, _, decErr := image.Decode(f)
		if decErr == nil {
			return img, nil
		}
	}
	if s.mediaPort <= 0 {
		return nil, fmt.Errorf("unreadable: %s", path)
	}
	fileURL := fmt.Sprintf("http://127.0.0.1:%d/file?path=%s", s.mediaPort, url.QueryEscape(path))
	resp, httpErr := mediaHTTPClient.Get(fileURL)
	if httpErr != nil {
		return nil, httpErr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	img, _, err := image.Decode(resp.Body)
	return img, err
}

// fetchVideoFrameThumb extracts a video frame via MediaFileServer /videothumb endpoint.
func (s *server) fetchVideoFrameThumb(path string) (image.Image, error) {
	if s.mediaPort <= 0 {
		return nil, fmt.Errorf("no media server for video thumb: %s", path)
	}
	thumbURL := fmt.Sprintf("http://127.0.0.1:%d/videothumb?path=%s", s.mediaPort, url.QueryEscape(path))
	resp, err := mediaHTTPClient.Get(thumbURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("videothumb status %d", resp.StatusCode)
	}
	img, _, decErr := image.Decode(resp.Body)
	return img, decErr
}

// resizeImage scales src to a square of size×size using nearest-neighbor,
// center-cropping to the shorter dimension.
func resizeImage(src image.Image, size int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	minDim := sw
	if sh < minDim {
		minDim = sh
	}
	offX := (sw - minDim) / 2
	offY := (sh - minDim) / 2

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := b.Min.X + offX + x*minDim/size
			sy := b.Min.Y + offY + y*minDim/size
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// cheapUUID returns a v4-style UUID string without pulling in google/uuid.
// Format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx (RFC 4122 variant).
// Cryptographic strength is unnecessary here — the value only needs to be
// unique per server run so /api/confirm stages don't collide.
func cheapUUID() string {
	var b [16]byte
	now := time.Now().UnixNano()
	// Mix time + monotonic counter via a tiny xorshift.
	x := uint64(now)
	for i := 0; i < 16; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte(x)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
