// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

package tui

import "github.com/vaelen/romwbw_tools/internal/rw"

// PickVersion asks which RomWBW release to use, starting on the one
// romwbw-get currently has in use.
func PickVersion() (string, error) {
	v, err := rw.ListVersions()
	if err != nil {
		return "", err
	}
	items := make([]Item, len(v.Releases))
	cur := 0
	for i, r := range v.Releases {
		d := r.Status
		if r.Version == v.InUse {
			d += " (in use)"
			cur = i
		}
		items[i] = Item{Label: r.Version, Detail: d}
	}
	i, err := Select("RomWBW release", items, cur)
	if err != nil {
		return "", err
	}
	return v.Releases[i].Version, nil
}
