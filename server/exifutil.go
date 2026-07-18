package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	exif "github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"
	jpegstructure "github.com/dsoprea/go-jpeg-image-structure/v2"
)

// readExifDateTime extracts the capture timestamp from a JPEG's EXIF data.
// Tries DateTimeOriginal (0x9003), then DateTime (0x0132).
// Returns the provided fallback if no EXIF or no date tag is found.
func readExifDateTime(data []byte, fallback time.Time) time.Time {
	rawExif, err := exif.SearchAndExtractExif(data)
	if err != nil {
		return fallback
	}

	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return fallback
	}

	ti := exif.NewTagIndex()

	_, index, err := exif.Collect(im, ti, rawExif)
	if err != nil {
		return fallback
	}

	// Try IFD/ExifIFD then IFD for DateTimeOriginal and DateTime
	tagNames := []string{"DateTimeOriginal", "DateTime"}
	ifdPaths := []string{"IFD/ExifIFD", "IFD"}

	for _, tagName := range tagNames {
		for _, ifdPath := range ifdPaths {
			ifd := index.Lookup[ifdPath]
			if ifd == nil {
				continue
			}
			for _, entry := range ifd.Entries() {
				if entry.TagName() == tagName {
					if s, err := entry.Value(); err == nil {
						if t, err := parseExifTime(fmt.Sprintf("%v", s)); err == nil {
							return t
						}
					}
				}
			}
		}
	}

	return fallback
}

// parseExifTime parses EXIF date format "2006:01:02 15:04:05".
func parseExifTime(s string) (time.Time, error) {
	return time.Parse("2006:01:02 15:04:05", s)
}

// readExifDateTimeFromReader reads up to maxBytes from r to extract EXIF date.
// Returns the extracted time, the bytes read (for reuse), and any error.
func readExifDateTimeFromReader(r io.Reader, maxBytes int64, fallback time.Time) (time.Time, []byte, error) {
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, r, maxBytes); err != nil && !errors.Is(err, io.EOF) {
		return fallback, buf.Bytes(), err
	}
	t := readExifDateTime(buf.Bytes(), fallback)
	return t, buf.Bytes(), nil
}

// stripGpsFromJpeg removes GPS IFD tags from a JPEG while preserving all other EXIF data.
func stripGpsFromJpeg(data []byte) ([]byte, error) {
	return stripExifFromJpeg(data, []string{"gps"})
}

// exifCategoryTags maps a privacy category to the IFD paths and tag IDs to
// delete. All known tags must be listed explicitly (IfdBuilder doesn't expose
// a way to enumerate tags from outside the package, so "delete whole IFD"
// is not directly expressible).
var exifCategoryTags = map[string]map[string][]uint16{
	"gps": {
		"IFD/GPSInfo": {
			0x0000, // GPSVersionID
			0x0001, // GPSLatitudeRef
			0x0002, // GPSLatitude
			0x0003, // GPSLongitudeRef
			0x0004, // GPSLongitude
			0x0005, // GPSAltitudeRef
			0x0006, // GPSAltitude
			0x0007, // GPSTimeStamp
			0x0008, // GPSSatellites
			0x0009, // GPSStatus
			0x000A, // GPSMeasureMode
			0x000B, // GPSDOP
			0x000C, // GPSSpeedRef
			0x000D, // GPSSpeed
			0x000E, // GPSTrackRef
			0x000F, // GPSTrack
			0x0010, // GPSImgDirectionRef
			0x0011, // GPSImgDirection
			0x0012, // GPSMapDatum
			0x0013, // GPSDestLatitudeRef
			0x0014, // GPSDestLatitude
			0x0015, // GPSDestLongitudeRef
			0x0016, // GPSDestLongitude
			0x0017, // GPSDestBearingRef
			0x0018, // GPSDestBearing
			0x0019, // GPSDestDistanceRef
			0x001A, // GPSDestDistance
			0x001B, // GPSProcessingMethod
			0x001C, // GPSAreaInformation
			0x001D, // GPSDateStamp
			0x001E, // GPSDifferential
		},
	},
	"time": {
		"IFD":         {0x0132},                 // DateTime
		"IFD/ExifIFD": {0x9003, 0x9004},         // DateTimeOriginal, DateTimeDigitized
		"IFD/GPSInfo": {0x0007, 0x001D},         // GPSTimeStamp, GPSDateStamp
	},
	"device": {
		"IFD":         {0x010F, 0x0110, 0x0131}, // Make, Model, Software
		"IFD/ExifIFD": {0xA431, 0xA434, 0xA435}, // BodySerialNumber, LensModel, LensSerialNumber
	},
	"shot": {
		"IFD/ExifIFD": {
			0x829D, // FNumber
			0x829A, // ExposureTime
			0x920A, // FocalLength
			0x8827, // ISOSpeedRatings
			0x9204, // ExposureBiasValue
			0xA403, // WhiteBalance
		},
	},
	"author": {
		"IFD":         {0x013B, 0x8298}, // Artist, Copyright
		"IFD/ExifIFD": {0xA430},         // OwnerName (CameraOwnerName)
	},
}

// stripExifFromJpeg removes the given EXIF categories from a JPEG while
// preserving all other EXIF data. Unknown categories are ignored.
func stripExifFromJpeg(data []byte, categories []string) ([]byte, error) {
	if len(categories) == 0 {
		return data, nil
	}

	parser := &jpegstructure.JpegMediaParser{}
	mc, err := parser.ParseBytes(data)
	if err != nil {
		return nil, fmt.Errorf("jpeg parse: %w", err)
	}

	sl, ok := mc.(*jpegstructure.SegmentList)
	if !ok {
		return data, nil
	}

	_, _, err = sl.Exif()
	if err != nil {
		// No EXIF segment → nothing to strip
		return data, nil
	}

	rootIb, err := sl.ConstructExifBuilder()
	if err != nil {
		return nil, fmt.Errorf("construct exif builder: %w", err)
	}

	// Build ifdPath → set of tags to delete (union across requested categories)
	rules := map[string]map[uint16]bool{}
	for _, cat := range categories {
		tagMap, ok := exifCategoryTags[cat]
		if !ok {
			continue
		}
		for ifdPath, tags := range tagMap {
			if rules[ifdPath] == nil {
				rules[ifdPath] = map[uint16]bool{}
			}
			for _, t := range tags {
				rules[ifdPath][t] = true
			}
		}
	}

	// Recursively walk the full IFD tree and apply the rules. Must visit:
	//   (1) the root IFD itself (holds Make/Model/DateTime/Artist/Copyright),
	//   (2) every child IFD reached via tag.Value().Ib() (ExifIFD/GPSInfo/Iop),
	//   (3) the sibling chain via NextIb() (IFD0 → IFD1 thumbnail).
	// The previous loop only followed NextIb, which skips the root IFD and
	// every child IFD — so no target tag was ever removed (silent no-op, and
	// Pro's "EXIF strip" feature had zero effect).
	var visit func(ib *exif.IfdBuilder)
	visit = func(ib *exif.IfdBuilder) {
		if ib == nil {
			return
		}
		// Descend into child IFDs first.
		for _, bt := range ib.Tags() {
			if v := bt.Value(); v != nil && v.IsIb() {
				visit(v.Ib())
			}
		}
		// Strip this IFD's own target tags.
		ifdPath := ib.IfdIdentity().UnindexedString()
		if tagsToDelete, applies := rules[ifdPath]; applies {
			for tagId := range tagsToDelete {
				ib.DeleteAll(tagId)
			}
		}
		// Follow the sibling chain.
		if next, err := ib.NextIb(); err == nil {
			visit(next)
		}
	}
	visit(rootIb)

	if err := sl.SetExif(rootIb); err != nil {
		return nil, fmt.Errorf("set exif: %w", err)
	}

	var out bytes.Buffer
	if err := sl.Write(&out); err != nil {
		return nil, fmt.Errorf("jpeg write: %w", err)
	}
	return out.Bytes(), nil
}
