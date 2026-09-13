package main

import "testing"

func TestEntryRelPath(t *testing.T) {
	cases := []struct {
		name     string
		fullPath string
		fallback string
		want     string
	}{
		{"emulated-0", "/storage/emulated/0/DCIM/Camera/IMG_1.jpg", "IMG_1.jpg", "DCIM/Camera/IMG_1.jpg"},
		{"emulated-other-user", "/storage/emulated/1/Pictures/WeChat/image.png", "image.png", "user1/Pictures/WeChat/image.png"},
		{"sdcard", "/sdcard/DCIM/Camera/a.mp4", "a.mp4", "DCIM/Camera/a.mp4"},
		{"self-primary", "/storage/self/primary/DCIM/x.jpg", "x.jpg", "DCIM/x.jpg"},
		{"mnt-user-0", "/mnt/user/0/Pictures/y.png", "y.png", "Pictures/y.png"},
		{"mnt-user-1", "/mnt/user/1/Pictures/y.png", "y.png", "user1/Pictures/y.png"},
		{"removable-volume", "/storage/ABCD-1234/DCIM/Camera/sd.jpg", "sd.jpg", "ABCD-1234/DCIM/Camera/sd.jpg"},
		{"media-rw", "/mnt/media_rw/ABCD-1234/DCIM/Camera/sd.jpg", "sd.jpg", "ABCD-1234/DCIM/Camera/sd.jpg"},
		{"data-media", "/data/media/0/DCIM/Camera/x.jpg", "x.jpg", "DCIM/Camera/x.jpg"},
		{"unknown-root-falls-back", "/home/dev/photos/z.jpg", "z.jpg", "photos/z.jpg"},
		{"traversal-blocked", "/storage/emulated/0/../etc/passwd", "passwd", "_"},
		{"empty-both", "", "", "_"},
		{"empty-fullpath-uses-fallback", "", "photo.jpg", "photo.jpg"},
	}
	for _, c := range cases {
		got := entryRelPath(c.fullPath, c.fallback)
		if got != c.want {
			t.Errorf("%s: entryRelPath(%q) = %q, want %q", c.name, c.fullPath, got, c.want)
		}
	}
}

// Same basename in different albums (same volume) must produce distinct names.
func TestEntryRelPathNoCollisionAcrossAlbums(t *testing.T) {
	a := entryRelPath("/storage/emulated/0/Pictures/Quark/image.png", "image.png")
	b := entryRelPath("/storage/emulated/0/Pictures/WeChat/image.png", "image.png")
	if a == b {
		t.Fatalf("collision: both map to %q", a)
	}
	if a != "Pictures/Quark/image.png" || b != "Pictures/WeChat/image.png" {
		t.Fatalf("unexpected names: %q, %q", a, b)
	}
}

// Internal storage vs removable SD card with the same relative path must NOT collide
// (they are different files); the volume id is kept as a prefix.
func TestEntryRelPathMultiVolumeNoCollision(t *testing.T) {
	internal := entryRelPath("/storage/emulated/0/DCIM/Camera/x.jpg", "x.jpg")
	sd := entryRelPath("/storage/ABCD-1234/DCIM/Camera/x.jpg", "x.jpg")
	if internal == sd {
		t.Fatalf("multi-volume collision: both map to %q", internal)
	}
	if internal != "DCIM/Camera/x.jpg" || sd != "ABCD-1234/DCIM/Camera/x.jpg" {
		t.Fatalf("unexpected names: %q, %q", internal, sd)
	}
	u1 := entryRelPath("/storage/emulated/1/DCIM/Camera/x.jpg", "x.jpg")
	if u1 == internal || u1 != "user1/DCIM/Camera/x.jpg" {
		t.Fatalf("multi-user name = %q", u1)
	}
}

// safeZipName must keep legitimate names that merely contain ".." while still
// blocking real parent-directory traversal.
func TestSafeZipNameDoubleDot(t *testing.T) {
	cases := map[string]string{
		"DCIM/Camera/a..b.jpg": "DCIM/Camera/a..b.jpg",
		"my..dir/photo.jpg":    "my..dir/photo.jpg",
		"../etc/passwd":        "_",
		"a/../../etc":          "_",
	}
	for in, want := range cases {
		if got := safeZipName(in); got != want {
			t.Errorf("safeZipName(%q) = %q, want %q", in, got, want)
		}
	}
}
