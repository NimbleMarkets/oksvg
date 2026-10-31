package oksvg

import (
	"bytes"
	"fmt"
	"testing"
)

func TestRectRadii(t *testing.T) {
	for _, tc := range []struct {
		name, attrs, want string
		width, height     int
		rounded           bool
	}{
		{"rx only", `rx="15"`, `rx="15" ry="15"`, 40, 40, true},
		{"ry only", `ry="15"`, `rx="15" ry="15"`, 40, 40, true},
		{"neither", ``, `rx="0" ry="0"`, 40, 40, false},
		{"zero rx", `rx="0" ry="15"`, ``, 40, 40, false},
		{"zero ry", `rx="15" ry="0"`, ``, 40, 40, false},
		{"negative rx", `rx="-5" ry="15"`, `rx="15" ry="15"`, 40, 40, true},
		{"negative ry", `rx="15" ry="-5"`, `rx="15" ry="15"`, 40, 40, true},
		{"both negative", `rx="-5" ry="-10"`, ``, 40, 40, false},
		{"negative rx alone", `rx="-5"`, ``, 40, 40, false},
		{"negative ry alone", `ry="-5"`, ``, 40, 40, false},
		{"clamp both", `rx="100" ry="100"`, `rx="30" ry="20"`, 60, 40, true},
		{"copy rx before clamp", `rx="25"`, `rx="10" ry="25"`, 20, 60, true},
		{"copy ry before clamp", `ry="25"`, `rx="25" ry="10"`, 60, 20, true},
		{"independent radii", `ry="10" rx="20"`, `rx="20" ry="10"`, 60, 40, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svg := func(attrs string) string {
				return fmt.Sprintf(`<svg width="%d" height="%d"><rect width="%d" height="%d" %s fill="red"/></svg>`, tc.width, tc.height, tc.width, tc.height, attrs)
			}
			got := renderSVG(t, svg(tc.attrs), tc.width, tc.height, StrictErrorMode)
			want := renderSVG(t, svg(tc.want), tc.width, tc.height, StrictErrorMode)
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Fatal("render differs from rectangle with resolved radii")
			}
			cornerAlpha := got.RGBAAt(0, 0).A
			if (tc.rounded && cornerAlpha != 0) || (!tc.rounded && cornerAlpha != 255) {
				t.Fatalf("corner alpha = %d, rounded = %v", cornerAlpha, tc.rounded)
			}
			if got.RGBAAt(tc.width/2, tc.height/2).A != 255 {
				t.Fatal("rectangle center is not opaque")
			}
		})
	}
}
