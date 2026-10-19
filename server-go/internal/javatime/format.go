package javatime

import (
	"fmt"
	"strings"
	"time"
)

// FormatISOOffset is DateTimeFormatter.ISO_OFFSET_DATE_TIME in t's zone: seconds always,
// the fraction only when not zero (without trailing zeros), and the offset as "Z" or
// "+HH:MM" (with ":ss" when it has seconds).
func FormatISOOffset(t time.Time) string {
	s := t.Format("2006-01-02T15:04:05")
	if ns := t.Nanosecond(); ns != 0 {
		s += strings.TrimRight(fmt.Sprintf(".%09d", ns), "0")
	}
	_, off := t.Zone()
	if off == 0 {
		return s + "Z"
	}
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	s += fmt.Sprintf("%c%02d:%02d", sign, off/3600, off/60%60)
	if off%60 != 0 {
		s += fmt.Sprintf(":%02d", off%60)
	}
	return s
}
