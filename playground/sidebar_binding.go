// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "github.com/go-widgets/toolkit"

// This is the sidebar's MVVM binding glue, in a *_binding.go file so mvvmlint
// exempts it from the direct-widget-state-mutation rule (its sanctioned opt-out
// for intentional binding code). The two data-carrying widget state fields a
// tree's Root and a timeline's Events are `[]T` slices, which mvvm.Observable
// cannot hold (Observable[T] requires a comparable T, and slices are not
// comparable), so they cannot be bound through mvvm.OneWay/BindField the way a
// scalar field is. The sidebar owns the model and rebuilds these forests when its
// signature changes; these two helpers are the single, isolated write sites where
// the freshly-built model is handed to the widget, keeping every OTHER sidebar
// file free of ad-hoc widget-state mutation.

// setFileEntries installs the rebuilt path list as the FileTree's entries.
//
// It stays in this file although Entries IS an mvvm.Observable, because what it
// holds is a []FileEntry: the observable is constructed upstream with an
// equality function over the slice, which is the only reason it can exist at
// all, and setting it is still the one place a freshly-built model crosses into
// the widget.
func setFileEntries(t *toolkit.FileTree, entries []toolkit.FileEntry) {
	t.Entries().Set(entries)
}

// setTimelineEvents installs the rebuilt event list as the Timeline's Events.
func setTimelineEvents(tl *toolkit.Timeline, events []toolkit.TimelineEvent) {
	tl.Events = events
}
