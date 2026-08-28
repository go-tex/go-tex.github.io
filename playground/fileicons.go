// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import (
	"strings"

	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// File-tree icon accents. The workspace tree gives each row a leading glyph (a
// folder, or a document tinted by file type) so a repository reads at a glance.
// The tints are mid-tone hues chosen to stay legible on BOTH the light and the
// dark surface — the tree paints them verbatim (the drawers ignore the row ink
// they are handed and use these), so they do not follow the theme; a mid-tone is
// the safe compromise a single value can make for both grounds.
var (
	inkFolder = toolkit.RGB(0xF5, 0x9E, 0x0B) // amber — folders
	inkTeX    = toolkit.RGB(0x00, 0x79, 0xA8) // go-tex blue — .tex/.sty/.cls/.bib
	inkImage  = toolkit.RGB(0x8B, 0x5C, 0xF6) // violet — images / PDF
	inkCode   = toolkit.RGB(0x3B, 0x82, 0xF6) // blue — source code
	inkData   = toolkit.RGB(0x64, 0x74, 0x8B) // slate — data / config
	inkDoc    = toolkit.RGB(0x8A, 0x93, 0x9B) // grey — everything else
)

// brandIndigo is the go-tex mark colour (the favicon tile), used for the sidebar
// logo tile and wordmark. It reads on both the light and the dark surface.
var brandIndigo = toolkit.RGB(0x4F, 0x46, 0xE5)

// folderIcon paints the amber folder glyph a directory row shows. It is a plain
// function value (not a per-node closure) because every folder looks the same.
func folderIcon(p painter.Painter, r toolkit.Rect, _ toolkit.RGBA) {
	toolkit.DrawIconOpen(p, r, inkFolder)
}

// fileIcon returns the document-glyph drawer for a file named name, tinted by its
// extension so a .tex reads apart from an image, some code, or a config file.
func fileIcon(name string) func(painter.Painter, toolkit.Rect, toolkit.RGBA) {
	tint := fileTypeInk(name)
	return func(p painter.Painter, r toolkit.Rect, _ toolkit.RGBA) {
		toolkit.DrawIconNew(p, r, tint)
	}
}

// fileTypeInk maps a file name to its tree-icon tint by extension.
func fileTypeInk(name string) toolkit.RGBA {
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 {
		return inkDoc
	}
	switch strings.ToLower(name[dot:]) {
	case ".tex", ".sty", ".cls", ".bib", ".dtx", ".ins":
		return inkTeX
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".pdf", ".eps", ".webp":
		return inkImage
	case ".go", ".js", ".ts", ".sh", ".py", ".rb", ".c", ".h", ".cpp", ".rs", ".lua":
		return inkCode
	case ".json", ".yaml", ".yml", ".toml", ".xml", ".csv", ".ini", ".cfg":
		return inkData
	default:
		return inkDoc
	}
}
