package main

// End-to-end EXIF stripping regression tests.
//
// Background: stripExifFromJpeg used to traverse the IFD tree with
// IfdBuilder.NextIb(), but NextIb walks the EXIF sibling IFD chain (IFD0→IFD1
// thumbnail), which both skips rootIb itself (Make/Model/DateTime/Artist/
// Copyright) and cannot enter child IFDs (ExifIFD/GPSInfo), making the Pro
// mode 5-category EXIF stripping effectively a silent no-op. These tests use
// real EXIF-bearing JPEGs to assert stripping actually takes effect and
// prevent regression.
//
// Samples come from the dsoprea module cache (real camera/phone photos), see
// testdata/exif-samples/. Skip instead of fail when missing (CI has no sample
// environment).

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

// enumerateExifTags flattens every (ifdPath, tagId) of the full JPEG tree into
// an "ifdPath|0xHHHH" key set. Recursively covers root + child IFDs + sibling
// chain (isomorphic to stripExifFromJpeg's post-fix traversal).
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
		return map[string]bool{} // no EXIF
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

// catKeys returns all (ifdPath, tagId) keys of a category (from exifCategoryTags).
func catKeys(cat string) map[string]bool {
	out := map[string]bool{}
	for path, ids := range exifCategoryTags[cat] {
		for _, id := range ids {
			out[fmt.Sprintf("%s|0x%04X", path, id)] = true
		}
	}
	return out
}

// exifSampleJpgs returns real EXIF-bearing test JPEGs. Prefers project testdata
// (local dev); when missing, bootstraps from the dsoprea module cache (Go deps
// always exist, so CI without testdata can still run).
// gps.jpg (197KB) spans IFD0 (Make/Model) + GPSInfo + ExifIFD, covering gps/time/device.
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

// TestExifStrip_RealJpeg_RemovesTargetTags: strip all 5 categories — targets
// must disappear, non-targets kept, no new tags added, JPEG still decodable,
// byte hash must change (no longer a no-op).
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
			// Samples like FUJI that contain unparseable tags: ConstructExifBuilder
			// errors out and the archiver falls back to the original image. Log but
			// do not fail (known dsoprea limitation).
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

// TestExifStrip_PerCategory_NoCollateral: strip one category only; must not
// delete anything outside that category's targets.
// Note: GPSTimeStamp(0x0007)/GPSDateStamp(0x001D) belong to both the gps and
// time categories (see exifCategoryTags), so stripping gps deleting them is
// correct, not collateral damage.
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
			// This category's targets: present ones must be deleted.
			for k := range thisTarget {
				if before[k] && after[k] {
					leaked = append(leaked, k)
				}
			}
			// Collateral = deleted but not part of the current category targets
			// (includes non-target tags and tags unique to other categories).
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

// TestExifStrip_DateTimeErased: after stripping the time category, the
// production function readExifDateTime must no longer read DateTimeOriginal
// (zeroed). Cross-validates the enumeration result via an independent
// production code path.
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
