// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

// romwbw-disk builds a custom 8-slice RomWBW disk image out of slices taken
// from the catalog's disk images.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/vaelen/romwbw_tools/internal/rw"
	"github.com/vaelen/romwbw_tools/internal/tui"
)

// cpm_disk.py is owned by cpmemu. It is fetched once into the cache from a
// pinned commit and must match cpmDiskSHA256, on download and on every use.
// To move to a newer version, update both constants together.
const (
	cpmDiskCommit = "e935dd0ab9f3c057faa9ab4236b393a6e700354d"
	cpmDiskSHA256 = "4d333765faee59f79be9ad45348adda3b3ee29aec7e6e256993c10c578d0f81f"
	cpmDiskURL    = "https://raw.githubusercontent.com/avwohl/cpmemu/" + cpmDiskCommit + "/util/cpm_disk.py"
)

// cpm_disk.py's combo format knows six slices; slots 6 and 7 are beyond it.
const scriptSlices = 6

const usageText = `romwbw-disk - build a custom 8-slice RomWBW disk image from catalog slices

USAGE
  romwbw-disk                          interactive menus (needs a terminal)
  romwbw-disk [options] SLICE [SLICE ...]

DESCRIPTION
  Builds one combo disk image (a 1 MB MBR prefix plus eight 8 MB hd1k slices,
  65 MB in all) whose slices are copied, byte for byte, from disk images in the
  romwbw-get catalog. Slices go into the new image in the order given: the
  first SLICE becomes slice 0, the next slice 1, and so on. Up to 8 may be
  given; any slots left over are blank, formatted, empty CP/M slices. A slice
  keeps its boot tracks, so a bootable slice stays bootable in any position.

  A SLICE is NAME, NAME:N, or the word empty
    NAME  a catalog disk id, with or without its "hd1k_" prefix (cpm22,
          games, hd1k_tpascal, combo, ...). 'romwbw-get list --disks' lists
          them; the catalog is read at runtime.
    N     which slice of that image to take. Default 0. Only multi-slice
          images (hd1k_combo has 6) have slices other than 0.
    empty a blank, formatted CP/M slice in that position, e.g. room for your
          own files between two catalog slices. Reserved: it never names a
          catalog disk. Trailing empty slices need not be given; unused
          slots are empty anyway.

  The blank image is made with cpm_disk.py (from github.com/avwohl/cpmemu),
  which is then used to check the result. It needs python3. It is looked for
  in this order:
    1. --cpm-disk PATH, then $CPM_DISK - used as given, not checked
    2. <romwbw-get cache-dir>/tools/cpm_disk-<commit>.py, downloaded there
       on first use from a pinned cpmemu commit
  The cached copy must match a SHA-256 built into this program, both when it
  is downloaded and each time it is used; on a mismatch nothing is run.

OPTIONS
%s
BOOTING THE RESULT
  Attach it with romwbw-run (or romwbw_emu --disk0=...) and choose a slice
  at boot: --boot=2 boots slice 0, --boot=2.N slice N.
    romwbw-run ./custom.img --boot 2.1

EXAMPLES
  romwbw-disk -o mydisk.img cpm22 games tpascal
      CP/M 2.2, Games and Turbo Pascal as slices 0-2, five empty slices.
  romwbw-disk -o work.img cpm22 empty empty games
      CP/M 2.2 in slice 0, empty slices 1 and 2, Games in slice 3.
  romwbw-disk -o z.img combo:4 combo:0 wp
      Slice 4 of the combo disk, then its slice 0, then Word Processing.
  romwbw-disk --version 3.5.1 -o old.img cpm22 games
      Take the slices from the RomWBW 3.5.1 images.

NOTES
  - The result is a plain file you own; the guest writes to it directly.
  - cpm_disk.py's own combo support stops at six slices, so 'cpm_disk.py
    --slice N' works on slices 0-5 of the result but not 6 and 7. The
    emulator uses all eight.
  - Exit status: 0 on success, 1 on error, 2 on a usage error, 130 when the
    menus are cancelled.
`

func main() {
	if err := run(); err != nil {
		if errors.Is(err, tui.ErrAborted) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "romwbw-disk:", err)
		os.Exit(1)
	}
}

// emptyName is the reserved SLICE word for a blank, formatted slice.
const emptyName = "empty"

// spec is one requested slice: slice n of catalog disk id, or an empty
// slice when id is "".
type spec struct {
	id string
	n  int
}

func (s spec) String() string {
	if s.id == "" {
		return emptyName
	}
	short := rw.Short(s.id, rw.DiskPrefix)
	if s.n == 0 {
		return short
	}
	return fmt.Sprintf("%s:%d", short, s.n)
}

type options struct {
	version, output, cpmDisk string
	force                    bool
	specs                    []spec
}

func run() error {
	fs := pflag.NewFlagSet("romwbw-disk", pflag.ContinueOnError)
	fs.SortFlags = false
	var o options
	fs.StringVarP(&o.output, "output", "o", "custom.img", "image file to write")
	fs.StringVarP(&o.version, "version", "V", "", "RomWBW release whose images to take slices from (3.6.0,\n3.5.1, ...). Default: the one romwbw-get has in use")
	fs.BoolVarP(&o.force, "force", "f", false, "overwrite the output file if it exists")
	fs.StringVar(&o.cpmDisk, "cpm-disk", "", "path to cpm_disk.py (default: $CPM_DISK, else cached/downloaded)")
	help := fs.BoolP("help", "h", false, "show this help")
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "run 'romwbw-disk --help' for usage\n") }

	interactive := len(os.Args) == 1
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "romwbw-disk: %v\n", err)
		fs.Usage()
		os.Exit(2)
	}
	if *help {
		fmt.Printf(usageText, fs.FlagUsages())
		return nil
	}

	var cat *rw.Catalog
	var err error
	if interactive {
		if !tui.IsTerminal() {
			return errors.New("no terminal for the menus; pass SLICE arguments instead (see --help)")
		}
		if cat, err = ask(&o); err != nil {
			return err
		}
	} else {
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "romwbw-disk: no SLICE given; run 'romwbw-disk --help' for usage")
			os.Exit(2)
		}
		if fs.NArg() > outSlices {
			fmt.Fprintf(os.Stderr, "romwbw-disk: %d slices given; the image holds %d\n", fs.NArg(), outSlices)
			os.Exit(2)
		}
		if cat, err = rw.List(o.version); err != nil {
			return err
		}
		for _, a := range fs.Args() {
			s, err := parseSpec(a, cat)
			if err != nil {
				return err
			}
			o.specs = append(o.specs, s)
		}
		if _, err := os.Stat(o.output); err == nil && !o.force {
			return fmt.Errorf("%s exists; use --force to overwrite it", o.output)
		}
	}

	if err := buildImage(o); err != nil {
		return err
	}
	report(o, cat, interactive)
	return nil
}

func parseSpec(a string, cat *rw.Catalog) (spec, error) {
	name, num, hasNum := strings.Cut(a, ":")
	if strings.EqualFold(name, emptyName) {
		if hasNum {
			return spec{}, fmt.Errorf("%q: an empty slice has no slice number", a)
		}
		return spec{}, nil
	}
	id, err := rw.Resolve("disk", name, rw.DiskPrefix, cat.DiskIDs())
	if err != nil {
		return spec{}, err
	}
	s := spec{id: id}
	if hasNum {
		if s.n, err = strconv.Atoi(num); err != nil || s.n < 0 {
			return spec{}, fmt.Errorf("%q: slice must be a number >= 0", a)
		}
	}
	if k := cat.Disk(id).SliceCount(); s.n >= k {
		if k == 1 {
			return spec{}, fmt.Errorf("%q: %s has only slice 0", a, id)
		}
		return spec{}, fmt.Errorf("%q: %s has slices 0-%d", a, id, k-1)
	}
	return s, nil
}

func buildImage(o options) error {
	script, err := findCPMDisk(o.cpmDisk)
	if err != nil {
		return err
	}

	var ids []string
	for _, s := range o.specs {
		if s.id != "" {
			ids = append(ids, s.id)
		}
	}
	paths, err := rw.Paths(o.version, false, ids...) // read-only originals are fine to copy from
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "romwbw-disk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	blank := filepath.Join(tmp, "blank.img")
	if out, err := exec.Command("python3", "-I", script, "create", "--combo", blank).CombinedOutput(); err != nil {
		return fmt.Errorf("cpm_disk.py create --combo: %v\n%s", err, out)
	}

	srcs := make([]source, len(o.specs))
	for i, s := range o.specs {
		if s.id != "" { // empty specs keep a zero source: a blank slice
			srcs[i] = source{path: paths[0], slice: s.n}
			paths = paths[1:]
		}
	}
	if err := assemble(o.output, blank, srcs); err != nil {
		return err
	}

	for n := 0; n < scriptSlices; n++ {
		out, err := exec.Command("python3", "-I", script, "verify", "--slice", strconv.Itoa(n), o.output).CombinedOutput()
		if err != nil {
			return fmt.Errorf("wrote %s, but cpm_disk.py verify failed on slice %d: %v\n%s", o.output, n, err, out)
		}
	}
	return nil
}

// findCPMDisk locates cpm_disk.py, downloading it into the cache if needed.
func findCPMDisk(flag string) (string, error) {
	if _, err := exec.LookPath("python3"); err != nil {
		return "", errors.New("python3 is needed to run cpm_disk.py and is not on $PATH")
	}
	for _, p := range []string{flag, os.Getenv("CPM_DISK")} {
		if p != "" {
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("cpm_disk.py: %w", err)
			}
			return p, nil
		}
	}
	cache, err := rw.CacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, "tools", "cpm_disk-"+cpmDiskCommit[:12]+".py")
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		if err := checkSHA256(f); err != nil {
			return "", fmt.Errorf("%s: %w; delete it to download it again", path, err)
		}
		return path, nil
	}
	fmt.Fprintf(os.Stderr, "fetching %s\n", cpmDiskURL)
	if err := download(cpmDiskURL, path); err != nil {
		return "", fmt.Errorf("downloading cpm_disk.py: %w", err)
	}
	return path, nil
}

// checkSHA256 fails unless r's content is the pinned cpm_disk.py.
func checkSHA256(r io.Reader) error {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != cpmDiskSHA256 {
		return fmt.Errorf("sha256 %s, expected %s", got, cpmDiskSHA256)
	}
	return nil
}

// download saves url to path, but only if its content matches the pin.
func download(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cpm_disk-*.py")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// Re-read what landed on disk rather than hashing the stream, so the
	// check covers exactly the bytes that will be run.
	saved, err := os.Open(f.Name())
	if err != nil {
		return err
	}
	err = checkSHA256(saved)
	saved.Close()
	if err != nil {
		return fmt.Errorf("%s: %w; not saved", url, err)
	}
	return os.Rename(f.Name(), path)
}

func report(o options, cat *rw.Catalog, interactive bool) {
	fmt.Printf("Wrote %s (8 slices, %d MB)\n\n", o.output, (prefixSize+outSlices*sliceSize)>>20)
	fmt.Println("  Slice  From                    Boot with")
	fmt.Println("  -----  ----------------------  ---------")
	for slot := 0; slot < outSlices; slot++ {
		from := "(empty)"
		if slot < len(o.specs) && o.specs[slot].id != "" {
			s := o.specs[slot]
			from = fmt.Sprintf("%s slice %d", s.id, s.n)
		}
		fmt.Printf("  %-5d  %-22s  --boot=2.%d\n", slot, from, slot)
	}
	path := o.output
	if !strings.ContainsRune(path, '/') {
		path = "./" + path
	}
	fmt.Printf("\nBoot it:  romwbw-run %s\n", rw.ShellQuote([]string{path}))
	if interactive {
		argv := []string{"romwbw-disk", "--version", cat.Version, "-o", o.output}
		if o.force {
			argv = append(argv, "--force")
		}
		for _, s := range o.specs {
			argv = append(argv, s.String())
		}
		fmt.Println("Rebuild without menus:  " + rw.ShellQuote(argv))
	}
}

// ask fills the options from menus.
func ask(o *options) (*rw.Catalog, error) {
	var err error
	if o.version, err = tui.PickVersion(); err != nil {
		return nil, err
	}
	cat, err := rw.List(o.version)
	if err != nil {
		return nil, err
	}

	items := make([]tui.Item, len(cat.Disks))
	for i, d := range cat.Disks {
		items[i] = tui.Item{Label: rw.Short(d.ID, rw.DiskPrefix), Detail: d.Name}
		if d.SliceCount() > 1 {
			items[i].Detail += fmt.Sprintf(" (%d slices)", d.SliceCount())
		}
	}
	var picked []int
	for len(picked) == 0 {
		if picked, err = tui.MultiSelect("Source images (up to 8)", items, nil, outSlices); err != nil {
			return nil, err
		}
	}

	// Slices: single-slice images give slice 0; for multi-slice ones, ask,
	// keeping room for at least one slice from each image still to come.
	var specs []spec
	for i, p := range picked {
		d := cat.Disks[p]
		if d.SliceCount() == 1 {
			specs = append(specs, spec{id: d.ID})
			continue
		}
		budget := outSlices - len(specs) - (len(picked) - i - 1)
		sl := make([]tui.Item, d.SliceCount())
		for n := range sl {
			sl[n] = tui.Item{Label: fmt.Sprintf("slice %d", n)}
		}
		var ns []int
		for len(ns) == 0 {
			title := fmt.Sprintf("Slices of %s (up to %d)", rw.Short(d.ID, rw.DiskPrefix), budget)
			if ns, err = tui.MultiSelect(title, sl, []int{0}, budget); err != nil {
				return nil, err
			}
		}
		for _, n := range ns {
			specs = append(specs, spec{id: d.ID, n: n})
		}
	}

	if room := outSlices - len(specs); room > 0 {
		counts := make([]tui.Item, room+1)
		for n := range counts {
			counts[n] = tui.Item{Label: strconv.Itoa(n)}
		}
		counts[0].Detail = "unused slots at the end are empty anyway"
		n, err := tui.Select("Empty slices to place among them", counts, 0)
		if err != nil {
			return nil, err
		}
		for ; n > 0; n-- {
			specs = append(specs, spec{})
		}
	}

	if len(specs) > 1 {
		labels := make([]string, len(specs))
		for i, s := range specs {
			labels[i] = s.String()
		}
		order, err := tui.Reorder("Slice order (top = slice 0)", labels)
		if err != nil {
			return nil, err
		}
		for _, j := range order {
			o.specs = append(o.specs, specs[j])
		}
	} else {
		o.specs = specs
	}

	for {
		if o.output, err = tui.Input("Output image", o.output); err != nil {
			return nil, err
		}
		if _, err := os.Stat(o.output); err != nil {
			break
		}
		ok, err := tui.Confirm(o.output+" exists. Overwrite it?", false)
		if err != nil {
			return nil, err
		}
		if ok {
			o.force = true
			break
		}
	}
	return cat, nil
}
