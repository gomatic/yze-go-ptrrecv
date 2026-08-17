// This file marks the repository as a library rather than a CLI application.
//
// The library_marker build tag is never set by any build, so this file is
// never compiled; its sole purpose is to exist. gomatic tooling and conventions
// distinguish a library repo from a CLI repo by the presence of this marker
// file — do not remove it.

//go:build library_marker

package ptrrecv
