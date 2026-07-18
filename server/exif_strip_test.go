package main

// 端到端 EXIF 抹除回归测试.
//
// 背景: stripExifFromJpeg 曾用 IfdBuilder.NextIb() 遍历 IFD 树, 而 NextIb 是
// EXIF 兄弟 IFD 链 (IFD0→IFD1 缩略图), 既跳过 rootIb 自身 (Make/Model/DateTime/
// Artist/Copyright) 又进不了子 IFD (ExifIFD/GPSInfo), 导致 Pro 模式 EXIF 5 类抹除
// 实际是静默 no-op. 这组测试用真实带 EXIF 的 JPEG 断言抹除真的生效, 防止回退.
//
// 样本来自 dsoprea module cache (真实相机/手机照片), 见 testdata/exif-samples/.
// 缺失时 skip 而非 fail (CI 无样本环境).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	exif "github.com/dsoprea/go-exif/v3"
	jpegstructure "github.com/dsoprea/go-jpeg-image-structure/v2"
)

// enumerateExifTags 扁平化 JPEG 全树所有 (ifdPath, tagId) 为 "ifdPath|0xHHHH" key 集合.
// 递归覆盖 root + 子 IFD + 兄弟链 (与 stripExifFromJpeg 修复后的遍历同构).
func enumerateExifTags(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	parser := &jpegstructure.JpegMediaParser{}
	mc, err := parser.ParseBytes(data)
	if err != nil {
		t.Fatalf("jpeg parse: %v", err)
	}
	sl, ok := mc.(*jpegstructure.SegmentList)
	if !ok {
		t.Fatalf("not segment list")
	}
	rootIfd, _, err := sl.Exif()
	if err != nil {
		return map[string]bool{} // 无 EXIF
	}
	out := map[string]bool{}
	var walk func(*exif.Ifd)
	walk = func(ifd *exif.Ifd) {
		if ifd == nil {
			return
		}
		for _, e := range ifd.DumpTags() {
			out[fmt.Sprintf("%s|0x%04X", e.IfdPath(), e.TagId())] = true
		}
		for _, c := range ifd.Children() {
			walk(c)
		}
		walk(ifd.NextIfd())
	}
	walk(rootIfd)
	return out
}

// catKeys 返回某类别所有 (ifdPath, tagId) key (来自 exifCategoryTags).
func catKeys(cat string) map[string]bool {
	out := map[string]bool{}
	for path, ids := range exifCategoryTags[cat] {
		for _, id := range ids {
			out[fmt.Sprintf("%s|0x%04X", path, id)] = true
		}
	}
	return out
}

// exifSampleJpgs 返回真实带 EXIF 的测试 JPEG. 优先用项目 testdata (本地开发),
// 缺失时自举自 dsoprea module cache (Go 依赖必有, CI 无 testdata 也能跑).
// gps.jpg (197KB) 跨 IFD0 (Make/Model) + GPSInfo + ExifIFD, 覆盖 gps/time/device.
func exifSampleJpgs(t *testing.T) []string {
	t.Helper()
	if ms, _ := filepath.Glob("testdata/exif-samples/*.jpg"); len(ms) > 0 {
		return ms
	}
	gmc := os.Getenv("GOMODCACHE")
	if gmc == "" {
		home, _ := os.UserHomeDir()
		gmc = filepath.Join(home, "go/pkg/mod")
	}
	if m, _ := filepath.Glob(filepath.Join(gmc, "github.com/dsoprea/go-exif@*/assets/gps.jpg")); len(m) > 0 {
		return m
	}
	t.Skip("no EXIF sample: put one in testdata/exif-samples/ or run where dsoprea cache exists")
	return nil
}

func jpegDecodes(t *testing.T, data []byte, label string) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(strings.NewReader(string(data)))
	if err != nil {
		t.Errorf("%s: post-strip JPEG undecodable: %v", label, err)
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		t.Errorf("%s: post-strip JPEG bad dims %dx%d", label, cfg.Width, cfg.Height)
	}
}

// TestExifStrip_RealJpeg_RemovesTargetTags: 全 5 类抹除, 目标必须消失, 非目标保留,
// 无新增 tag, JPEG 仍可解码, 字节 hash 必变 (不再是 no-op).
func TestExifStrip_RealJpeg_RemovesTargetTags(t *testing.T) {
	samples := exifSampleJpgs(t)
	if len(samples) == 0 {
		t.Skip("no exif samples in testdata/exif-samples")
	}
	sort.Strings(samples)

	allCats := []string{"gps", "time", "device", "shot", "author"}
	fullTarget := map[string]bool{}
	for _, c := range allCats {
		for k := range catKeys(c) {
			fullTarget[k] = true
		}
	}

	totalHit := 0
	for _, sp := range samples {
		name := filepath.Base(sp)
		data, err := os.ReadFile(sp)
		if err != nil {
			t.Logf("skip %s: %v", name, err)
			continue
		}
		before := enumerateExifTags(t, data)

		stripped, err := stripExifFromJpeg(data, allCats)
		if err != nil {
			// FUJI 等含 unparseable tag 的样本: ConstructExifBuilder 报错,
			// archiver 会回退原图. 记录但不 fail (已知 dsoprea 限制).
			t.Logf("[%s] strip returned err (expected for some cameras): %v", name, err)
			continue
		}
		after := enumerateExifTags(t, stripped)

		h := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:12]) }
		if h(data) == h(stripped) {
			t.Errorf("%s: JPEG hash unchanged after strip (silent no-op)", name)
		}

		hit := 0
		for k := range fullTarget {
			if before[k] {
				hit++
			}
		}
		totalHit += hit

		var leaked, added, lost []string
		for k := range after {
			if fullTarget[k] && before[k] {
				leaked = append(leaked, k)
			}
			if !before[k] {
				added = append(added, k)
			}
		}
		for k := range before {
			if !fullTarget[k] && !after[k] {
				lost = append(lost, k)
			}
		}
		sort.Strings(leaked)
		sort.Strings(added)
		sort.Strings(lost)
		t.Logf("[%s] before=%d target_hit=%d -> after=%d | leaked=%d added=%d non_target_lost=%d",
			name, len(before), hit, len(after), len(leaked), len(added), len(lost))
		if len(leaked) > 0 {
			t.Errorf("%s: STRIP LEAKED %d target tags: %v", name, len(leaked), leaked)
		}
		if len(added) > 0 {
			t.Errorf("%s: strip ADDED %d tags: %v", name, len(added), added)
		}
		if len(lost) > 0 {
			t.Errorf("%s: COLLATERAL non-target lost %d: %v", name, len(lost), lost)
		}
		jpegDecodes(t, stripped, name)
	}
	if totalHit == 0 {
		t.Fatalf("no sample carried any target tag — test has zero coverage")
	}
	t.Logf("total target tags hit across samples: %d", totalHit)
}

// TestExifStrip_PerCategory_NoCollateral: 只 strip 一类, 不得误删非该类目标.
// 注: GPSTimeStamp(0x0007)/GPSDateStamp(0x001D) 同时归入 gps 与 time 类
// (见 exifCategoryTags), strip gps 删它们是正确的, 不算误伤.
func TestExifStrip_PerCategory_NoCollateral(t *testing.T) {
	samples := exifSampleJpgs(t)
	if len(samples) == 0 {
		t.Skip("no samples")
	}
	sort.Strings(samples)
	cats := []string{"gps", "time", "device", "shot", "author"}

	for _, sp := range samples {
		name := filepath.Base(sp)
		data, err := os.ReadFile(sp)
		if err != nil {
			continue
		}
		before := enumerateExifTags(t, data)

		for _, cat := range cats {
			thisTarget := catKeys(cat)
			stripped, err := stripExifFromJpeg(data, []string{cat})
			if err != nil {
				continue
			}
			after := enumerateExifTags(t, stripped)

			var leaked, collateral []string
			// 本类目标: 存在的应被删.
			for k := range thisTarget {
				if before[k] && after[k] {
					leaked = append(leaked, k)
				}
			}
			// 误伤 = 被删且不属于当前类目标 (含非目标 tag 与其他类独有 tag).
			for k := range before {
				if before[k] && !after[k] && !thisTarget[k] {
					collateral = append(collateral, k)
				}
			}
			if len(leaked) > 0 {
				t.Errorf("%s[%s]: leaked %d: %v", name, cat, len(leaked), leaked)
			}
			if len(collateral) > 0 {
				t.Errorf("%s[%s]: collateral %d: %v", name, cat, len(collateral), collateral)
			}
		}
	}
}

// TestExifStrip_DateTimeErased: strip time 类后, 生产函数 readExifDateTime
// 必须读不出 DateTimeOriginal (归零). 用独立的生产代码路径交叉验证枚举结果.
func TestExifStrip_DateTimeErased(t *testing.T) {
	samples := exifSampleJpgs(t)
	if len(samples) == 0 {
		t.Skip("no samples")
	}
	sort.Strings(samples)
	checked := 0
	for _, sp := range samples {
		name := filepath.Base(sp)
		data, err := os.ReadFile(sp)
		if err != nil {
			continue
		}
		before := readExifDateTime(data, time.Time{})
		if before.IsZero() {
			continue
		}
		stripped, err := stripExifFromJpeg(data, []string{"time"})
		if err != nil {
			continue
		}
		after := readExifDateTime(stripped, time.Time{})
		checked++
		t.Logf("[%s] DateTimeOriginal: before=%v after=%v", name, before, after)
		if !after.IsZero() {
			t.Errorf("%s: DateTimeOriginal survived time-strip: %v", name, after)
		}
	}
	if checked == 0 {
		t.Skip("no sample had a DateTimeOriginal to verify")
	}
}
