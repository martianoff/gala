/-
  # GoCore: the gc toolchain's choices

  What the Go specification leaves to the implementation and GoCore takes from the gc
  toolchain: **the gc toolchain's choices (Go 1.25.5, amd64), not the spec.** The
  differential run compares GoCore with `go run` built by exactly that toolchain
  (`goVersion`, which `tools/check_repo_policy.sh` checks against the Go SDK pinned in
  MODULE.bazel and the harness checks against the `go` it runs), so a change of policy
  in another Go release shows as a disagreement, not as a silent pass.

  * **Append growth.** When `append` needs more room, the spec says only that it
    allocates "a new, sufficiently large underlying array" (Go spec, "Appending to and
    copying slices"). gc's new capacity is `growslice` (runtime/slice.go): the capacity
    `nextslicecap` asks for, rounded up to the allocator's size class by `roundupsize`
    (runtime/msize.go) over the class sizes of internal/runtime/gc/sizeclasses.go.
  * **A stack buffer for an append from empty.** When escape analysis finds that an
    append's result does not escape, gc gives a slice grown from length 0 a 32-byte
    stack buffer instead (cmd/compile/internal/ssagen/ssa.go, `append`). GoCore does
    not model escape analysis, so such a slice has two possible capacities
    (`SliceRep.alt`); the evaluator stops `unsupported` when a run would tell them
    apart.
  * **`[]byte(s)` of a constant string.** The spec says only that the conversion gives
    a new slice (Go spec, "Conversions to and from a string type"); gc gives one of a
    constant string exactly its length (`constStringBytesCap`).
  * **No fused multiply-add.** The spec lets an implementation fuse `x*y + z` into one
    rounding (Go spec, "Arithmetic operators", "Floating-point operators"); gc on amd64
    at the default GOAMD64=v1 does not, so GoCore rounds each float operation (fixture
    `floats`, whose fused result would differ).
  * **Sizes and alignment** of types on amd64 (cmd/compile/internal/types/size.go,
    `CalcStructSize`), which the growth reads through the element type's size and
    whether it holds pointers.
  * **A variadic argument slice** has capacity equal to its length: the spec says only
    that it is a new slice with a new underlying array (Go spec, "Passing arguments to
    ... parameters").

  Nothing else in GoCore encodes these numbers. A proof about GoCore may rely only on
  what the spec guarantees about them (capacity at least the length, growth allocating
  a fresh array, an append within capacity sharing the array), never on their values.
-/
import Gala.GoCore.Value

namespace Gala.GoCore.Full.GcRuntime

/-- The Go release whose choices this module models, as `go env GOVERSION` prints it. -/
def goVersion : String := "go1.25.5"

/-- A type's size and alignment in bytes on amd64, and whether it holds pointers (the
    garbage collector's `noscan` is its negation). -/
structure Layout where
  size : Nat
  align : Nat
  ptrs : Bool
  deriving Inhabited, BEq

/-- The pointer size, `goarch.PtrSize`. -/
def ptrSize : Nat := 8

/-- An integer type: its width in bytes, aligned to its size. -/
def Layout.ofInt (k : IntKind) : Layout := ⟨k.bits / 8, k.bits / 8, false⟩

/-- A floating-point type of `bits` bits: its width in bytes, aligned to its size. -/
def Layout.ofFloat (bits : Nat) : Layout := ⟨bits / 8, bits / 8, false⟩

def Layout.bool : Layout := ⟨1, 1, false⟩
/-- A pointer, function, map or channel: one word that is a pointer. -/
def Layout.word : Layout := ⟨ptrSize, ptrSize, true⟩
/-- A string: a pointer and a length. -/
def Layout.string : Layout := ⟨2 * ptrSize, ptrSize, true⟩
/-- An interface value: a type word and a data word. -/
def Layout.iface : Layout := ⟨2 * ptrSize, ptrSize, true⟩
/-- A slice: a pointer, a length and a capacity. -/
def Layout.slice : Layout := ⟨3 * ptrSize, ptrSize, true⟩

/-- `[n]T`: `n` elements of `T` back to back. -/
def Layout.array (n : Nat) (e : Layout) : Layout := ⟨n * e.size, e.align, n > 0 && e.ptrs⟩

/-- `x` rounded up to a multiple of `a`. -/
def roundUp (x a : Nat) : Nat := if a == 0 then x else (x + a - 1) / a * a

/-- A struct of fields laid out in order, each at an offset aligned to its alignment,
    the whole rounded up to the largest alignment. A non-empty struct that ends in a
    zero-size field gets one byte of padding, so the address of that field never points
    past the struct (`CalcStructSize`). -/
def Layout.struct_ (fs : List Layout) : Layout :=
  let align := fs.foldl (fun a f => max a f.align) 1
  let size := fs.foldl (fun off f => roundUp off f.align + f.size) 0
  let size := match fs.getLast? with
    | some last => if size > 0 && last.size == 0 then size + 1 else size
    | none => size
  ⟨roundUp size align, align, fs.any (·.ptrs)⟩

/-- `nextslicecap` (runtime/slice.go): the capacity growth asks for. Double a small
    slice; grow a large one by about a quarter, smoothly, until the new length fits;
    and if even double the old capacity is too small, the new length itself. -/
def nextSliceCap (newLen oldCap : Nat) : Nat :=
  let double := oldCap + oldCap
  if newLen > double then newLen
  else if oldCap < 256 then double
  else
    -- Each round grows the capacity by at least 192, so `newLen` rounds are plenty.
    let rec grow : Nat → Nat → Nat
      | 0, c => c
      | k + 1, c =>
        let c := c + (c + 3 * 256) / 4
        if c ≥ newLen then c else grow k c
    grow newLen oldCap

/-- The size classes of the allocator's small objects (`SizeClassToSize`,
    internal/runtime/gc/sizeclasses.go); class 0 is unused. -/
def sizeClasses : List Nat :=
  [0, 8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240, 256,
   288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024, 1152, 1280,
   1408, 1536, 1792, 2048, 2304, 2688, 3072, 3200, 3456, 4096, 4864, 5376, 6144, 6528,
   6784, 6912, 8192, 9472, 9728, 10240, 10880, 12288, 13568, 14336, 16384, 18432, 19072,
   20480, 21760, 24576, 27264, 28672, 32768]

/-- `maxSmallSize`, `pageSize`, `MallocHeaderSize` and `MinSizeForMallocHeader`. -/
def maxSmallSize : Nat := 32768
def pageSize : Nat := 8192
def mallocHeaderSize : Nat := 8
def minSizeForMallocHeader : Nat := ptrSize * 64

/-- `roundupsize` (runtime/msize.go): the size of the block the allocator hands out for
    `size` bytes. A small object takes the smallest size class that holds it (what
    `SizeToSizeClass8` and `SizeToSizeClass128` look up: every class above 1024 bytes is
    a multiple of 128, every one below a multiple of 8); one with pointers above 512
    bytes carries an 8-byte malloc header in the block. A large object takes whole
    pages. -/
def roundUpSize (size : Nat) (noscan : Bool) : Nat :=
  if size ≤ maxSmallSize - mallocHeaderSize then
    let req := if !noscan && size > minSizeForMallocHeader then size + mallocHeaderSize else size
    match sizeClasses.find? (· ≥ req) with
    | some cls => cls - (req - size)
    | none => size
  else roundUp size pageSize

/-- The capacity `growslice` gives a slice of `elem` grown from capacity `oldCap` to
    length `newLen`: the block for `nextSliceCap` elements, as many elements as fit in
    it. A zero-size element gets exactly `newLen`. -/
def grownCap (newLen oldCap : Nat) (elem : Layout) : Nat :=
  if elem.size == 0 then newLen
  else roundUpSize (nextSliceCap newLen oldCap * elem.size) (!elem.ptrs) / elem.size

/-- `VariableMakeThreshold` (cmd/compile/internal/base/flag.go): the most bytes of a
    stack-allocated backing store for an `append`. -/
def stackAppendBytes : Nat := 32

/-- The capacity an append growing a slice from length `oldLen` to `newLen` gets when gc
    gives it a stack-allocated backing store (cmd/compile/internal/ssagen/ssa.go,
    `append`): as many elements as fit in 32 bytes, for a slice grown from length 0 by
    `append(s, x, ...)`, not `append(s, xs...)` (`spread`). gc does this only when escape
    analysis finds that the result does not escape (and at most once per call site and
    call), which GoCore does not decide; so it is a capacity the slice *may* have, and
    `none` where it cannot. -/
def stackAppendCap (elem : Layout) (oldLen newLen : Nat) (spread : Bool) : Option Nat :=
  if oldLen == 0 && !spread && elem.size > 0 && elem.size ≤ stackAppendBytes &&
      newLen ≤ stackAppendBytes / elem.size then
    some (stackAppendBytes / elem.size)
  else none

/-- The capacity of `[]byte(s)` for a constant string of `n` bytes: gc allocates a
    `[n]byte` array for it, on the stack or the heap (cmd/compile/internal/walk/
    convert.go, `walkStringToBytes`), so exactly `n`. A non-constant string goes
    through `runtime.stringtoslicebyte`, whose capacity depends on escape analysis
    and on the allocator; GoCore does not model that conversion. -/
def constStringBytesCap (n : Nat) : Nat := n

/-- The capacity of the slice a variadic parameter receives for `k` arguments. -/
def variadicCap (k : Nat) : Nat := k

end Gala.GoCore.Full.GcRuntime
