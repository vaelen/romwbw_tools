// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
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

// openImage opens an image file or device, saying how to get access to a
// card when that is what's missing.
func openImage(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag, 0)
	if errors.Is(err, os.ErrPermission) {
		return nil, fmt.Errorf("%w; for an SD card, 'sudo chown $USER %s' gives you access until it is removed", err, path)
	}
	return f, err
}

// layout finds the slices in an image file or device: the offset of slice 0
// and how many whole slices there are. It knows
//   - a plain hd1k image: exactly 8 MB, one slice
//   - anything whose sector 0 is an MBR with a RomWBW (0x2E) partition: a
//     combo image, or a whole SD card such as /dev/sda; the slices fill that
//     partition, as far as the file or device actually goes
//   - otherwise a run of slices from offset 0, such as the RomWBW partition
//     of a card on its own (/dev/sda1). Plain hd1k slices carry an MBR of
//     their own in sector 0, but without a 0x2E entry, so they land here.
func layout(f *os.File) (base int64, count int, err error) {
	// Stat reports 0 for a block device; seeking to the end gives its size on
	// Linux. ponytail: Linux only. macOS and BSD report 0 here too and need
	// an ioctl (DKIOCGETBLOCKCOUNT, DIOCGMEDIASIZE) to support cards there.
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, 0, err
	}
	if size == sliceSize {
		return 0, 1, nil
	}
	mbr := make([]byte, 512)
	if _, err := f.ReadAt(mbr, 0); err != nil {
		return 0, 0, fmt.Errorf("%s: reading sector 0: %w", f.Name(), err)
	}
	if mbr[0x1FE] == 0x55 && mbr[0x1FF] == 0xAA {
		for e := mbr[0x1BE:0x1FE]; len(e) > 0; e = e[16:] {
			if e[4] != 0x2E {
				continue
			}
			start := int64(binary.LittleEndian.Uint32(e[8:])) * 512
			length := int64(binary.LittleEndian.Uint32(e[12:])) * 512
			if start <= 0 || start >= size {
				return 0, 0, fmt.Errorf("%s: its RomWBW partition starts at byte %d, outside the %d bytes there are", f.Name(), start, size)
			}
			return start, int(min(length, size-start) / sliceSize), nil
		}
	}
	if size > 0 && size%sliceSize == 0 {
		return 0, int(size / sliceSize), nil
	}
	return 0, 0, fmt.Errorf("%s: %d bytes with no RomWBW partition is not a whole number of 8 MB slices", f.Name(), size)
}

// sliceReader returns slice n of the image or device f (see layout).
func sliceReader(f *os.File, n int) (io.Reader, error) {
	base, k, err := layout(f)
	if err != nil {
		return nil, err
	}
	if n < 0 || n >= k {
		return nil, fmt.Errorf("%s has slices 0-%d, not %d", f.Name(), k-1, n)
	}
	return io.NewSectionReader(f, base+int64(n)*sliceSize, sliceSize), nil
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
	f, err := openImage(src.path, os.O_RDONLY)
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

// writeInto overwrites slices at, at+1, ... of the existing image file or
// device dest with srcs, and leaves the rest of it alone. A source with no
// path is slice 0 of blank, a formatted empty slice.
//
// ponytail: each source is read just before its slot is written, so taking a
// source from a slot of dest that this same call already overwrote copies the
// new data; buffer every source first if that ever matters.
func writeInto(dest, blank string, at int, srcs []source) error {
	srcs = append([]source(nil), srcs...)
	for i := range srcs {
		if srcs[i].path == "" {
			srcs[i] = source{path: blank}
		}
	}
	// Check every source before the first write, so a bad one changes nothing.
	for _, s := range srcs {
		if err := copySlice(io.Discard, s); err != nil {
			return err
		}
	}

	fi, err := os.Stat(dest)
	if err != nil {
		return err
	}
	flag := os.O_RDWR
	if fi.Mode()&os.ModeDevice != 0 {
		// On Linux, O_EXCL without O_CREAT on a block device fails with EBUSY
		// while anything has it (or, for a whole disk, a partition) mounted.
		// Elsewhere it is ignored; macOS gives EBUSY for a mounted disk anyway.
		flag |= syscall.O_EXCL
	}
	f, err := openImage(dest, flag)
	if errors.Is(err, syscall.EBUSY) {
		return fmt.Errorf("%s is in use, probably mounted; unmount it, or name just its RomWBW partition (such as /dev/sda1)", dest)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	base, count, err := layout(f)
	if err != nil {
		return err
	}
	if at < 0 || at+len(srcs) > count {
		return fmt.Errorf("%s has slices 0-%d; can't write slices %d-%d", dest, count-1, at, at+len(srcs)-1)
	}

	buf := make([]byte, sliceSize)
	for i, s := range srcs {
		if err := readSlice(buf, s); err != nil {
			return err
		}
		if _, err := f.WriteAt(buf, base+int64(at+i)*sliceSize); err != nil {
			return fmt.Errorf("writing slice %d of %s: %w", at+i, dest, err)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// readSlice fills buf (one slice long) with src.
func readSlice(buf []byte, src source) error {
	f, err := openImage(src.path, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := sliceReader(f, src.slice)
	if err != nil {
		return err
	}
	_, err = io.ReadFull(r, buf)
	return err
}
