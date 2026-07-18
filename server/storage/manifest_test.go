package storage

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSpaceReaderFillsWithSpaces(t *testing.T) {
	buf := make([]byte, 16)
	n, err := spaceReader{}.Read(buf)
	if err != nil {
		t.Fatalf("Read returned err: %v", err)
	}
	if n != 16 {
		t.Fatalf("Read returned n=%d want 16", n)
	}
	for i, b := range buf {
		if b != ' ' {
			t.Fatalf("byte %d = %#x want ' '", b, i)
		}
	}
}

func TestManifestReservedSizeFormula(t *testing.T) {
	cases := []struct {
		name         string
		filesCount   int
		wantMinBytes int64
	}{
		{"empty", 0, 4096},
		{"100 files", 100, 100*300 + 4096},
		{"1000 files", 1000, 1000*300 + 10000}, // slack 10*1000=10000 > 4096
		{"10000 files", 10000, 10000 * 300},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ManifestReservedSize(c.filesCount)
			if got < c.wantMinBytes {
				t.Errorf("got=%d, want >= %d", got, c.wantMinBytes)
			}
		})
	}
}

func TestWriteManifestPadsWithSpaces(t *testing.T) {
	payload := ManifestPayload{
		Schema:  ManifestSchemaVersion,
		Session: "test-session",
		BatchID: "test-batch",
		Files: []ManifestEntry{
			{Path: "2024/01/IMG_0001.jpg", OriginalPath: "/DCIM/Camera/IMG_0001.heic", Converted: true, Size: 3145728, Sha256: strings.Repeat("a", 64)},
		},
	}
	reserved := ManifestReservedSize(1)

	var buf bytes.Buffer
	written, err := WriteManifest(&buf, payload, reserved)
	if err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if written != reserved {
		t.Errorf("written=%d want reserved=%d", written, reserved)
	}
	if int64(buf.Len()) != reserved {
		t.Errorf("buf.Len=%d want reserved=%d", buf.Len(), reserved)
	}

	// JSON payload must be parseable despite the trailing spaces.
	var parsed ManifestPayload
	raw := bytes.TrimRight(buf.Bytes(), " ")
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed.Schema != ManifestSchemaVersion {
		t.Errorf("parsed.Schema=%d want %d", parsed.Schema, ManifestSchemaVersion)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Sha256 != strings.Repeat("a", 64) {
		t.Errorf("parsed.Files mismatch: %+v", parsed.Files)
	}
}

func TestWriteManifestOverflowReturnsError(t *testing.T) {
	// Reserved too small to hold even the schema field.
	payload := ManifestPayload{Schema: ManifestSchemaVersion, Files: make([]ManifestEntry, 100)}
	var buf bytes.Buffer
	_, err := WriteManifest(&buf, payload, 10)
	if err != ErrManifestOverflow {
		t.Errorf("err=%v want ErrManifestOverflow", err)
	}
}
