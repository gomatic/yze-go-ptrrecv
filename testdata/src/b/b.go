// Package b is run with -allow=b.special: the configured exemption APPLIES to
// Holder, and must EXTEND the derived criterion rather than replace it, which
// Baked is here to prove.
package b

import "bytes"

// special is a custom type callers may declare no-copy via configuration. It is
// not in the standard library, so nothing derives its copy semantics.
type special struct{ _ int }

// Holder contains a special field; with -allow=b.special its pointer receiver
// is permitted.
type Holder struct {
	s special
	n int
}

func (h *Holder) N() int { return h.n }

// Baked is protected by the derived criterion — bytes.Buffer's method set is
// entirely pointer-based — and configuring -allow must not take that away. A
// build that let configuration replace the derived criterion reports Baked.
type Baked struct {
	buf bytes.Buffer
	n   int
}

func (b *Baked) N() int { return b.n }

// AliasHolder reaches the configured type through an ALIAS, which the lookup
// must resolve: -allow names b.special, and b.specialAlias is the same type
// under another name.
type specialAlias = special

type AliasHolder struct {
	s specialAlias
	n int
}

func (a *AliasHolder) N() int { return a.n }

// Control holds neither, so the silence above is never mistaken for a run that
// reported nothing at all.
type Control struct{ n int }

func (c *Control) N() int { return c.n } // want `pointer receiver on Control should be a value receiver`
