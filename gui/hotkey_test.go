package main

import (
	"testing"
)

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		input       string
		wantMods    uint32
		wantVK      uint32
		expectErr   bool
		expectedFmt string
	}{
		{
			input:       "Ctrl+Shift+R",
			wantMods:    MOD_CONTROL | MOD_SHIFT,
			wantVK:      0x52, // 'R'
			expectErr:   false,
			expectedFmt: "Ctrl+Shift+R",
		},
		{
			input:       "ctrl + alt + c",
			wantMods:    MOD_CONTROL | MOD_ALT,
			wantVK:      0x43, // 'C'
			expectErr:   false,
			expectedFmt: "Ctrl+Alt+C",
		},
		{
			input:       "F9",
			wantMods:    0,
			wantVK:      0x78, // VK_F9
			expectErr:   false,
			expectedFmt: "F9",
		},
		{
			input:       "Shift+F12",
			wantMods:    MOD_SHIFT,
			wantVK:      0x7B, // VK_F12
			expectErr:   false,
			expectedFmt: "Shift+F12",
		},
		{
			input:       "Ctrl+Space",
			wantMods:    MOD_CONTROL,
			wantVK:      0x20, // VK_SPACE
			expectErr:   false,
			expectedFmt: "Ctrl+Space",
		},
		{
			input:       "Win+Alt+R",
			wantMods:    MOD_WIN | MOD_ALT,
			wantVK:      0x52,
			expectErr:   false,
			expectedFmt: "Alt+Win+R", // Canonical order in FormatHotkey: Ctrl, Alt, Shift, Win
		},
		{
			input:     "R", // Bare letter without modifier must be rejected
			expectErr: true,
		},
		{
			input:     "",
			expectErr: true,
		},
		{
			input:     "Ctrl+Shift", // Missing key
			expectErr: true,
		},
	}

	for _, tc := range tests {
		mods, vk, err := ParseHotkey(tc.input)
		if tc.expectErr {
			if err == nil {
				t.Errorf("ParseHotkey(%q) expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHotkey(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if mods != tc.wantMods {
			t.Errorf("ParseHotkey(%q) mods = 0x%X, want 0x%X", tc.input, mods, tc.wantMods)
		}
		if vk != tc.wantVK {
			t.Errorf("ParseHotkey(%q) vk = 0x%X, want 0x%X", tc.input, vk, tc.wantVK)
		}
		if tc.expectedFmt != "" {
			fmtRes := FormatHotkey(mods, vk)
			if fmtRes != tc.expectedFmt {
				t.Errorf("FormatHotkey(0x%X, 0x%X) = %q, want %q", mods, vk, fmtRes, tc.expectedFmt)
			}
		}
	}
}
