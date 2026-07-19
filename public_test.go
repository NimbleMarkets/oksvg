// Copyright 2018 The oksvg Authors. All rights reserved.
// created: 2018 by S.R.Wiley
package oksvg_test

import (
	"image"
	"testing"

	. "github.com/NimbleMarkets/oksvg"
	. "github.com/srwiley/rasterx"
	// . "github.com/srwiley/scanFT"
)

// ReadIconSet reads every named icon from folder, failing tb immediately with
// a clear message identifying the file if any read errors out. Previously a
// failed read was silently dropped, which let benchmarks quietly run against
// a shorter icon list (or panic on beachIcons[0] if the very first file
// failed to load) instead of reporting the real problem.
func ReadIconSet(tb testing.TB, folder string, paths []string) (icons []*SvgIcon) {
	tb.Helper()
	for _, p := range paths {
		file := folder + p + ".svg"
		icon, errSvg := ReadIcon(file, IgnoreErrorMode)
		if errSvg != nil {
			tb.Fatalf("ReadIconSet: failed to read %q: %v", file, errSvg)
		}
		icons = append(icons, icon)
	}
	return
}

func BenchmarkLandscapeIcons(b *testing.B) {
	var (
		beachIconNames = []string{
			"beach", "cape", "iceberg", "island",
			"mountains", "sea", "trees", "village"}
		beachIcons = ReadIconSet(b, "testdata/landscapeIcons/", beachIconNames)
		w, h       = int(beachIcons[0].ViewBox.W), int(beachIcons[0].ViewBox.H)
		img        = image.NewRGBA(image.Rect(0, 0, w, h))
		//source     = image.NewUniform(color.NRGBA{0, 0, 0, 255})
		scannerGV = NewScannerGV(w, h, img, img.Bounds())
		raster    = NewDasher(w, h, scannerGV)

		//	painter   = NewRGBAPainter(img)
		//	scannerFT = NewScannerFT(w, h, painter)
		//	raster    = NewDasher(w, h, scannerFT)
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, ic := range beachIcons {
			ic.Draw(raster, 1.0)
		}
	}
}

func BenchmarkSportsIcons(b *testing.B) {
	var (
		sportsIconNames = []string{
			"archery", "fencing", "rugby_sevens",
			"artistic_gymnastics", "football", "sailing",
			"athletics", "golf", "shooting",
			"badminton", "handball", "swimming",
			"basketball", "hockey", "synchronised_swimming",
			"beach_volleyball", "judo", "table_tennis",
			"boxing", "marathon_swimming", "taekwondo",
			"canoe_slalom", "modern_pentathlon", "tennis",
			"canoe_sprint", "olympic_medal_bronze", "trampoline_gymnastics",
			"cycling_bmx", "olympic_medal_gold", "triathlon",
			"cycling_mountain_bike", "olympic_medal_silver", "trophy",
			"cycling_road", "olympic_torch", "volleyball",
			"cycling_track", "water_polo",
			"diving", "rhythmic_gymnastics", "weightlifting",
			"equestrian", "rowing", "wrestling"}
		sportsIcons = ReadIconSet(b, "testdata/sportsIcons/", sportsIconNames)
		w2, h2      = int(sportsIcons[0].ViewBox.W), int(sportsIcons[0].ViewBox.H)
		img2        = image.NewRGBA(image.Rect(0, 0, w2, h2))
		scannerGV2  = NewScannerGV(w2, h2, img2, image.Rect(0, 0, w2, h2))
		raster2     = NewDasher(w2, h2, scannerGV2)

		//	painter2   = NewRGBAPainter(img2)
		//	scannerFT2 = NewScannerFT(w2, h2, painter2)
		//	raster2    = NewDasher(w2, h2, scannerFT2)
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, ic := range sportsIcons {
			ic.Draw(raster2, 1.0)
		}
	}
}
