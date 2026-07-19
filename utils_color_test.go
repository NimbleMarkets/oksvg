// Copyright 2026 The oksvg Authors. All rights reserved.
//
// utils_color_test.go tests the hardening of parseColorValue and related
// helpers in utils.go against malformed/untrusted SVG color input.
package oksvg

import (
	"testing"
)

// TestParseColorValue exercises parseColorValue with a table of inputs,
// including the malformed empty-component case extracted from
// `<rect fill="rgb(1,,1)"/>` which used to panic on v[len(v)-1].
func TestParseColorValue(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    uint8
		wantErr bool
	}{
		{name: "empty string errors, does not panic", in: "", want: 0, wantErr: true},
		{name: "padded 50 percent rounds and clamps", in: " 50% ", want: 128, wantErr: false},
		{name: "over 100 percent clamps to 255", in: "200%", want: 255, wantErr: false},
		{name: "negative integer clamps to 0", in: "-5", want: 0, wantErr: false},
		{name: "integer over 255 clamps to 255", in: "300", want: 255, wantErr: false},
		{name: "float component rounds", in: "127.5", want: 128, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseColorValue(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseColorValue(%q) = (%d, nil); want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseColorValue(%q) returned unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseColorValue(%q) = %d; want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseColorValueNegativePercentClamps covers the negative-percentage
// clamp case called out in the task brief (rgb(-5%,0,0) -> 0), kept separate
// from the table above since it wasn't part of the required table but is
// part of the documented behavior contract.
func TestParseColorValueNegativePercentClamps(t *testing.T) {
	got, err := parseColorValue("-5%")
	if err != nil {
		t.Fatalf("parseColorValue(\"-5%%\") returned unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("parseColorValue(\"-5%%\") = %d; want 0", got)
	}
}

// TestParseColorValueNoPanicOnMissingComponent reproduces the exact crash
// scenario from `<rect fill="rgb(1,,1)"/>`: splitting "1,,1" on commas
// yields a middle component of "". parseColorValue must return an error
// instead of panicking on v[len(v)-1].
func TestParseColorValueNoPanicOnMissingComponent(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parseColorValue(\"\") panicked: %v", r)
		}
	}()
	if _, err := parseColorValue(""); err == nil {
		t.Fatal("parseColorValue(\"\") = nil error; want error for missing rgb() component")
	}
}

// TestSplitOnCommaOrSpaceWhitespaceSeparators verifies that tab, newline and
// carriage return are treated as separators alongside comma and space, so
// inputs like stroke-dasharray="5,3\t2" don't lose components.
func TestSplitOnCommaOrSpaceWhitespaceSeparators(t *testing.T) {
	got := splitOnCommaOrSpace("5,3\t2\n1\r4")
	want := []string{"5", "3", "2", "1", "4"}
	if len(got) != len(want) {
		t.Fatalf("splitOnCommaOrSpace(...) = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("splitOnCommaOrSpace(...)[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}
