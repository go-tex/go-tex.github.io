// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"github.com/go-icons/devicon"
	"github.com/go-icons/material"
	"github.com/go-icons/seti"
	vscodeicons "github.com/go-icons/vscode-icons"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// iconDrawer is the shape a TreeTableNode.Icon takes: paint a glyph into a box.
type iconDrawer = func(painter.Painter, toolkit.Rect, toolkit.RGBA)

// iconPack is a file-type icon pack the workspace tree draws from: a name for
// the picker, and lookups from a file name / a directory to an SVG document. The
// SVGs come from a go-icons pack repo; toolkit.SVGIcon turns them into glyphs.
type iconPack struct {
	name   string
	file   func(string) string // filename -> SVG
	folder func() string       // directory SVG
}

// iconPacks are the packs the reader can choose between, in picker order. The
// first is the default.
var iconPacks = []iconPack{
	{seti.Name, seti.Icon, seti.Folder},
	{material.Name, material.Icon, material.Folder},
	{vscodeicons.Name, vscodeicons.Icon, vscodeicons.Folder},
	{devicon.Name, devicon.Icon, devicon.Folder},
}

// iconPackNames is the picker's option list, parallel to iconPacks.
func iconPackNames() []string {
	out := make([]string, len(iconPacks))
	for i, p := range iconPacks {
		out[i] = p.name
	}
	return out
}

// fileIconDrawer renders the pack's icon for a file named name through the
// toolkit's SVG icon renderer.
func (p iconPack) fileIconDrawer(name string) iconDrawer {
	return toolkit.SVGIcon(p.file(name))
}

// folderIconDrawer renders the pack's directory icon.
func (p iconPack) folderIconDrawer() iconDrawer {
	return toolkit.SVGIcon(p.folder())
}
