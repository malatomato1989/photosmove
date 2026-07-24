package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileEntry represents a single file in the photo library.
type FileEntry struct {
	Path     string // relative path from root (forward slashes)
	FullPath string // absolute path
	Size     int64
	ModTime  int64 // unix seconds
	IsLive   bool  // part of a Live Photo pair
}

// Batch represents one album's files to be packed into one ZIP archive.
type Batch struct {
	ID        string
	AlbumName string // e.g. "Camera"
	Files     []FileEntry
	TotalSize int64

	// Plan D (T-big-3) fields — only populated by planBatchesD for >1GB
	// standalone batches. Consumed by /api/batches serializer.
	BigFile              bool       // true for vid_big_*.zip single-file batches
	BiggestFile          *FileEntry // pointer so omitempty works in JSON
	EstimatedWiFiSeconds int        // size / 20MB/s
	EstimatedUSBSeconds  int        // size / 80MB/s
}

// Album represents a directory containing media files.
type Album struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Category   string   `json:"category,omitempty"`
	FileCount  int      `json:"file_count"`
	TotalSize  int64    `json:"total_size"`
	VideoCount int      `json:"video_count"`
	VideoSize  int64    `json:"video_size"`
	HeicCount  int      `json:"heic_count"`
	Thumb      string   `json:"thumb,omitempty"`
	LatestTime int64    `json:"latest_time,omitempty"`
	ThumbFiles      []string `json:"-"`
	VideoThumbFiles []string `json:"-"`
	// ThumbCount is exposed to the front-end: the number of single-image
	// thumbnails loadable for this album (to fill the dashboard thumb grid).
	// The actual paths live in ThumbFiles (json:"-"); the front-end requests
	// /api/thumb/{idx}/{n} for 0..ThumbCount-1.
	ThumbCount int `json:"thumb_count,omitempty"`
}

// mediaExtensions are file extensions considered as photos or videos.
var mediaExtensions = map[string]bool{
	// Images
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true, ".heif": true,
	".gif": true, ".bmp": true, ".webp": true, ".tiff": true, ".tif": true,
	".raw": true, ".dng": true, ".svg": true,
	// Videos
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".3gp": true,
	".wmv": true, ".flv": true, ".m4v": true, ".mpeg": true, ".mpg": true,
}

// imageExtensions are file extensions considered as images (for thumbnail selection).
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true, ".heif": true,
	".gif": true, ".bmp": true, ".webp": true, ".tiff": true, ".tif": true,
}

// videoExtensions are video file extensions (for fallback thumbnail via frame extraction).
var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".3gp": true,
	".wmv": true, ".flv": true, ".m4v": true, ".mpeg": true, ".mpg": true,
}

// BigFileThreshold is the size above which a single file becomes its own
// batch (Plan D, Pass 2). 1 GiB matches the small-batch ceiling so a single
// >1GB video always lands in vid_big_*.zip and never gets co-mingled with
// small monthly batches.
const BigFileThreshold int64 = 1 << 30 // 1024 * 1024 * 1024

// IsBigFile reports whether size exceeds the standalone-batch threshold.
// Exported so the archiver shares one source of truth with the scanner.
func IsBigFile(size int64) bool { return size > BigFileThreshold }

// thumbSampleCount is the number of single images sampled per album for the
// dashboard thumbnail grid. With the front-end grid auto-fit equal-split row,
// 6 images are enough to fill one row without wrapping.
const thumbSampleCount = 6

// sampleEvenly samples n paths at even intervals (first and last always
// included, equal steps in between) for thumbnail sampling — more
// representative of the album's full time span than taking the first N, and
// avoids clustering duplicates from burst shots / adjacent photos.
func sampleEvenly(paths []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(paths) <= n {
		return paths
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		idx := i * (len(paths) - 1) / (n - 1)
		out = append(out, paths[idx])
	}
	return out
}

// isVideoFile returns true if the file has a video extension.
func isVideoFile(path string) bool {
	return videoExtensions[strings.ToLower(filepath.Ext(path))]
}

// verifyImage opens a file and validates magic bytes match a known image format.
func verifyImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 12)
	n, err := f.Read(buf)
	if err != nil || n < 4 {
		return false
	}

	switch {
	case buf[0] == 0xFF && buf[1] == 0xD8 && buf[2] == 0xFF: // JPEG
		return true
	case buf[0] == 0x89 && buf[1] == 0x50 && buf[2] == 0x4E && buf[3] == 0x47: // PNG
		return true
	case buf[0] == 0x47 && buf[1] == 0x49 && buf[2] == 0x46 && buf[3] == 0x38: // GIF
		return true
	case buf[0] == 0x52 && buf[1] == 0x49 && buf[2] == 0x46 && buf[3] == 0x46 &&
		n >= 12 && buf[8] == 0x57 && buf[9] == 0x45 && buf[10] == 0x42 && buf[11] == 0x50: // WebP
		return true
	}
	return false
}

// verifyVideo opens a file and validates magic bytes match a known video format.
func verifyVideo(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 12)
	n, err := f.Read(buf)
	if err != nil || n < 8 {
		return false
	}

	switch {
	case buf[4] == 0x66 && buf[5] == 0x74 && buf[6] == 0x79 && buf[7] == 0x70: // ftyp (MP4/MOV/3GP)
		return true
	case buf[0] == 0x52 && buf[1] == 0x49 && buf[2] == 0x46 && buf[3] == 0x46 &&
		n >= 12 && buf[8] == 0x41 && buf[9] == 0x56 && buf[10] == 0x49: // RIFF...AVI
		return true
	case buf[0] == 0x1A && buf[1] == 0x45 && buf[2] == 0xDF && buf[3] == 0xA3: // MKV/WebM
		return true
	}
	return false
}

// verifyOpenable checks if a file can be opened (fallback for HEIC/TIFF/BMP/DNG).
func verifyOpenable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// shouldSkipDir returns true for directories that should never be traversed.
// Skips caches, hidden directories (dot-prefix).
func shouldSkipDir(name string) bool {
	if name == ".thumbnails" || name == "cache" {
		return true
	}
	return len(name) > 0 && name[0] == '.'
}

// shouldSkipPath returns true for full paths that should not be traversed.
func shouldSkipPath(path string) bool {
	return strings.HasSuffix(path, "/Android/data") ||
		strings.HasSuffix(path, "/Android/obb")
}

// --- MediaStore integration for Android/data/ restricted directories ---

type mediaStoreDB struct {
	Albums []mediaStoreAlbum `json:"albums"`
}

type mediaStoreAlbum struct {
	Path      string           `json:"path"`
	Name      string           `json:"name"`
	Category  string           `json:"category"`
	FileCount int              `json:"file_count"`
	TotalSize int64            `json:"total_size"`
	Files     []mediaStoreFile `json:"files"`
}

type mediaStoreFile struct {
	RelPath  string `json:"rel_path"`
	FullPath string `json:"full_path"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"mod_time"`
}

var globalMediaDB *mediaStoreDB

func loadMediaStoreDB(path string) *mediaStoreDB {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("MediaStore DB: cannot read %s: %v", path, err)
		return nil
	}
	var db mediaStoreDB
	if err := json.Unmarshal(data, &db); err != nil {
		log.Printf("MediaStore DB: parse error: %v", err)
		return nil
	}
	log.Printf("MediaStore DB: loaded %d albums", len(db.Albums))
	return &db
}

func discoverAlbums(roots []string, mediaDBPath string) []Album {
	// JSON-driven: if MediaStore DB is available, build albums entirely from it
	if mediaDBPath != "" {
		db := loadMediaStoreDB(mediaDBPath)
		if db != nil {
			globalMediaDB = db
			return buildAlbumsFromMediaDB(db)
		}
	}
	// Fallback: filesystem scan (for PC dev / testing without MediaStore)
	return discoverAlbumsFromFilesystem(roots)
}

func buildAlbumsFromMediaDB(db *mediaStoreDB) []Album {
	var albums []Album
	for _, msa := range db.Albums {
		if msa.FileCount == 0 {
			continue
		}
		var latest int64
		var allImages []string
		var thumbVideos []string
		var videoCount int
		var videoSize int64
		var heicCount int
		for _, mf := range msa.Files {
			if mf.ModTime > latest {
				latest = mf.ModTime
			}
			if isVideoFile(mf.RelPath) {
				videoCount++
				videoSize += mf.Size
			}
			if isHeicExt(mf.RelPath) {
				heicCount++
			}
			fp := mf.FullPath
			if fp == "" {
				fp = filepath.Join(msa.Path, mf.RelPath)
			}
			ext := strings.ToLower(filepath.Ext(mf.RelPath))
			// Collect all image paths, then sample at even intervals outside the
			// loop → thumbnails represent different periods, avoiding clustering
			// duplicates from burst shots / adjacent photos. Videos still take
			// the first 8 (videothumb only).
			if imageExtensions[ext] {
				allImages = append(allImages, fp)
			} else if videoExtensions[ext] && len(thumbVideos) < 8 {
				thumbVideos = append(thumbVideos, fp)
			}
		}
		thumbFiles := sampleEvenly(allImages, thumbSampleCount)
		album := Album{
			Path:       msa.Path,
			Name:       msa.Name,
			Category:   msa.Category,
			FileCount:  msa.FileCount,
			TotalSize:  msa.TotalSize,
			VideoCount: videoCount,
			VideoSize:  videoSize,
			HeicCount:  heicCount,
			LatestTime: latest,
			ThumbFiles:      thumbFiles,
			VideoThumbFiles: thumbVideos,
			ThumbCount:      len(thumbFiles),
		}
		if len(thumbFiles) > 0 {
			album.Thumb = "composite"
		}
		albums = append(albums, album)
	}
	sort.Slice(albums, func(i, j int) bool {
		return albums[i].Path < albums[j].Path
	})
	return albums
}

func discoverAlbumsFromFilesystem(roots []string) []Album {
	type albumStats struct {
		fileCount  int
		totalSize  int64
		thumbFiles []string
		thumbVideos []string
		latestTime int64
		videoCount int
		videoSize  int64
		heicCount  int
	}
	albumMap := make(map[string]*albumStats)

	countFile := func(albumDir, path, ext string, size int64, modTime int64) {
		st := albumMap[albumDir]
		if st == nil {
			st = &albumStats{}
			albumMap[albumDir] = st
		}
		st.fileCount++
		st.totalSize += size
		if modTime > st.latestTime {
			st.latestTime = modTime
		}
		if videoExtensions[ext] {
			st.videoCount++
			st.videoSize += size
		}
		if ext == ".heic" || ext == ".heif" {
			st.heicCount++
		}
		if imageExtensions[ext] && (verifyImage(path) || verifyOpenable(path)) && len(st.thumbFiles) < 8 {
			st.thumbFiles = append(st.thumbFiles, path)
		} else if videoExtensions[ext] && verifyVideo(path) && len(st.thumbVideos) < 8 {
			st.thumbVideos = append(st.thumbVideos, path)
		}
	}

	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			continue
		}

		var albumDirs []string
		filepath.WalkDir(realRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if shouldSkipDir(d.Name()) || shouldSkipPath(path) {
				return filepath.SkipDir
			}
			if path == realRoot {
				return nil
			}
			rel, _ := filepath.Rel(realRoot, path)
			depth := len(strings.Split(rel, string(filepath.Separator)))
			if depth <= 2 {
				albumDirs = append(albumDirs, path)
			}
			if depth >= 2 {
				return filepath.SkipDir
			}
			return nil
		})

		depth2Set := make(map[string]bool)
		for _, d := range albumDirs {
			rel, _ := filepath.Rel(realRoot, d)
			if len(strings.Split(rel, string(filepath.Separator))) == 2 {
				depth2Set[d] = true
			}
		}
		for _, albumDir := range albumDirs {
			rel, _ := filepath.Rel(realRoot, albumDir)
			depth := len(strings.Split(rel, string(filepath.Separator)))
			var exclude []string
			if depth == 1 {
				for _, d2 := range albumDirs {
					if depth2Set[d2] && strings.HasPrefix(d2, albumDir+string(filepath.Separator)) {
						exclude = append(exclude, d2)
					}
				}
			}
			files, err := scanOneDirectory(albumDir, exclude...)
			if err != nil {
				continue
			}
			for _, f := range files {
				ext := strings.ToLower(filepath.Ext(f.Path))
				countFile(albumDir, f.FullPath, ext, f.Size, f.ModTime)
			}
		}

		entries, err := os.ReadDir(realRoot)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if strings.HasPrefix(e.Name(), "._") {
					continue
				}
				ext := strings.ToLower(filepath.Ext(e.Name()))
				if !mediaExtensions[ext] {
					continue
				}
				info, err := e.Info()
				if err != nil || info.Size() == 0 {
					continue
				}
				countFile(realRoot, filepath.Join(realRoot, e.Name()), ext, info.Size(), info.ModTime().Unix())
			}
		}
	}

	var albums []Album
	for dir, st := range albumMap {
		if st.fileCount == 0 || (len(st.thumbFiles) == 0 && len(st.thumbVideos) == 0) {
			continue
		}
		albums = append(albums, Album{
			Path:       dir,
			Name:       filepath.Base(dir),
			FileCount:  st.fileCount,
			TotalSize:  st.totalSize,
			VideoCount: st.videoCount,
			VideoSize:  st.videoSize,
			HeicCount:  st.heicCount,
			Thumb:      "composite",
			LatestTime: st.latestTime,
			ThumbFiles:      st.thumbFiles,
			VideoThumbFiles: st.thumbVideos,
			ThumbCount:      len(st.thumbFiles),
		})
	}

	sort.Slice(albums, func(i, j int) bool {
		return albums[i].Path < albums[j].Path
	})
	return albums
}

// scanDirectories scans each album directory independently.
// Each album becomes one Batch (one ZIP download).
// If since > 0, only includes files with ModTime >= since.
func scanDirectories(roots []string, since int64) ([]Batch, error) {
	var batches []Batch

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}

		var files []FileEntry
		if globalMediaDB != nil {
			files = getFilesFromMediaDB(root)
		} else {
			var err error
			files, err = scanOneDirectory(root)
			if err != nil {
				log.Printf("warning: skip %s: %v", root, err)
				continue
			}
		}
		if len(files) == 0 {
			continue
		}

		markLivePhotos(files)

		if since > 0 {
			n := 0
			for _, f := range files {
				if f.ModTime >= since {
					files[n] = f
					n++
				}
			}
			kept := make(map[string]bool, n)
			for i := 0; i < n; i++ {
				kept[files[i].FullPath] = true
			}
			for _, f := range files[n:] {
				if f.IsLive {
					base := strings.TrimSuffix(f.Path, filepath.Ext(f.Path))
					for i := 0; i < n; i++ {
						if files[i].IsLive {
							ib := strings.TrimSuffix(files[i].Path, filepath.Ext(files[i].Path))
							if ib == base && !kept[f.FullPath] {
								files[n] = f
								kept[f.FullPath] = true
								n++
								break
							}
						}
					}
				}
			}
			files = files[:n]
		}

		if len(files) == 0 {
			continue
		}

		sort.Slice(files, func(i, j int) bool {
			return files[i].Path < files[j].Path
		})

		absRoot, _ := filepath.Abs(root)
		albumName := lookupAlbumName(absRoot)
		if albumName == "" {
			albumName = filepath.Base(absRoot)
		}

		batches = append(batches, Batch{
			ID:        generateBatchID(),
			AlbumName: albumName,
			Files:     files,
			TotalSize: sumSize(files),
		})
		log.Printf("  %s: %d media files (%s)", albumName, len(files), formatBytes(sumSize(files)))
	}

	return batches, nil
}

func getFilesFromMediaDB(path string) []FileEntry {
	if globalMediaDB == nil {
		return nil
	}
	cleanPath := normalizePath(path)
	for _, msa := range globalMediaDB.Albums {
		if normalizePath(msa.Path) == cleanPath {
			files := make([]FileEntry, 0, len(msa.Files))
			for _, mf := range msa.Files {
				fp := mf.FullPath
				if fp == "" {
					fp = filepath.Join(msa.Path, mf.RelPath)
				}
				files = append(files, FileEntry{
					Path:     filepath.ToSlash(mf.RelPath),
					FullPath: fp,
					Size:     mf.Size,
					ModTime:  mf.ModTime,
				})
			}
			return files
		}
	}
	return nil
}

func normalizePath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func lookupAlbumName(path string) string {
	if globalMediaDB == nil {
		return ""
	}
	cleanPath := normalizePath(path)
	for _, msa := range globalMediaDB.Albums {
		if normalizePath(msa.Path) == cleanPath {
			return msa.Name
		}
	}
	return ""
}

func generateBatchID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sumSize(files []FileEntry) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}

func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// scanOneDirectory enumerates media files recursively under root.
// For restricted paths (Android/data/), reads from MediaStore DB instead.
// excludeDirs are subdirectories to skip (used to avoid double-counting child albums).
func scanOneDirectory(root string, excludeDirs ...string) ([]FileEntry, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	// Check if this is a restricted MediaStore album
	if globalMediaDB != nil {
		for _, msa := range globalMediaDB.Albums {
			cleanAbs := normalizePath(absRoot)
			if normalizePath(msa.Path) == cleanAbs {
				return scanFromMediaStore(absRoot, msa), nil
			}
		}
	}

	// Recursive scan: all media files under this directory tree
	var files []FileEntry
	excludeSet := make(map[string]bool, len(excludeDirs))
	for _, d := range excludeDirs {
		excludeSet[d] = true
	}
	filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) || shouldSkipPath(path) || excludeSet[path] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !mediaExtensions[ext] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			rel = d.Name()
		}
		files = append(files, FileEntry{
			Path:     filepath.ToSlash(rel),
			FullPath: path,
			Size:     info.Size(),
			ModTime:  info.ModTime().Unix(),
		})
		return nil
	})

	return files, nil
}

// scanFromMediaStore builds FileEntry list from MediaStore album data.
func scanFromMediaStore(absRoot string, msa mediaStoreAlbum) []FileEntry {
	files := make([]FileEntry, 0, len(msa.Files))
	for _, mf := range msa.Files {
		fp := mf.FullPath
		if fp == "" {
			fp = filepath.Join(absRoot, mf.RelPath)
		}
		files = append(files, FileEntry{
			Path:     filepath.ToSlash(mf.RelPath),
			FullPath: fp,
			Size:     mf.Size,
			ModTime:  mf.ModTime,
		})
	}
	return files
}

// markLivePhotos detects HEIC+MOV pairs sharing the same base name and
// marks both entries as IsLive.
func markLivePhotos(files []FileEntry) {
	byBaseName := make(map[string][]*FileEntry)
	for i := range files {
		origExt := filepath.Ext(files[i].Path)
		ext := strings.ToLower(origExt)
		if ext == ".heic" || ext == ".mov" {
			base := strings.TrimSuffix(files[i].Path, origExt)
			byBaseName[base] = append(byBaseName[base], &files[i])
		}
	}
	for _, entries := range byBaseName {
		exts := make(map[string]bool)
		for _, e := range entries {
			exts[strings.ToLower(filepath.Ext(e.Path))] = true
		}
		if len(exts) == 2 && exts[".heic"] && exts[".mov"] {
			for _, e := range entries {
				e.IsLive = true
			}
		}
	}
}
