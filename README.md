# romwbw tools

Two small command-line tools for the [RomWBW](https://github.com/wwarthen/RomWBW)
emulator `romwbw_emu`, built on top of `romwbw-get` and its catalog of ROM and
disk images:

- **`romwbw-run`** boots the emulator with a ROM and a list of disks named on
  the command line, such as `romwbw-run cpm22 games tpascal`.
- **`romwbw-disk`** builds a custom 8-slice disk image out of slices taken from
  the catalog's images, plus empty slices if you want them.

Run either tool with no arguments and it walks you through a short series of
menus instead. When the menus finish it prints the equivalent command line, so
you can do the same thing again without them.

## Requirements

| Needed for     | What                       | Notes                                        |
|----------------|----------------------------|----------------------------------------------|
| building       | Go 1.25 or newer           |                                              |
| both tools     | `romwbw-get` on `$PATH`    | or point `$ROMWBW_GET` at it                 |
| `romwbw-run`   | `romwbw_emu` on `$PATH`    | or pass `--emu PATH`                         |
| `romwbw-disk`  | `python3`                  | runs `cpm_disk.py`, which is fetched for you |

Both tools get the list of releases, ROMs and disks from `romwbw-get` each time
they run, so they pick up new catalog entries without being rebuilt. Downloads
need network access the first time; after that they come from
`romwbw-get`'s cache.

## Installing

```sh
go install github.com/vaelen/romwbw_tools/cmd/...@latest
```

That puts `romwbw-run` and `romwbw-disk` in `$(go env GOPATH)/bin`, usually
`~/go/bin`, which needs to be on your `$PATH`.

From a checkout of this repository, `go install ./cmd/...` does the same with
your local copy. To build into the checkout instead:

```sh
go build -o bin/ ./cmd/...
```

Run `go test ./...` to run the tests.

## romwbw-run

```sh
romwbw-run [options] [DISK ...] [-- EMULATOR-ARGS ...]
```

Each disk is attached in the order given: the first is disk 0 (boot unit 2),
the second disk 1, and so on, up to 16. Disk 0 is booted unless you say
otherwise.

A disk is either:

- **a catalog disk**, named with or without its `hd1k_` prefix: `cpm22`,
  `hd1k_cpm22`, `games`, `combo`, ... `romwbw-get list --disks` shows what a
  release has. Catalog disks are attached as writable working copies
  (`romwbw-get path --work`), so the guest can save files and the verified
  downloads in the cache are never changed.
- **your own image file**, used as-is. An argument is treated as a file when it
  contains a `/` or ends in `.img`, for example `./custom.img`.

With no disks named, the release's default disk (normally `hd1k_combo`) is
attached.

| Option             | Meaning                                                             |
|--------------------|---------------------------------------------------------------------|
| `-r`, `--rom`      | ROM to boot: `avw`, `rcz80`, or a full id like `emu_avw`            |
| `-V`, `--version`  | RomWBW release, such as `3.5.1` (`romwbw-get versions` lists them)  |
| `-b`, `--boot`     | what to boot (see below)                                            |
| `--no-disks`       | attach no disks at all                                              |
| `--emu`            | the `romwbw_emu` program to run                                     |
| `-n`, `--dry-run`  | print the `romwbw_emu` command instead of running it                |
| `-h`, `--help`     | full help                                                           |

Boot targets for `--boot`:

| Value    | Boots                                                                   |
|----------|-------------------------------------------------------------------------|
| `2`      | disk 0, slice 0 (the default when any disk is attached)                 |
| `2.N`    | disk 0, slice N; `3` / `3.N` for disk 1, `4` for disk 2, ...            |
| `C`, `Z` | CP/M 2.2 or Z-System from ROM                                           |
| `H`      | stops at the RomWBW boot menu                                           |
| `saved`  | whatever the emulator's saved setting says (default with no disks)      |
| `none`   | clears the saved setting and stops at the boot menu                     |

Everything after a bare `--` is passed to `romwbw_emu` unchanged.

Examples:

```sh
romwbw-run cpm22 games tpascal          # three disks, boot CP/M 2.2
romwbw-run --rom rcz80 -V 3.5.1 combo   # another ROM and release
romwbw-run combo --boot 2.3             # boot slice 3 of the combo disk
romwbw-run ./custom.img games           # your own image plus a catalog disk
romwbw-run wp -- --escape=none          # WordStar needs ^E for cursor-up
romwbw-run -n cpm22                     # show the emulator command line
```

Inside the emulator, Ctrl+E opens the `sim>` console, and `quit` there exits.
Working copies live in `~/.local/share/romwbw_emu/disks/<release>/` and keep
what the guest writes between runs. Delete one to start that disk fresh. The
same catalog disk can't be attached twice, because both slots would share one
working copy.

## romwbw-disk

```sh
romwbw-disk [options] SLICE [SLICE ...]
```

Builds a 65 MB combo image: an MBR followed by eight 8 MB slices. The slices
you name are copied into it byte for byte, in order, so the first becomes
slice 0, the next slice 1, and so on. Slots you don't fill are empty,
formatted CP/M slices. Slices keep their boot tracks, so a bootable slice
boots from any position.

A slice is:

| Form      | Meaning                                                            |
|-----------|--------------------------------------------------------------------|
| `NAME`    | slice 0 of a catalog disk: `cpm22`, `games`, `hd1k_tpascal`, ...   |
| `NAME:N`  | slice N of a multi-slice disk, such as `combo:3` (combo has 0-5)   |
| `empty`   | an empty formatted slice in that position                          |

| Option             | Meaning                                                         |
|--------------------|-----------------------------------------------------------------|
| `-o`, `--output`   | image to write (default `custom.img`)                           |
| `-V`, `--version`  | RomWBW release to take the slices from                          |
| `-f`, `--force`    | overwrite the output file if it exists                          |
| `--cpm-disk`       | use this `cpm_disk.py` instead of the downloaded one            |
| `-h`, `--help`     | full help                                                       |

Examples:

```sh
romwbw-disk -o mydisk.img cpm22 games tpascal
romwbw-disk -o work.img cpm22 empty empty games
romwbw-disk -V 3.5.1 -o old.img cpm22 combo:1
```

When it finishes it prints which slice came from where and the `--boot` value
that boots each one. To use the image:

```sh
romwbw-run ./mydisk.img              # boots slice 0
romwbw-run ./mydisk.img --boot 2.2   # boots slice 2
```

### cpm_disk.py

`romwbw-disk` uses `cpm_disk.py` from
[cpmemu](https://github.com/avwohl/cpmemu) to create the blank image and to
check the result. On first use it downloads the script from a fixed cpmemu
commit into `<romwbw-get cache-dir>/tools/`. It checks the file's SHA-256 when
downloading it and again every time it runs, and won't run a copy that doesn't
match. To use your own copy, pass `--cpm-disk PATH` or set `$CPM_DISK`; that
copy is run without the check. To move to a newer version, update
`cpmDiskCommit` and `cpmDiskSHA256` together in `cmd/romwbw-disk/main.go`.

`cpm_disk.py` only knows about six slices in a combo image. Its `--slice`
option works on slices 0-5 of an image built here, but not 6 and 7. The
emulator uses all eight.

## License

MIT. See [LICENSE](LICENSE).
