package slips

import (
	"fmt"
	"strings"
)

// Code 39 barcode rendering, as inline SVG.
//
// A printed gate pass is worth much more if the guard can scan it than if they
// have to key fourteen characters into a terminal in the dark. Code 39 is the
// right symbology for this: it encodes exactly the characters our document
// numbers and verification tokens use, every warehouse scanner on the market
// reads it without configuration, and — unlike QR — it can be produced correctly
// in a hundred lines with no dependency, which matters for a service that has to
// build reproducibly on a machine with no network.
//
// Each character is nine elements: five bars and four spaces, three of which are
// wide. Characters are separated by one narrow space, and the whole symbol is
// framed by the '*' start/stop character.

const (
	narrowWidth = 2
	wideWidth   = 6
	barHeight   = 56
	quietZone   = 20
)

// code39 maps each encodable character to its nine elements, alternating bar and
// space starting with a bar. 'w' is a wide element, 'n' a narrow one.
var code39 = map[rune]string{
	'0': "nnnwwnwnn", '1': "wnnwnnnnw", '2': "nnwwnnnnw", '3': "wnwwnnnnn",
	'4': "nnnwwnnnw", '5': "wnnwwnnnn", '6': "nnwwwnnnn", '7': "nnnwnnwnw",
	'8': "wnnwnnwnn", '9': "nnwwnnwnn",
	'A': "wnnnnwnnw", 'B': "nnwnnwnnw", 'C': "wnwnnwnnn", 'D': "nnnnwwnnw",
	'E': "wnnnwwnnn", 'F': "nnwnwwnnn", 'G': "nnnnnwwnw", 'H': "wnnnnwwnn",
	'I': "nnwnnwwnn", 'J': "nnnnwwwnn", 'K': "wnnnnnnww", 'L': "nnwnnnnww",
	'M': "wnwnnnnwn", 'N': "nnnnwnnww", 'O': "wnnnwnnwn", 'P': "nnwnwnnwn",
	'Q': "nnnnnnwww", 'R': "wnnnnnwwn", 'S': "nnwnnnwwn", 'T': "nnnnwnwwn",
	'U': "wwnnnnnnw", 'V': "nwwnnnnnw", 'W': "wwwnnnnnn", 'X': "nwnnwnnnw",
	'Y': "wwnnwnnnn", 'Z': "nwwnwnnnn",
	'-': "nwnnnnwnw", '.': "wwnnnnwnn", ' ': "nwwnnnwnn", '$': "nwnwnwnnn",
	'/': "nwnwnnnwn", '+': "nwnnnwnwn", '%': "nnnwnwnwn", '*': "nwnnwnwnn",
}

// Encodable reports whether every character of s can be represented. Callers use
// it to decide whether a value is safe to put on a printed document at all — an
// unscannable barcode on a gate pass is worse than none, because the guard will
// trust it and wave the lorry through when it fails to read.
func Encodable(s string) bool {
	for _, r := range strings.ToUpper(s) {
		if _, ok := code39[r]; !ok {
			return false
		}
	}
	return true
}

// Barcode39SVG renders s as a Code 39 symbol. Unencodable input returns an empty
// string rather than a partial symbol, so a caller that ignores the result
// prints nothing instead of printing something wrong.
func Barcode39SVG(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || !Encodable(s) {
		return ""
	}

	var bars strings.Builder
	x := quietZone
	emit := func(pattern string) {
		for i, el := range pattern {
			w := narrowWidth
			if el == 'w' {
				w = wideWidth
			}
			if i%2 == 0 { // even elements are bars, odd are spaces
				fmt.Fprintf(&bars, `<rect x="%d" y="0" width="%d" height="%d"/>`, x, w, barHeight)
			}
			x += w
		}
		x += narrowWidth // inter-character gap
	}

	emit(code39['*'])
	for _, r := range s {
		emit(code39[r])
	}
	emit(code39['*'])

	width := x + quietZone - narrowWidth
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="barcode %s">`+
			`<rect width="%d" height="%d" fill="#fff"/><g fill="#000">%s</g></svg>`,
		width, barHeight, width, barHeight, s, width, barHeight, bars.String())
}
