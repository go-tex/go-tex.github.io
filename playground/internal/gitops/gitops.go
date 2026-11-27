// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package gitops names the one git-worker operation the playground speaks that
// github.com/go-browserhttp/browsergit/gitrpc (v0.4.0) does not define.
//
// OpRemoveFile deletes one working-tree file and stages the removal. It is the
// one file verb that cannot be expressed as a write: creating, importing and the
// first half of a rename all carry content, a removal has none, so a workspace
// with only writeFile can add files for ever and never lose one. The browsergit
// module has the method (Repo.RemoveFile) but not the wire name; this constant
// is the stopgap until the op moves there, at which point this package goes.
package gitops

// OpRemoveFile is the gitrpc.Request.Op that asks the worker to remove a file.
const OpRemoveFile = "removeFile"
