// S-1 Spike: ZIP64 真机验证 (本地可执行版)
//
// 来源: openspec/changes/big-video-no-split/spikes/zip64-spike-draft_test.go
// 目标: 验证 Go archive/zip 在三种场景下需要/不需要显式启用 ZIP64
//
// 跑法:
//   go test -run TestSpikeZip64 -v -timeout 600s          # 全部 (需 11GB 磁盘)
//   go test -run TestSpikeZip64 -v -short -timeout 60s    # 只跑文件数场景
//
// 三场景:
//   1. 单文件 > 4GB          (DJI 4K 5GB)         - 需 5GB 磁盘
//   2. 累计 > 4GB            (6 × 1GB 视频)        - 需 6GB 磁盘
//   3. 文件数 > 65535        (70000 张小照片)      - 需 ~100MB, 慢

package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// zip32SizeSentinel 是 ZIP32 size 字段哨兵值, 触发解压器读取 ZIP64 extra field
const zip32SizeSentinel = 0xFFFFFFFF

// createHeaderZip64 是 photosmove 改造后 writeFileToZip 的核心片段 (S-1 Spike 验证目标)
//
// 关键: 预填 UncompressedSize64, 超 4GB 时把 32 位 size 设哨兵.
// 验证: Go 是否自动加 ZIP64 extra field (Local Header + Central Directory).
func createHeaderZip64(zw *zip.Writer, name string, size int64) (io.Writer, error) {
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	}
	hdr.SetMode(0644)
	hdr.UncompressedSize64 = uint64(size)
	if uint64(size) > zip32SizeSentinel {
		hdr.UncompressedSize = zip32SizeSentinel
	} else {
		hdr.UncompressedSize = uint32(size)
	}
	return zw.CreateHeader(hdr)
}

// TestSpikeZip64_SingleFileOver4GB 场景 1: 单文件 5GB
// 验证 ZIP Local Header + Central Directory 都含 ZIP64 extra field
func TestSpikeZip64_SingleFileOver4GB(t *testing.T) {
	if testing.Short() {
		t.Skip("需要 5GB 临时磁盘空间, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "big.zip")
	const fileSize int64 = 5 * 1024 * 1024 * 1024 // 5GB

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip file: %v", err)
	}
	zw := zip.NewWriter(fw)

	w, err := createHeaderZip64(zw, "dji_4k_flight.mp4", fileSize)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}

	if _, err := io.Copy(w, newSpikeZeroReader(fileSize)); err != nil {
		t.Fatalf("Copy 5GB: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader (ZIP64 解码失败说明 Go 没自动启用): %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 {
		t.Fatalf("expect 1 entry, got %d", len(zr.File))
	}
	entry := zr.File[0]
	if entry.UncompressedSize64 != uint64(fileSize) {
		t.Errorf("UncompressedSize64 = %d, want %d (说明 64 位 size 没传对)",
			entry.UncompressedSize64, fileSize)
	}

	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("entry Open: %v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != fileSize {
		t.Errorf("read back bytes = %d, want %d (数据错位 = ZIP32 溢出)", n, fileSize)
	}
	t.Logf("S-1 场景 1 PASS: 5GB 单文件 ZIP64 正确启用, read back %d bytes", n)
}

// TestSpikeZip64_AccumulatedOver4GB 场景 2: 累计 6 × 1GB = 6GB
// 验证 Central Directory ZIP64 启用 (offset > uint32max)
func TestSpikeZip64_AccumulatedOver4GB(t *testing.T) {
	if testing.Short() {
		t.Skip("需要 6GB 临时磁盘空间, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "multi.zip")
	const fileCount = 6
	const eachSize int64 = 1024 * 1024 * 1024 // 1GB

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(fw)

	for i := 0; i < fileCount; i++ {
		name := "video_" + string(rune('0'+i)) + ".mp4"
		w, err := createHeaderZip64(zw, name, eachSize)
		if err != nil {
			t.Fatalf("CreateHeader %d: %v", i, err)
		}
		if _, err := io.Copy(w, newSpikeZeroReader(eachSize)); err != nil {
			t.Fatalf("Copy %d: %v", i, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer zr.Close()
	if len(zr.File) != fileCount {
		t.Fatalf("expect %d entries, got %d", fileCount, len(zr.File))
	}
	t.Logf("S-1 场景 2 PASS: 6GB 累计 ZIP64 中央目录正确启用, %d entries", len(zr.File))
}

// TestSpikeZip64_FileCountOver65535 场景 3: 70000 个 1KB 文件
// 验证 Central Directory ZIP64 启用 (count > uint16max)
// 不需要大磁盘, 但创建 70000 entries 较慢
func TestSpikeZip64_FileCountOver65535(t *testing.T) {
	if testing.Short() {
		t.Skip("创建 70000 entries 较慢, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "many.zip")
	const fileCount = 70000

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(fw)

	for i := 0; i < fileCount; i++ {
		// 生成不重复的文件名 (避免重名)
		name := "img_" + spikeItoa(i) + ".jpg"
		w, err := createHeaderZip64(zw, name, 1024)
		if err != nil {
			t.Fatalf("CreateHeader %d: %v", i, err)
		}
		if _, err := w.Write(make([]byte, 1024)); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v (若失败说明中央目录 ZIP64 未启用)", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader (中央目录 ZIP64 解码失败): %v", err)
	}
	defer zr.Close()
	if len(zr.File) != fileCount {
		t.Errorf("entry count = %d, want %d", len(zr.File), fileCount)
	}
	t.Logf("S-1 场景 3 PASS: %d 文件 ZIP64 中央目录正确启用", len(zr.File))
}

// TestSpikeZip64_ControlGroup 对照组: 用默认 Create(name) 不预填 size
// 验证不显式启用 ZIP64 时 Go 默认行为 (应该失败或数据错位)
func TestSpikeZip64_ControlGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("对照组需要 5GB, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "control.zip")
	const fileSize int64 = 5 * 1024 * 1024 * 1024

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(fw)

	// 对照组: 默认 Create, 不预填 64 位 size
	w, err := zw.Create("control.mp4")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 写 5GB (Go 内部会在 Close 时计算 size, 但流式场景可能出错)
	if _, err := io.Copy(w, newSpikeZeroReader(fileSize)); err != nil {
		t.Logf("对照组预期失败 (Create 模式不支持大文件): %v", err)
		return
	}

	if err := zw.Close(); err != nil {
		t.Logf("对照组 Close 失败 (预期): %v", err)
		return
	}
	fw.Close()

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Logf("对照组 OpenReader 失败 (预期, ZIP32 溢出): %v", err)
		return
	}
	defer zr.Close()

	entry := zr.File[0]
	t.Logf("对照组 entry.UncompressedSize64 = %d (实际 5GB = %d), 一致=%v",
		entry.UncompressedSize64, fileSize, entry.UncompressedSize64 == uint64(fileSize))

	if entry.UncompressedSize64 != uint64(fileSize) {
		t.Logf("对照组预期错位: Go 默认 Create 模式下 5GB 文件 size 不正确, 证明 createHeaderZip64 改造必要")
	}
}

// TestSpikeZip64_NonSeekableWriter 关键场景: 模拟 HTTP ResponseWriter (non-seekable)
// photosmove 真实场景: zip.Writer 底层是 http.ResponseWriter, 不能 Seek
// 验证: non-seekable writer 下 Go 是否仍自动启用 ZIP64
//
// 这个测试是 S-1 Spike 的"真正"验证 (前 4 个用 os.File Seeker 走 seek-and-update 路径)
func TestSpikeZip64_NonSeekableWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("non-seekable 5GB 测试, skip in -short")
	}

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "nonseekable.zip")
	const fileSize int64 = 5 * 1024 * 1024 * 1024 // 5GB

	fw, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 包装为 non-seekable writer, 模拟 http.ResponseWriter
	nw := newNonSeekableWriter(fw)

	zw := zip.NewWriter(nw)

	// 不预填 size (对照组: 模拟 photosmove 现状)
	w, err := zw.Create("non_seekable.mp4")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := io.Copy(w, newSpikeZeroReader(fileSize)); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v (若失败说明 non-seekable 模式不支持大文件)", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader (non-seekable ZIP64 解码失败): %v", err)
	}
	defer zr.Close()

	entry := zr.File[0]
	if entry.UncompressedSize64 != uint64(fileSize) {
		t.Errorf("UncompressedSize64 = %d, want %d", entry.UncompressedSize64, fileSize)
	}

	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("entry Open: %v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != fileSize {
		t.Errorf("read back = %d, want %d", n, fileSize)
	}
	t.Logf("S-1 non-seekable PASS: Go 在 non-seekable writer 下仍自动启用 ZIP64, read back %d bytes", n)
	t.Logf("  → 说明 photosmove 无需显式预填 UncompressedSize64, Go 自动 data descriptor + ZIP64")
}

// nonSeekableWriter 包装 io.Writer, 屏蔽 Seek 方法, 模拟 http.ResponseWriter
type nonSeekableWriter struct {
	w io.Writer
}

func newNonSeekableWriter(w io.Writer) *nonSeekableWriter {
	return &nonSeekableWriter{w: w}
}

func (n *nonSeekableWriter) Write(p []byte) (int, error) { return n.w.Write(p) }

// spikeZeroReader 流式产生 N 字节零数据, 不占内存
type spikeZeroReader struct{ remaining int64 }

func newSpikeZeroReader(n int64) *spikeZeroReader { return &spikeZeroReader{remaining: n} }

func (z *spikeZeroReader) Read(p []byte) (int, error) {
	if z.remaining <= 0 {
		return 0, io.EOF
	}
	give := int64(len(p))
	if give > z.remaining {
		give = z.remaining
	}
	for i := int64(0); i < give; i++ {
		p[i] = 0
	}
	z.remaining -= give
	return int(give), nil
}

// spikeItoa 简化版 itoa, 避免引入 strconv (减少依赖演示)
func spikeItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
