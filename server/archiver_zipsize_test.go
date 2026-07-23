package main

import (
	"strconv"
	"testing"
)

// TestCalculateZipSizeMatchesLegacy asserts the math-based calculateZipSize
// is byte-for-byte identical to calculateZipSizeLegacy (the old zip.Writer
// simulation) across the shapes that exercise different ZIP-structure paths:
// empty, small, zero-byte, mixed media, long names, FlatMode, no-manifest,
// zip64 (>4GB single file), and high file count.
func TestCalculateZipSizeMatchesLegacy(t *testing.T) {
	cases := []struct {
		name  string
		files []FileEntry
		opts  ZipWriteOptions
	}{
		{"empty", nil, ZipWriteOptions{EmitManifest: true}},
		{"single_small", []FileEntry{{Path: "a.jpg", Size: 100, ModTime: 1234567890}}, ZipWriteOptions{EmitManifest: true}},
		{"zero_byte", []FileEntry{{Path: "empty.txt", Size: 0, ModTime: 1234567890}}, ZipWriteOptions{EmitManifest: true}},
		{"mixed_media", []FileEntry{
			{Path: "DCIM/IMG_001.jpg", Size: 5 * 1024 * 1024, ModTime: 1234567890},
			{Path: "DCIM/VID_001.mp4", Size: 500 * 1024 * 1024, ModTime: 1234567891},
			{Path: "DCIM/IMG_002.heic", Size: 3 * 1024 * 1024, ModTime: 1234567892},
		}, ZipWriteOptions{EmitManifest: true}},
		{"long_name", []FileEntry{{Path: "DCIM/very_long_filename_for_testing_padding_alignment_in_local_header_and_central_directory_record_fields.jpg", Size: 1024, ModTime: 1234567890}}, ZipWriteOptions{EmitManifest: true}},
		{"flat_mode", []FileEntry{
			{Path: "DCIM/Camera/a.jpg", Size: 1024, ModTime: 1234567890},
			{Path: "DCIM/Camera/b.jpg", Size: 2048, ModTime: 1234567891},
		}, ZipWriteOptions{FlatMode: true, EmitManifest: true}},
		{"no_manifest", []FileEntry{{Path: "a.jpg", Size: 100, ModTime: 1234567890}}, ZipWriteOptions{}},
		// zip64: single file just over 4GB triggers dataDescriptor64 + zip64 extra in CD
		{"zip64_big_file", []FileEntry{{Path: "big.mp4", Size: 4*1024*1024*1024 + 1024, ModTime: 1234567890}}, ZipWriteOptions{EmitManifest: true}},
		{"many_files", makeZipSizeFiles(10000, 1024), ZipWriteOptions{EmitManifest: true}},
		// Real-world 10GB+ album shape: total >4GB but each file <4GB.
		// Triggers EOCD zip64 (localSum>=4GB) + per-file central-dir zip64
		// extra for files whose local-header offset crosses 4GB.
		{"total_over_4gb", makeZipSizeFiles(1000, 5 * 1024 * 1024), ZipWriteOptions{EmitManifest: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := calculateZipSize(c.files, c.opts)
			want := calculateZipSizeLegacy(c.files, c.opts)
			if got != want {
				t.Errorf("calculateZipSize=%d legacy=%d diff=%d", got, want, got-want)
			}
		})
	}
}

func makeZipSizeFiles(n int, size int64) []FileEntry {
	files := make([]FileEntry, n)
	for i := range files {
		files[i] = FileEntry{Path: "f" + strconv.Itoa(i) + ".dat", Size: size, ModTime: 1234567890}
	}
	return files
}
