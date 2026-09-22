package main

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	WM_HOTKEY          = 0x0312
	WM_UPDATE_HOTKEY   = WM_USER + 10
	ID_HOTKEY_RECENTER = 2001

	MOD_ALT      = 0x0001
	MOD_CONTROL  = 0x0002
	MOD_SHIFT    = 0x0004
	MOD_WIN      = 0x0008
	MOD_NOREPEAT = 0x4000
)

var (
	pRegisterHotKey   = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey = user32.NewProc("UnregisterHotKey")
)

// ParseHotkey parses a shortcut string (e.g. "Ctrl+Shift+R") into Win32 modifiers and virtual key code.
func ParseHotkey(s string) (uint32, uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty hotkey string")
	}

	parts := strings.Split(s, "+")
	var mods uint32
	var vk uint32

	for _, p := range parts {
		token := strings.TrimSpace(strings.ToUpper(p))
		if token == "" {
			continue
		}
		switch token {
		case "CTRL", "CONTROL":
			mods |= MOD_CONTROL
		case "ALT", "OPTION":
			mods |= MOD_ALT
		case "SHIFT":
			mods |= MOD_SHIFT
		case "WIN", "SUPER", "META", "CMD", "COMMAND":
			mods |= MOD_WIN
		case "SPACE":
			vk = 0x20
		case "TAB":
			vk = 0x09
		case "ENTER", "RETURN":
			vk = 0x0D
		case "ESCAPE", "ESC":
			vk = 0x1B
		case "BACKSPACE":
			vk = 0x08
		case "DELETE", "DEL":
			vk = 0x2E
		case "INSERT", "INS":
			vk = 0x2D
		case "HOME":
			vk = 0x24
		case "END":
			vk = 0x23
		case "PAGEUP", "PGUP":
			vk = 0x21
		case "PAGEDOWN", "PGDN":
			vk = 0x22
		case "UP":
			vk = 0x26
		case "DOWN":
			vk = 0x28
		case "LEFT":
			vk = 0x25
		case "RIGHT":
			vk = 0x27
		case "`", "~", "TILDE":
			vk = 0xC0
		case "-", "MINUS":
			vk = 0xBD
		case "=", "EQUAL", "EQUALS":
			vk = 0xBB
		case "[", "BRACKETLEFT":
			vk = 0xDB
		case "]", "BRACKETRIGHT":
			vk = 0xDD
		case ";", "SEMICOLON":
			vk = 0xBA
		case "'", "QUOTE":
			vk = 0xDE
		case ",", "COMMA":
			vk = 0xBC
		case ".", "PERIOD":
			vk = 0xBE
		case "/", "SLASH":
			vk = 0xBF
		case "\\", "BACKSLASH":
			vk = 0xDC
		case "NUMADD", "NUM+":
			vk = 0x6B
		case "NUMSUBTRACT", "NUM-":
			vk = 0x6D
		case "NUMMULTIPLY", "NUM*":
			vk = 0x6A
		case "NUMDIVIDE", "NUM/":
			vk = 0x6F
		case "NUMDECIMAL", "NUM.":
			vk = 0x6E
		default:
			// Check Function keys F1-F24
			if strings.HasPrefix(token, "F") && len(token) >= 2 {
				if num, err := strconv.Atoi(token[1:]); err == nil {
					if num >= 1 && num <= 12 {
						vk = uint32(0x70 + num - 1)
						break
					} else if num >= 13 && num <= 24 {
						vk = uint32(0x7C + num - 13)
						break
					}
				}
			}
			// Check Numpad keys NUM0-NUM9
			if strings.HasPrefix(token, "NUM") && len(token) == 4 {
				if num, err := strconv.Atoi(token[3:]); err == nil && num >= 0 && num <= 9 {
					vk = uint32(0x60 + num)
					break
				}
			}
			// Check single letter A-Z
			if len(token) == 1 && token[0] >= 'A' && token[0] <= 'Z' {
				vk = uint32(token[0])
				break
			}
			// Check single digit 0-9
			if len(token) == 1 && token[0] >= '0' && token[0] <= '9' {
				vk = uint32(token[0])
				break
			}
			return 0, 0, fmt.Errorf("unrecognized key: %s", p)
		}
	}

	if vk == 0 {
		return 0, 0, fmt.Errorf("missing key in shortcut")
	}

	// Safety: bare letter or number without modifier is not allowed to prevent intercepting normal typing OS-wide
	isFuncKey := (vk >= 0x70 && vk <= 0x87)
	if mods == 0 && !isFuncKey {
		return 0, 0, fmt.Errorf("shortcut must include at least one modifier (Ctrl, Alt, Shift) or be a function key (F1-F12)")
	}

	return mods, vk, nil
}

// FormatHotkey converts modifiers and virtual key code back to standard representation.
func FormatHotkey(mods uint32, vk uint32) string {
	var parts []string
	if mods&MOD_CONTROL != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&MOD_ALT != 0 {
		parts = append(parts, "Alt")
	}
	if mods&MOD_SHIFT != 0 {
		parts = append(parts, "Shift")
	}
	if mods&MOD_WIN != 0 {
		parts = append(parts, "Win")
	}

	var keyName string
	switch {
	case vk >= 'A' && vk <= 'Z':
		keyName = string(rune(vk))
	case vk >= '0' && vk <= '9':
		keyName = string(rune(vk))
	case vk >= 0x70 && vk <= 0x7B:
		keyName = fmt.Sprintf("F%d", vk-0x70+1)
	case vk == 0x20:
		keyName = "Space"
	case vk == 0x09:
		keyName = "Tab"
	case vk == 0x0D:
		keyName = "Enter"
	case vk == 0x1B:
		keyName = "Esc"
	case vk == 0x08:
		keyName = "Backspace"
	case vk == 0x2E:
		keyName = "Delete"
	case vk == 0x2D:
		keyName = "Insert"
	case vk == 0x24:
		keyName = "Home"
	case vk == 0x23:
		keyName = "End"
	case vk == 0x21:
		keyName = "PageUp"
	case vk == 0x22:
		keyName = "PageDown"
	case vk == 0x26:
		keyName = "Up"
	case vk == 0x28:
		keyName = "Down"
	case vk == 0x25:
		keyName = "Left"
	case vk == 0x27:
		keyName = "Right"
	case vk == 0xC0:
		keyName = "~"
	case vk == 0xBD:
		keyName = "-"
	case vk == 0xBB:
		keyName = "="
	case vk == 0xDB:
		keyName = "["
	case vk == 0xDD:
		keyName = "]"
	case vk == 0xBA:
		keyName = ";"
	case vk == 0xDE:
		keyName = "'"
	case vk == 0xBC:
		keyName = ","
	case vk == 0xBE:
		keyName = "."
	case vk == 0xBF:
		keyName = "/"
	case vk == 0xDC:
		keyName = "\\"
	case vk >= 0x60 && vk <= 0x69:
		keyName = fmt.Sprintf("Num%d", vk-0x60)
	case vk == 0x6B:
		keyName = "NumAdd"
	case vk == 0x6D:
		keyName = "NumSubtract"
	case vk == 0x6A:
		keyName = "NumMultiply"
	case vk == 0x6F:
		keyName = "NumDivide"
	case vk == 0x6E:
		keyName = "NumDecimal"
	default:
		keyName = fmt.Sprintf("0x%X", vk)
	}

	if keyName != "" {
		parts = append(parts, keyName)
	}
	return strings.Join(parts, "+")
}
