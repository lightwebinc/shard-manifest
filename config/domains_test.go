package config

import "testing"

func TestParseDomainSpec(t *testing.T) {
	cases := []struct {
		spec string
		ok   bool
		id   uint8
		bits uint8
		span uint8
		ssm  bool
		act  bool
	}{
		{"0x1:bits=12:ssm:active", true, 1, 12, 1, true, true},
		{"1:bits=4", true, 1, 4, 1, false, false},
		{"2:bits=13:slotspan=4", true, 2, 13, 4, false, false},
		{"1:bits=15", true, 1, 15, 8, false, false}, // implied span 8
		{"1:bits=12:generation=000102030405060708090a0b0c0d0e0f", true, 1, 12, 1, false, false},
		{"0x0F:bits=4", false, 0, 0, 0, false, false}, // forbidden domain
		{"1", false, 0, 0, 0, false, false},           // bits required
		{"1:bits=0", false, 0, 0, 0, false, false},
		{"1:bits=16", false, 0, 0, 0, false, false},
		{"1:bits=4:bogus", false, 0, 0, 0, false, false},
		{"1:bits=12:generation=abcd", false, 0, 0, 0, false, false},
	}
	for _, c := range cases {
		d, err := parseDomainSpec(c.spec)
		if c.ok && err != nil {
			t.Errorf("%q: unexpected error %v", c.spec, err)
			continue
		}
		if !c.ok {
			if err == nil {
				t.Errorf("%q: expected error", c.spec)
			}
			continue
		}
		if d.ID != c.id || d.ShardBits != c.bits || d.SlotSpan != c.span || d.SSM != c.ssm || d.Active != c.act {
			t.Errorf("%q: parsed %+v", c.spec, d)
		}
	}
}

func TestParseDomainSpecSuccessor(t *testing.T) {
	d, err := parseDomainSpec("1:bits=12:active:succbits=13:succepoch=1700000000:succssm:succgen=000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Successor == nil {
		t.Fatal("successor not parsed")
	}
	if d.Successor.ShardBits != 13 || d.Successor.TransitionEpoch != 1700000000 || !d.Successor.SSM {
		t.Fatalf("successor fields: %+v", d.Successor)
	}
	if d.Successor.GenerationID[15] != 0x0f {
		t.Fatalf("successor generation not parsed: %x", d.Successor.GenerationID)
	}

	// succbits without succepoch is rejected.
	if _, err := parseDomainSpec("1:bits=12:succbits=13"); err == nil {
		t.Error("missing succepoch accepted")
	}
	// succepoch without succbits is rejected.
	if _, err := parseDomainSpec("1:bits=12:succepoch=1700000000"); err == nil {
		t.Error("missing succbits accepted")
	}
}
