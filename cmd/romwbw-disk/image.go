// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	sliceSize  = 8 << 20 // one hd1k slice
	prefixSize = 1 << 20 // MBR prefix of a combo image
	// The stock combo MBR reserves 64 slices (512 MB) for the RomWBW
	// partition, with a FAT partition right after it; more would overlap it.
	maxSlices = 64
)

// source is one slice to copy: slice n of the image at path.
// An empty path means a blank, freshly formatted slice.
type source struct {
	path  string
	slice int
}

// sliceReader returns slice n of an hd1k image (8 MB, slice 0 only) or of a
// combo image (1 MB prefix + k x 8 MB).
func sliceReader(f *os.File, n int) (io.Reader, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	switch {
	case size == sliceSize && n == 0:
		return io.NewSectionReader(f, 0, sliceSize), nil
	case size > prefixSize && (size-prefixSize)%sliceSize == 0:
		k := int((size - prefixSize) / sliceSize)
		if n < 0 || n >= k {
			return nil, fmt.Errorf("%s has slices 0-%d, not %d", f.Name(), k-1, n)
		}
		return io.NewSectionReader(f, prefixSize+int64(n)*sliceSize, sliceSize), nil
	default:
		return nil, fmt.Errorf("%s: %d bytes is neither an 8 MB hd1k image nor a combo; can't take slice %d", f.Name(), size, n)
	}
}

// assemble writes a combo image of total slices to out. blank is a combo
// image made by `cpm_disk.py create --combo`: its MBR prefix is reused (with
// the RomWBW partition sized to total slices) and its slice 0 fills any
// unused slot.
func assemble(out, blank string, srcs []source, total int) error {
	if total < 1 || total > maxSlices {
		return fmt.Errorf("%d slices: must be 1-%d", total, maxSlices)
	}
	if len(srcs) > total {
		return fmt.Errorf("%d slices given; the image holds %d", len(srcs), total)
	}
	bf, err := os.Open(blank)
	if err != nil {
		return err
	}
	defer bf.Close()
	prefix := make([]byte, prefixSize)
	if _, err := io.ReadFull(bf, prefix); err != nil {
		return fmt.Errorf("reading MBR from %s: %w", blank, err)
	}
	if prefix[0x1FE] != 0x55 || prefix[0x1FF] != 0xAA || prefix[0x1BE+4] != 0x2E {
		return fmt.Errorf("%s has no RomWBW (0x2E) partition in its MBR", blank)
	}
	binary.LittleEndian.PutUint32(prefix[0x1BE+12:], uint32(total*sliceSize/512))

	// Write to a temp file beside the output and rename, so a failure never
	// leaves a half-written image under the requested name.
	tmp, err := os.CreateTemp(filepath.Dir(out), ".romwbw-disk-*.img")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(prefix); err != nil {
		return err
	}
	for slot := 0; slot < total; slot++ {
		src := source{path: blank}
		if slot < len(srcs) && srcs[slot].path != "" {
			src = srcs[slot]
		}
		if err := copySlice(tmp, src); err != nil {
			return err
		}
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), out)
}

func copySlice(w io.Writer, src source) error {
	f, err := os.Open(src.path)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := sliceReader(f, src.slice)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, r)
	if err == nil && n != sliceSize {
		err = fmt.Errorf("%s slice %d: short read (%d bytes)", src.path, src.slice, n)
	}
	return err
}

// extractSlice writes slice n of image to path as a plain 8 MB hd1k image.
func extractSlice(path, image string, n int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := copySlice(f, source{path: image, slice: n}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
