// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

// Package rw wraps the romwbw-get command line tool. Everything about the
// catalog (releases, ROMs, disks, where files live) comes from it at runtime.
package rw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Bin is the romwbw-get executable; $ROMWBW_GET overrides it.
var Bin = envOr("ROMWBW_GET", "romwbw-get")

const (
	ROMPrefix  = "emu_"
	DiskPrefix = "hd1k_"
)

type ROM struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

type Disk struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Size         int64  `json:"size"`
	Format       string `json:"format"`
	Slices       int    `json:"slices"`
	DefaultSlot  *int   `json:"defaultSlot"`
	HostTransfer bool   `json:"host_transfer"`
}

// SliceCount is how many 8 MB slices the image holds (1 for a plain hd1k).
func (d Disk) SliceCount() int {
	if d.Slices < 1 {
		return 1
	}
	return d.Slices
}

type Catalog struct {
	Version string `json:"romwbw_version"`
	ROMs    []ROM  `json:"roms"`
	Disks   []Disk `json:"disks"`
}

type Release struct {
	Version string `json:"romwbw_version"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Default bool   `json:"default"`
}

type Versions struct {
	InUse    string    `json:"in_use"`
	Releases []Release `json:"romwbw_versions"`
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// run calls romwbw-get, optionally pinned to a release. Its progress messages
// (downloads, working copies) go to our stderr; stdout is returned.
func run(version string, args ...string) ([]byte, error) {
	if version != "" {
		args = append([]string{"--romwbw", version}, args...)
	}
	cmd := exec.Command(Bin, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", Bin, strings.Join(args, " "), err)
	}
	return out, nil
}

func List(version string) (*Catalog, error) {
	out, err := run(version, "list", "--json")
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(out, &c); err != nil {
		return nil, fmt.Errorf("parsing romwbw-get list --json: %w", err)
	}
	return &c, nil
}

func ListVersions() (*Versions, error) {
	out, err := run("", "versions", "--json")
	if err != nil {
		return nil, err
	}
	var v Versions
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("parsing romwbw-get versions --json: %w", err)
	}
	return &v, nil
}

// Paths returns the local path of each id, fetching as needed. With work set
// it returns writable working copies instead of the read-only originals.
func Paths(version string, work bool, ids ...string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []string{"path"}
	if work {
		args = append(args, "--work")
	}
	out, err := run(version, append(args, ids...)...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(ids) {
		return nil, fmt.Errorf("romwbw-get path: asked for %d paths, got %d", len(ids), len(lines))
	}
	return lines, nil
}

func CacheDir() (string, error) {
	out, err := run("", "cache-dir")
	return string(bytes.TrimSpace(out)), err
}

// Short drops the catalog prefix: hd1k_games -> games, emu_avw -> avw.
func Short(id, prefix string) string { return strings.TrimPrefix(id, prefix) }

// Resolve matches a user-typed name against catalog ids, accepting either the
// full id or the id without its prefix, case-insensitively.
func Resolve(kind, name, prefix string, ids []string) (string, error) {
	n := strings.ToLower(name)
	for _, id := range ids {
		if id == n || id == prefix+n {
			return id, nil
		}
	}
	short := make([]string, len(ids))
	for i, id := range ids {
		short[i] = Short(id, prefix)
	}
	sort.Strings(short)
	return "", fmt.Errorf("no %s named %q; this release has: %s", kind, name, strings.Join(short, ", "))
}

func (c *Catalog) ROMIDs() []string {
	ids := make([]string, len(c.ROMs))
	for i, r := range c.ROMs {
		ids[i] = r.ID
	}
	return ids
}

func (c *Catalog) DiskIDs() []string {
	ids := make([]string, len(c.Disks))
	for i, d := range c.Disks {
		ids[i] = d.ID
	}
	return ids
}

func (c *Catalog) DefaultROM() string {
	for _, r := range c.ROMs {
		if r.Default {
			return r.ID
		}
	}
	if len(c.ROMs) > 0 {
		return c.ROMs[0].ID
	}
	return ""
}

// DefaultDisk is the disk the catalog puts in slot 0 (what @disk0 means).
func (c *Catalog) DefaultDisk() string {
	for _, d := range c.Disks {
		if d.DefaultSlot != nil && *d.DefaultSlot == 0 {
			return d.ID
		}
	}
	return ""
}

func (c *Catalog) Disk(id string) Disk {
	for _, d := range c.Disks {
		if d.ID == id {
			return d
		}
	}
	return Disk{ID: id}
}

// ShellQuote renders argv as a copy-pasteable shell command.
func ShellQuote(argv []string) string {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@,+") == "" {
			b.WriteString(a)
		} else {
			b.WriteString("'" + strings.ReplaceAll(a, "'", `'\''`) + "'")
		}
	}
	return b.String()
}
