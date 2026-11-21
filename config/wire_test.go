package config

import (
	"strings"
	"testing"
	"time"
)

func TestWireSeconds(t *testing.T) {
	for _, tc := range []struct {
		in      time.Duration
		want    uint16
		errPart string // empty = must succeed
	}{
		{0, 0, ""},
		{time.Second, 1, ""},
		{900 * time.Second, 900, ""},
		{MaxWireDuration, 65535, ""},
		{18*time.Hour + 12*time.Minute + 15*time.Second, 65535, ""},
		{MaxWireDuration + time.Second, 0, "65535s"},
		{65536 * time.Second, 0, "65535s"},
		{24 * time.Hour, 0, "65535s"},
		{-time.Second, 0, "negative"},
		{500 * time.Millisecond, 0, "whole number of seconds"},
		{1500 * time.Millisecond, 0, "whole number of seconds"},
		{MaxWireDuration + time.Nanosecond, 0, "65535s"},
	} {
		got, err := WireSeconds("ttl", tc.in)
		if tc.errPart == "" {
			if err != nil || got != tc.want {
				t.Errorf("WireSeconds(%v) = %d, %v; want %d, nil", tc.in, got, err, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("WireSeconds(%v) = %d, nil; want an error containing %q", tc.in, got, tc.errPart)
			continue
		}
		if !strings.Contains(err.Error(), tc.errPart) || !strings.HasPrefix(err.Error(), "ttl ") {
			t.Errorf("WireSeconds(%v) error %q: want it to name the field and contain %q", tc.in, err, tc.errPart)
		}
	}
}
