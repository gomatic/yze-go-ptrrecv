# yze-go-ptrrecv

A [`yze`](https://github.com/gomatic/yze) analyzer (category `immutability`) enforcing the gomatic Go immutability standard: methods use **value receivers**, never pointer receivers, unless the receiver type transitively contains a field that cannot be copied (a `sync` primitive, `sync/atomic` type, `bytes.Buffer`, or `strings.Builder`).

Pointer receivers imply mutability — and on exported types that leaks into interface semantics and concurrency expectations. This analyzer flags every pointer-receiver method whose type holds no no-copy field.

The rule is intentionally narrow: only a transitively-uncopyable field justifies a pointer receiver. Mutation and interface-satisfaction are deliberately **not** carve-outs — that narrowness reflects the gomatic immutability preference. The no-copy walk recurses through nested structs and through array elements (an array stores its elements inline, so a no-copy element makes the array uncopyable), but not through slices, maps, channels, or pointers, which are references that leave the enclosing struct copyable.

- **Rule:** `yze/ptrrecv`
- **No-copy types:** 16 baked-in standard-library types, extensible at runtime via the `-allow` flag (comma-separated fully-qualified `pkgpath.Name` entries).
- **Suggested fix:** the diagnostic carries a "change to a value receiver" fix (deleting the receiver's `*`) only when the rewrite is provably behavior-preserving: the body never assigns through the receiver (including `++`/`--`, index/field chains, and range assignment), never takes the address of the receiver or anything reachable through it, never calls a pointer-receiver method on the receiver, and never mentions the receiver bare (which would change its pointer-typed semantics). Since the yze driver applies fixes to packages loaded without test files, dropping the `*` is chosen because it only widens the method set — unseen callers keep compiling — and the mutation analysis guarantees no copy is silently mutated. When in doubt the diagnostic is reported without a fix.
- **Binary:** `cmd/yze-go-ptrrecv` runs it standalone (`text`/`-json`, and as a `go vet -vettool`).

Built on the [`go-yze`](https://github.com/gomatic/go-yze) framework.
