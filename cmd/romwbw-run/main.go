// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

// romwbw-run boots romwbw_emu with a ROM and an ordered list of disks, using
// romwbw-get to find (and fetch) the files and to make writable working copies.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/vaelen/romwbw_tools/internal/rw"
	"github.com/vaelen/romwbw_tools/internal/tui"
)

const maxDisks = 16 // romwbw_emu has --disk0 .. --disk15

const usageText = `romwbw-run - boot the RomWBW emulator with a chosen ROM and set of disks

USAGE
  romwbw-run                          interactive menus (needs a terminal)
  romwbw-run [options] [DISK ...] [-- EMULATOR-ARGS ...]

DESCRIPTION
  Each DISK is attached in the order given: the first becomes --disk0 (boot
  unit 2), the second --disk1 (unit 3), and so on, up to 16. Catalog disks are
  attached as writable working copies made by 'romwbw-get path --work', so the
  guest can save files and the verified downloads in the cache stay pristine.
  Anything missing is downloaded first. The list of ROMs and disks is read from
  'romwbw-get list' at runtime; nothing is hard-coded.

  A DISK is either:
    - a catalog disk id, with or without its "hd1k_" prefix:
        cpm22, hd1k_cpm22, games, combo, tpascal, ...
      Run 'romwbw-get list --disks' (or 'romwbw-get --romwbw VER list') to see
      what a release has.
    - a path to your own image file, used as-is (not copied). It is treated as
      a path when it contains a '/' or ends in .img, e.g. ./custom.img
      Images built by romwbw-disk are attached this way.

  With no DISK arguments (and no --no-disks) the release's default disk,
  the one 'romwbw-get' calls @disk0 (normally hd1k_combo), is attached.

OPTIONS
%s
BOOT TARGETS (--boot)
  2          boot disk 0, slice 0 (the default when any disk is attached)
  2.N        boot disk 0, slice N;  3 / 3.N for disk 1, 4 for disk 2, ...
  C          CP/M 2.2 from ROM          Z   Z-System from ROM
  H          stop at the boot menu
  saved      pass no --boot: use the target saved in the emulator's NVRAM
             (the default when no disk is attached)
  none       clear the saved NVRAM target and stop at the boot menu
  Any other value is passed through as --boot=VALUE. A --boot given here
  applies to this run only; romwbw_emu does not save it.

EMULATOR ARGUMENTS
  Everything after a bare '--' goes to romwbw_emu unchanged, e.g.
    romwbw-run wp -- --escape=none     WordStar needs ^E, which is the
                                       emulator's console key by default
  See 'romwbw_emu --help' for what it accepts.

EXAMPLES
  romwbw-run cpm22 games tpascal
      CP/M 2.2 as disk 0 (booted), Games as disk 1, Turbo Pascal as disk 2,
      with the default ROM of the default release.
  romwbw-run --rom rcz80 --version 3.5.1 combo
      The RCZ80 ROM from RomWBW 3.5.1 with the combo disk.
  romwbw-run combo --boot 2.3
      Boot slice 3 of the combo disk.
  romwbw-run ./custom.img games
      Your own image (e.g. from romwbw-disk) as disk 0, Games as disk 1.
  romwbw-run --boot C --no-disks
      CP/M 2.2 from ROM, no disks.
  romwbw-run -n cpm22 games
      Print the romwbw_emu command line instead of running it.

NOTES
  - Working copies live in ~/.local/share/romwbw_emu/disks/<release>/ and keep
    whatever the guest writes; running again reuses the same copy. Delete one to
    start that disk fresh. The same catalog disk cannot be attached twice,
    since both slots would share one working copy.
  - In the emulator, Ctrl+E opens the sim> console ('quit' exits).
  - Exit status is romwbw_emu's (it replaces this process): 0 normally, 124
    when its instruction limit stopped it. Errors here exit 1, usage errors 2.
`

func main() {
	if err := run(); err != nil {
		if errors.Is(err, tui.ErrAborted) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "romwbw-run:", err)
		os.Exit(1)
	}
}

type plan struct {
	version string
	rom     string // name as typed or id; "" = catalog default
	disks   []string
	noDisks bool
	boot    string
	emuArgs []string
}

func run() error {
	fs := pflag.NewFlagSet("romwbw-run", pflag.ContinueOnError)
	fs.SortFlags = false
	var p plan
	fs.StringVarP(&p.rom, "rom", "r", "", "ROM to boot, by catalog id with or without its \"emu_\" prefix\n(avw, rcz80, emu_avw, ...). Default: the release's default ROM")
	fs.StringVarP(&p.version, "version", "V", "", "RomWBW release to use (3.6.0, 3.5.1, ...; 'romwbw-get versions'\nlists them). Default: the one romwbw-get has in use")
	fs.StringVarP(&p.boot, "boot", "b", "", "boot target, see BOOT TARGETS below. Default: 2 with disks, saved without")
	fs.BoolVar(&p.noDisks, "no-disks", false, "attach no disks at all (instead of the default disk)")
	emu := fs.String("emu", "romwbw_emu", "the romwbw_emu executable to run (name on $PATH, or a path)")
	dry := fs.BoolP("dry-run", "n", false, "print the romwbw_emu command line instead of running it")
	help := fs.BoolP("help", "h", false, "show this help")
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "run 'romwbw-run --help' for usage\n") }

	interactive := len(os.Args) == 1
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "romwbw-run: %v\n", err)
		fs.Usage()
		os.Exit(2)
	}
	if *help {
		fmt.Printf(usageText, fs.FlagUsages())
		return nil
	}
	args := fs.Args()
	if d := fs.ArgsLenAtDash(); d >= 0 {
		args, p.emuArgs = args[:d], args[d:]
	}
	p.disks = args
	if p.noDisks && len(p.disks) > 0 {
		fmt.Fprintln(os.Stderr, "romwbw-run: --no-disks and DISK arguments conflict")
		os.Exit(2)
	}

	if interactive {
		if !tui.IsTerminal() {
			return errors.New("no terminal for the menus; pass options instead (see --help)")
		}
		if err := ask(&p); err != nil {
			return err
		}
	}

	argv, err := build(p, *emu)
	if err != nil {
		return err
	}
	if *dry {
		fmt.Println(rw.ShellQuote(argv))
		return nil
	}
	if interactive {
		fmt.Fprintln(os.Stderr, "Next time, the same thing without menus:\n  "+rw.ShellQuote(equivalent(p)))
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(bin, argv, os.Environ())
}

// build resolves the plan against the catalog and returns romwbw_emu's argv.
func build(p plan, emu string) ([]string, error) {
	cat, err := rw.List(p.version)
	if err != nil {
		return nil, err
	}
	rom := cat.DefaultROM()
	if p.rom != "" {
		if rom, err = rw.Resolve("ROM", p.rom, rw.ROMPrefix, cat.ROMIDs()); err != nil {
			return nil, err
		}
	}
	disks := p.disks
	if len(disks) == 0 && !p.noDisks {
		if d := cat.DefaultDisk(); d != "" {
			disks = []string{d}
		}
	}
	if len(disks) > maxDisks {
		return nil, fmt.Errorf("%d disks given; romwbw_emu takes at most %d", len(disks), maxDisks)
	}

	// Resolve every disk first so a typo fails before anything is downloaded.
	paths := make([]string, len(disks))
	var ids []string
	var slots []int
	seen := map[string]bool{}
	for i, d := range disks {
		if rw.IsFile(d) {
			abs, err := filepath.Abs(d)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(abs); err != nil {
				return nil, err
			}
			paths[i] = abs
			continue
		}
		id, err := rw.Resolve("disk", d, rw.DiskPrefix, cat.DiskIDs())
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("%s is listed twice; both slots would share one working copy", id)
		}
		seen[id] = true
		ids = append(ids, id)
		slots = append(slots, i)
	}

	romPath, err := rw.Paths(p.version, false, rom)
	if err != nil {
		return nil, err
	}
	work, err := rw.Paths(p.version, true, ids...)
	if err != nil {
		return nil, err
	}
	for j, slot := range slots {
		paths[slot] = work[j]
	}

	argv := []string{emu, "--romwbw=" + romPath[0]}
	for i, path := range paths {
		argv = append(argv, fmt.Sprintf("--disk%d=%s", i, path))
	}
	boot := p.boot
	if boot == "" && len(paths) > 0 {
		boot = "2"
	}
	if boot != "" && boot != "saved" {
		argv = append(argv, "--boot="+boot)
	}
	return append(argv, p.emuArgs...), nil
}

// ask fills the plan from menus.
func ask(p *plan) error {
	var err error
	if p.version, err = tui.PickVersion(); err != nil {
		return err
	}
	cat, err := rw.List(p.version)
	if err != nil {
		return err
	}

	roms := make([]tui.Item, len(cat.ROMs))
	cur := 0
	for i, r := range cat.ROMs {
		roms[i] = tui.Item{Label: rw.Short(r.ID, rw.ROMPrefix), Detail: r.Name}
		if r.Default {
			roms[i].Detail += " (default)"
			cur = i
		}
	}
	i, err := tui.Select("ROM", roms, cur)
	if err != nil {
		return err
	}
	p.rom = rw.Short(cat.ROMs[i].ID, rw.ROMPrefix)

	disks := make([]tui.Item, len(cat.Disks))
	var pre []int
	for i, d := range cat.Disks {
		disks[i] = tui.Item{Label: rw.Short(d.ID, rw.DiskPrefix), Detail: d.Name}
		if d.SliceCount() > 1 {
			disks[i].Detail += fmt.Sprintf(" (%d slices)", d.SliceCount())
		}
		if d.ID == cat.DefaultDisk() {
			pre = []int{i}
		}
	}
	picked, err := tui.MultiSelect("Disks, in slot order (first = disk 0)", disks, pre, maxDisks)
	if err != nil {
		return err
	}
	for _, i := range picked {
		p.disks = append(p.disks, disks[i].Label)
	}
	p.noDisks = len(p.disks) == 0

	type target struct{ value, label, detail string }
	var targets []target
	if !p.noDisks {
		first := cat.Disk(rw.DiskPrefix + p.disks[0])
		targets = append(targets, target{"2", "Disk 0", p.disks[0] + ", slice 0"})
		for s := 1; s < first.SliceCount(); s++ {
			targets = append(targets, target{fmt.Sprintf("2.%d", s), fmt.Sprintf("Disk 0 slice %d", s), p.disks[0]})
		}
	}
	targets = append(targets,
		target{"H", "Boot menu", "stop at the RomWBW boot loader prompt"},
		target{"C", "CP/M 2.2 from ROM", ""},
		target{"Z", "Z-System from ROM", ""},
		target{"saved", "Saved setting", "whatever the emulator's NVRAM says"})
	items := make([]tui.Item, len(targets))
	for i, t := range targets {
		items[i] = tui.Item{Label: t.label, Detail: t.detail}
	}
	i, err = tui.Select("Boot", items, 0)
	if err != nil {
		return err
	}
	p.boot = targets[i].value
	return nil
}

// equivalent is the non-interactive romwbw-run command for a plan.
func equivalent(p plan) []string {
	argv := []string{"romwbw-run", "--version", p.version, "--rom", p.rom}
	if !(p.boot == "2" && len(p.disks) > 0) && !(p.boot == "saved" && len(p.disks) == 0) {
		argv = append(argv, "--boot", p.boot)
	}
	if p.noDisks {
		argv = append(argv, "--no-disks")
	}
	return append(argv, p.disks...)
}
