// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// fill writes an image whose every 8 MB slice is filled with its own marker
// byte, so the test can tell which slice landed where.
func fill(t *testing.T, path string, prefix bool, marks ...byte) {
	var b []byte
	if prefix {
		p := make([]byte, prefixSize)
		p[0x1FE], p[0x1FF], p[0x1BE+4] = 0x55, 0xAA, 0x2E
		b = append(b, p...)
	}
	for _, m := range marks {
		b = append(b, bytes.Repeat([]byte{m}, sliceSize)...)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssemble(t *testing.T) {
	d := t.TempDir()
	blank, single, combo, out := filepath.Join(d, "blank"), filepath.Join(d, "single"), filepath.Join(d, "combo"), filepath.Join(d, "out.img")
	fill(t, blank, true, 0xE5, 0xE5)
	fill(t, single, false, 'S')
	fill(t, combo, true, 'a', 'b', 'c')

	if err := assemble(out, blank, []source{{combo, 2}, {}, {single, 0}, {combo, 0}}, 8); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if len(got) != prefixSize+8*sliceSize {
		t.Fatalf("size %d", len(got))
	}
	if n := binary.LittleEndian.Uint32(got[0x1BE+12:]); n != 8*sliceSize/512 {
		t.Errorf("partition size %d sectors", n)
	}
	want := []byte{'c', 0xE5, 'S', 'a', 0xE5, 0xE5, 0xE5, 0xE5}
	for i, w := range want {
		off := prefixSize + i*sliceSize
		if got[off] != w || got[off+sliceSize-1] != w {
			t.Errorf("slot %d: got %q, want %q", i, got[off], w)
		}
	}

	if err := assemble(out, blank, []source{{single, 0}, {single, 0}}, 1); err == nil {
		t.Error("two slices into a 1-slice image should fail")
	}
	if err := assemble(out, blank, nil, maxSlices+1); err == nil {
		t.Error("more than maxSlices should fail")
	}
	if err := assemble(out, blank, []source{{combo, 3}}, 8); err == nil {
		t.Error("slice 3 of a 3-slice combo should fail")
	}
	if err := assemble(out, blank, []source{{single, 1}}, 8); err == nil {
		t.Error("slice 1 of a single hd1k should fail")
	}
}
