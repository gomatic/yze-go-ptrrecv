package b

// special is a custom type callers may declare no-copy via configuration.
type special struct{ _ int }

// Holder contains a special field; with -allow=b.special its pointer receiver is
// permitted.
type Holder struct{ s special }

func (h *Holder) Touch() { _ = h.s }
