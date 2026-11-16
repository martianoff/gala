/-
  # gala2lean's runtime model

  Every Lean program `internal/formal/gala2lean` writes imports this module. It is the
  hand-written part of the translation, and so part of what the translated programs'
  meaning rests on (formal/README.md, "What gala2lean trusts"): Go's `int64` arithmetic
  where Lean's differs, GALA's panics, and how `Println`/`Print` lay out values
  (docs/GALA.MD, "How Values Print"; galaspec's `Gala/Print.lean` models the same text).

  It is independent of galaspec: it shares no definitions with `Gala/Eval.lean` or
  `Gala/Print.lean`, so the two models check each other.

  A translated program runs in `Prog σ`: a state `σ` (the printed output and the
  package-level variables), a GALA panic, or divergence. Divergence is `Option`'s `none`,
  as in Aeneas: the monad is a chain-complete partial order, so recursive functions are
  defined with `partial_fixpoint` and have equations to reason with, instead of being
  opaque `partial def`s.
-/

namespace Gala.Gen

/-- How a GALA program panics; `name` is the panic class galaspec and the differential
    harness report. -/
inductive Panic where
  /-- Integer division or modulo by zero. -/
  | divide
  /-- No arm of a `match` matched. -/
  | noArm
  /-- A negative shift count. -/
  | shift
  /-- A panic the standard library raises with a message (`go_builtins.Panic`), such
      as `Array.Get` out of range: a string panic, class `other` in Go. -/
  | std
  deriving Repr, Inhabited, BEq

def Panic.name : Panic → String
  | .divide => "divide"
  | .noArm => "match"
  | .shift => "other"
  | .std => "other"

/-- The monad of a translated program: state `σ`, then a panic (which keeps the state, so
    the output printed before it survives), then divergence (`none`). -/
abbrev Prog (σ : Type) := ExceptT Panic (StateT σ Option)

/-- An action that panics; it makes function types, and so structs holding functions,
    inhabited. -/
instance : Inhabited (Prog σ α) := ⟨throw .noArm⟩

/-- A program state that holds the printed output. -/
class HasOut (σ : Type) where
  out : σ → String
  setOut : σ → String → σ

/-- How a value prints under `%v`. `gshow` is the text when `fmt` reaches the value with
    its methods (as a `Println` operand, or through an exported field); `graw` when it
    reaches it through an unexported field, where `fmt` prints the Go struct itself.
    `isStr` is true for strings, for `Print`'s spacing. -/
class GShow (α : Type) where
  gshow : α → String
  graw : α → String := gshow
  isStr : Bool := false
export GShow (gshow graw)

instance : GShow Int64 := ⟨fun n => toString n.toInt, fun n => toString n.toInt, false⟩
instance : GShow String := ⟨id, id, true⟩
instance : GShow Bool where
  gshow b := if b then "true" else "false"

/-- A `val` field is stored in an `Immutable` wrapper, which `fmt` prints raw as `{v}`;
    a `var` field is stored as it is. -/
def wrapImm (mutable : Bool) (s : String) : String := if mutable then s else "{" ++ s ++ "}"

/-! Tuples. GALA's tuple fields are `V1`, `V2`, ... (docs/GALA.MD, "Tuple"); each is a
    `val` field, so it prints raw inside an `Immutable`. -/

structure T2 (α β : Type) where
  V1 : α
  V2 : β
  deriving BEq, Inhabited
structure T3 (α β γ : Type) where
  V1 : α
  V2 : β
  V3 : γ
  deriving BEq, Inhabited
structure T4 (α β γ δ : Type) where
  V1 : α
  V2 : β
  V3 : γ
  V4 : δ
  deriving BEq, Inhabited

instance [GShow α] [GShow β] : GShow (T2 α β) where
  gshow t := "(" ++ gshow t.V1 ++ ", " ++ gshow t.V2 ++ ")"
  graw t := "{" ++ wrapImm false (graw t.V1) ++ " " ++ wrapImm false (graw t.V2) ++ "}"
instance [GShow α] [GShow β] [GShow γ] : GShow (T3 α β γ) where
  gshow t := "(" ++ gshow t.V1 ++ ", " ++ gshow t.V2 ++ ", " ++ gshow t.V3 ++ ")"
  graw t := "{" ++ " ".intercalate [wrapImm false (graw t.V1), wrapImm false (graw t.V2),
    wrapImm false (graw t.V3)] ++ "}"
instance [GShow α] [GShow β] [GShow γ] [GShow δ] : GShow (T4 α β γ δ) where
  gshow t := "(" ++ ", ".intercalate [gshow t.V1, gshow t.V2, gshow t.V3, gshow t.V4] ++ ")"
  graw t := "{" ++ " ".intercalate [wrapImm false (graw t.V1), wrapImm false (graw t.V2),
    wrapImm false (graw t.V3), wrapImm false (graw t.V4)] ++ "}"

/-! `Option` and `Either` from GALA's standard library (docs/GALA.MD, "Option",
    "Either"): sealed types that print like any sealed value, `Some(1)`, `None()`. -/

inductive GOption (α : Type) where
  | Some (Value : α)
  | None
  deriving BEq, Inhabited

inductive GEither (α β : Type) where
  | Left (Value : α)
  | Right (Value : β)
  deriving BEq, Inhabited

instance [GShow α] : GShow (GOption α) where
  gshow
    | .Some v => "Some(" ++ gshow v ++ ")"
    | .None => "None()"

instance [GShow α] [GShow β] : GShow (GEither α β) where
  gshow
    | .Left v => "Left(" ++ gshow v ++ ")"
    | .Right v => "Right(" ++ gshow v ++ ")"

/-- A function value prints as `<fn>` inside the `String` method GALA generates for a
    sealed value or shorthand struct (galaspec's `fmtStr`). -/
def fnText : String := "<fn>"

/-- One operand of `Println`/`Print`: its text, and whether it is a string. -/
structure Arg where
  text : String
  isStr : Bool

def garg [inst : GShow α] (x : α) : Arg := ⟨gshow x, inst.isStr⟩

section
variable {σ : Type} [HasOut σ]

/-- Append to the printed output. -/
def emit (s : String) : Prog σ Unit :=
  modify fun st => HasOut.setOut st (HasOut.out st ++ s)

/-- `Println`, Go's `fmt.Println`: operands separated by spaces, then a newline. -/
def gprintln (as : List Arg) : Prog σ Unit :=
  emit (" ".intercalate (as.map (·.text)) ++ "\n")

/-- The text of `Print`, Go's `fmt.Print`: a space between two operands when neither is
    a string. -/
def printText : Option Arg → List Arg → String
  | _, [] => ""
  | none, a :: r => a.text ++ printText (some a) r
  | some p, a :: r => (if !p.isStr && !a.isStr then " " else "") ++ a.text ++ printText (some a) r

def gprint (as : List Arg) : Prog σ Unit := emit (printText none as)
end

/-- `+` on `int` (wrapping) and on `string` (concatenation). -/
class GAdd (α : Type) where
  gadd : α → α → α
export GAdd (gadd)
instance : GAdd Int64 := ⟨(· + ·)⟩
instance : GAdd String := ⟨(· ++ ·)⟩

section
variable {σ : Type}

/-- Go's `/` on `int64`: truncating, a panic on zero; `min / -1` is `min` (as Lean's
    `Int64` division). -/
def gdiv (a b : Int64) : Prog σ Int64 := if b == 0 then throw .divide else pure (a / b)
/-- Go's `%` on `int64`: the sign of the dividend, a panic on zero. -/
def gmod (a b : Int64) : Prog σ Int64 := if b == 0 then throw .divide else pure (a % b)
/-- Go's `<<`: a count of 64 or more shifts every bit out; a negative count panics. -/
def gshl (a b : Int64) : Prog σ Int64 :=
  if b < 0 then throw .shift else pure (if b ≥ 64 then 0 else a <<< b)
/-- Go's `>>` (arithmetic): a count of 64 or more leaves only the sign; a negative count
    panics. -/
def gshr (a b : Int64) : Prog σ Int64 :=
  if b < 0 then throw .shift else pure (if b ≥ 64 then (if a < 0 then -1 else 0) else a >>> b)
end

/-! `Array` and `List` from GALA's `collection_immutable` (`collection_immutable/array.gala`,
    `collection_immutable/list.gala`), as the list of their elements. Each function below
    cites the std code it mirrors, and runs the functions it is given in the order and
    with the early exits that code does. A function that panics in std (`go_builtins.Panic`
    with a message) throws `Panic.std`, class `other`, as the Go run is classed.

    Both compare as their `Equal` does: the same length, then each element `Equal`, which
    for the types gala2lean translates is the derived `BEq` (array.gala:294, list.gala:131).
    -/

structure GArray (α : Type) where
  data : List α
  deriving BEq, Inhabited

structure GList (α : Type) where
  data : List α
  deriving BEq, Inhabited

/-- `Array.String` (array.gala:1107) and `List.String` (list.gala:652): `Array(1, 2)`,
    each element as `s"${e}"` prints it. -/
def seqText [GShow α] (name : String) (xs : List α) : String :=
  name ++ "(" ++ ", ".intercalate (xs.map gshow) ++ ")"

instance [GShow α] : GShow (GArray α) where
  gshow a := seqText "Array" a.data
instance [GShow α] : GShow (GList α) where
  gshow l := seqText "List" l.data

section
variable {σ : Type}

/-- `GetOption`, `HeadOption`, `LastOption`: `None` where `Get`, `Head`, `Last` panic
    (array.gala:452, 469, 486; list.gala:100, 216, 250). -/
def toGOption : Option α → GOption α
  | some x => .Some x
  | none => .None

/-- The element found, or `Panic.std` where std panics for want of one. -/
def orStd : Option α → Prog σ α
  | some x => pure x
  | none => throw .std

/-- The element at index `i`, or `Panic.std` out of range: `Array.Get` (array.gala:433),
    `List.Get` (list.gala:204), `Updated`'s range check. -/
def seqGet (xs : List α) (i : Int64) : Prog σ α :=
  if i < 0 then throw .std else orStd xs[i.toInt.toNat]?

/-- `Prepend(v)`: `v` first (array.gala:609, list.gala:154). It takes the receiver
    first, so that the translation evaluates the receiver before the argument, as Go
    does. -/
def seqPrepend (xs : List α) (v : α) : List α := v :: xs

/-- `Updated(i, v)`: `Panic.std` out of range (array.gala:507, list.gala:224). -/
def seqUpdated (xs : List α) (i : Int64) (v : α) : Prog σ (List α) :=
  if i < 0 || i.toInt.toNat ≥ xs.length then throw .std else pure (xs.set i.toInt.toNat v)

/-- `Take(n)`: nothing for `n ≤ 0`, everything for `n` past the end (array.gala:673,
    list.gala:271). -/
def seqTake (xs : List α) (n : Int64) : List α := if n ≤ 0 then [] else xs.take n.toInt.toNat

/-- `Drop(n)`: everything for `n ≤ 0`, nothing for `n` past the end (array.gala:688,
    list.gala:279). -/
def seqDrop (xs : List α) (n : Int64) : List α := if n ≤ 0 then xs else xs.drop n.toInt.toNat

/-- `IndexOf(e)`: the first index whose element is `Equal`, else -1 (array.gala:750,
    list.gala:314). -/
def seqIndexOf [BEq α] (xs : List α) (e : α) : Int64 :=
  match xs.findIdx? (· == e) with
  | some i => Int64.ofNat i
  | none => -1

/-- `FoldLeft`: `f` on each element from the first (array.gala:851, list.gala:415). -/
def seqFoldLeft (xs : List α) (z : β) (f : β → α → Prog σ β) : Prog σ β := xs.foldlM f z

/-- `FoldRight`: `f` on each element from the last; List's recursion
    `f(l.head, tail.FoldRight(...))` (list.gala:426) evaluates the tail's fold before `f`,
    as Array's loop from the end does (array.gala:860). -/
def seqFoldRight (xs : List α) (z : β) (f : α → β → Prog σ β) : Prog σ β := xs.foldrM f z

/-- `Reduce`: `Panic.std` on an empty sequence, else a left fold from the first element
    (array.gala:870, list.gala:436). -/
def seqReduce (xs : List α) (f : α → α → Prog σ α) : Prog σ α :=
  match xs with
  | [] => throw .std
  | x :: r => r.foldlM f x

/-- `Map`: `f` on each element from the first. Array's loop (array.gala:780), and List's
    `consList(f(l.head), tail.Map(f))` (list.gala:344), whose arguments Go evaluates left to
    right. -/
def seqMap (xs : List α) (f : α → Prog σ β) : Prog σ (List β) := xs.mapM f

/-- `Filter`: `p` on each element from the first (array.gala:821; list.gala:380 tests the
    head before the tail's filter). -/
def seqFilter (xs : List α) (p : α → Prog σ Bool) : Prog σ (List α) := xs.filterM p

/-- `ForEach`: `f` on each element from the first (array.gala:888, list.gala:453). -/
def seqForEach (xs : List α) (f : α → Prog σ Unit) : Prog σ Unit := xs.forM f

/-- `Exists`: `p` from the first element, stopping at the first true (array.gala:895,
    list.gala:462). -/
def seqExists (xs : List α) (p : α → Prog σ Bool) : Prog σ Bool := xs.anyM p

/-- `ForAll`: `p` from the first element, stopping at the first false (array.gala:905,
    list.gala:474). -/
def seqForAll (xs : List α) (p : α → Prog σ Bool) : Prog σ Bool := xs.allM p

/-- `Find`: `p` from the first element, stopping at the first true (array.gala:915,
    list.gala:486). -/
def seqFind (xs : List α) (p : α → Prog σ Bool) : Prog σ (GOption α) :=
  toGOption <$> xs.findM? p

/-- `Count`: `p` on each element from the first, through `FoldLeft` (array.gala:937,
    list.gala:498). -/
def seqCount (xs : List α) (p : α → Prog σ Bool) : Prog σ Int64 :=
  xs.foldlM (fun n x => do pure (if ← p x then n + 1 else n)) 0

/-- `Partition(p)`: `p` on each element from the first, in one pass; the elements it
    holds for, then the others, each in order (array.gala:836, list.gala:399). -/
def seqPartition (xs : List α) (p : α → Prog σ Bool) : Prog σ (List α × List α) := do
  let (yes, no) ← xs.foldlM (fun (yes, no) x => do
    pure (if ← p x then (x :: yes, no) else (yes, x :: no))) ([], [])
  pure (yes.reverse, no.reverse)

/-- `ZipWithIndex()`: each element with its index (array.gala:960, list.gala:519). -/
def seqZipWithIndex (xs : List α) : List (T2 α Int64) :=
  xs.zipIdx.map fun (x, i) => T2.mk x (Int64.ofNat i)

/-- `MkString(sep)`: the elements as `s"${e}"` prints them, `sep` between (array.gala:1122,
    list.gala:671). -/
def seqMkString [GShow α] (xs : List α) (sep : String) : String := sep.intercalate (xs.map gshow)

/-- The first and last elements, `Panic.std` when empty: `Head`, `Last` (array.gala:461,
    478; list.gala:92, 236). -/
def seqHead (xs : List α) : Prog σ α := orStd xs.head?
def seqLast (xs : List α) : Prog σ α := orStd xs.getLast?

/-- `FlatMap(f)`: `f` on each element from the first, the results' elements in order
    (array.gala:790; list.gala:353's `f(l.head).AppendAll(tail.FlatMap(f))` calls `f` on
    the head before the tail's). `data` reads a result's elements. -/
def seqFlatMap (xs : List α) (f : α → Prog σ γ) (data : γ → List β) : Prog σ (List β) := do
  let ys ← xs.mapM f
  pure (ys.flatMap data)

/-- `Collect(pf)`: `pf` on each element from the first, the values of the `Some`s it
    returns in order (array.gala:804, list.gala:363). -/
def seqCollect (xs : List α) (pf : α → Prog σ (GOption β)) : Prog σ (List β) :=
  xs.filterMapM fun x => do
    match ← pf x with
    | .Some v => pure (some v)
    | .None => pure none

/-- `Zip(other)`: pairs up to the shorter length (array.gala:947, list.gala:508). -/
def seqZip (xs : List α) (ys : List β) : List (T2 α β) :=
  (xs.zip ys).map fun (a, b) => T2.mk a b

/-- `TakeWhile(p)`: `p` from the first element, stopping at the first false
    (array.gala:704, list.gala:287). -/
def seqTakeWhile : List α → (α → Prog σ Bool) → Prog σ (List α)
  | [], _ => pure []
  | x :: r, p => do if ← p x then (x :: ·) <$> seqTakeWhile r p else pure []

/-- `DropWhile(p)`: `p` from the first element, stopping at the first false
    (array.gala:717, list.gala:295). -/
def seqDropWhile : List α → (α → Prog σ Bool) → Prog σ (List α)
  | [], _ => pure []
  | x :: r, p => do if ← p x then seqDropWhile r p else pure (x :: r)

/-- `FindLast(p)`: `p` from the last element, stopping at the first true
    (array.gala:926). -/
def seqFindLast (xs : List α) (p : α → Prog σ Bool) : Prog σ (GOption α) :=
  toGOption <$> xs.reverse.findM? p

/-- `Grouped(n)`: `Panic.std` for `n ≤ 0`, else the elements in order, a new group after
    each `n` of them, the last group shorter (array.gala:1044's loop `i += n` with
    `Slice(i, min(i + n, length))`). One pass: the group being filled is kept reversed,
    with its size. -/
def seqGrouped (xs : List α) (n : Int64) : Prog σ (List (GArray α)) :=
  if n ≤ 0 then throw .std
  else
    let k := n.toInt.toNat
    let (done, cur, _) := xs.foldl (fun (done, cur, c) x =>
      if c + 1 = k then (GArray.mk (x :: cur).reverse :: done, [], 0) else (done, x :: cur, c + 1))
      (([] : List (GArray α)), ([] : List α), (0 : Nat))
    pure (if cur.isEmpty then done.reverse else (GArray.mk cur.reverse :: done).reverse)

/-- `Distinct()`: the first occurrence of each element, in order (array.gala:972,
    list.gala:533, through hashset.gala:89's `firstOccurrence`). std keeps an element
    unless an earlier kept one is `Equal` to it, by value through a HashSet for a
    primitive type and by `Equal` otherwise; `eraseDups` keeps it unless an earlier one
    is `==` to it. These agree because the `BEq` the translation derives is GALA's
    generated `Equal`, which is symmetric; a type with its own `Equal` is reported
    before this runs (`compares`). -/
def seqDistinct [BEq α] (xs : List α) : List α := xs.eraseDups

/-- `ArrayTabulate(n, f)`: `f(0)` to `f(n - 1)` in order, nothing for `n ≤ 0`
    (collection_immutable/array.gala:95's loop `i := 0; i < n`). -/
def seqTabulate (n : Int64) (f : Int64 → Prog σ α) : Prog σ (List α) :=
  (List.range n.toInt.toNat).mapM fun i => f (Int64.ofNat i)

/-- `ArrayFill(n, v)`: `v` `n` times, nothing for `n ≤ 0`
    (collection_immutable/array.gala:106). -/
def seqFill (n : Int64) (v : α) : List α := List.replicate n.toInt.toNat v

/-- `When(c, v)`: `Some(v)` when `c`, else `None` (std/option.gala:127); `Unless(c, v)`
    (std/option.gala:130) is `When(!c, v)`. Both arguments are evaluated before the call,
    as Go evaluates them. -/
def optWhen (c : Bool) (v : α) : GOption α := if c then .Some v else .None

/-! Methods of `Option` (std/option.gala) and `Either` (std/either.gala), each after the
    std code it cites. A function argument runs only where std calls it; where std panics
    the model throws `Panic.std`. -/

/-- `IsDefined()` (option.gala:33); `IsEmpty()` (option.gala:36) is its negation. -/
def optIsDefined : GOption α → Bool
  | .Some _ => true
  | .None => false

/-- `Get()`: `Panic.std` on `None` (option.gala:39). -/
def optGet : GOption α → Prog σ α
  | .Some v => pure v
  | .None => throw .std

/-- `GetOrElse(d)` (option.gala:48). -/
def optGetOrElse : GOption α → α → α
  | .Some v, _ => v
  | .None, d => d

/-- `OrElse(alt)` (option.gala:56). -/
def optOrElse : GOption α → GOption α → GOption α
  | .Some v, _ => .Some v
  | .None, alt => alt

/-- `OnSome(f)`: `f` on the value, the option unchanged (option.gala:65). -/
def optOnSome (o : GOption α) (f : α → Prog σ Unit) : Prog σ (GOption α) := do
  if let .Some v := o then f v
  pure o

/-- `OnNone(f)`: `f` on `None`, the option unchanged (option.gala:74). -/
def optOnNone (o : GOption α) (f : Unit → Prog σ Unit) : Prog σ (GOption α) := do
  if let .None := o then f ()
  pure o

/-- `ForEach(f)` (option.gala:83). -/
def optForEach (o : GOption α) (f : α → Prog σ Unit) : Prog σ Unit :=
  match o with
  | .Some v => f v
  | .None => pure ()

/-- `Map(f)` (option.gala:92). -/
def optMap (o : GOption α) (f : α → Prog σ β) : Prog σ (GOption β) :=
  match o with
  | .Some v => .Some <$> f v
  | .None => pure .None

/-- `FlatMap(f)` (option.gala:102). -/
def optFlatMap (o : GOption α) (f : α → Prog σ (GOption β)) : Prog σ (GOption β) :=
  match o with
  | .Some v => f v
  | .None => pure .None

/-- `Filter(p)`: `p` on the value of `Some` only (option.gala:111). -/
def optFilter (o : GOption α) (p : α → Prog σ Bool) : Prog σ (GOption α) :=
  match o with
  | .Some v => do pure (if ← p v then o else .None)
  | .None => pure .None

/-- `Exists(p)`: `isSome() && p(Value)`, so `p` runs on `Some` only (option.gala:120). -/
def optExists (o : GOption α) (p : α → Prog σ Bool) : Prog σ Bool :=
  match o with
  | .Some v => p v
  | .None => pure false

/-- `ForAll(p)`: `isNone() || p(Value)` (option.gala:124). -/
def optForAll (o : GOption α) (p : α → Prog σ Bool) : Prog σ Bool :=
  match o with
  | .Some v => p v
  | .None => pure true

/-- `IsLeft()` (either.gala:19); `IsRight()` (either.gala:22) is its negation. -/
def eitherIsLeft : GEither α β → Bool
  | .Left _ => true
  | .Right _ => false

/-- `GetLeft()`: `Panic.std` on a `Right` (either.gala:49). -/
def eitherGetLeft : GEither α β → Prog σ α
  | .Left a => pure a
  | .Right _ => throw .std

/-- `GetRight()`: `Panic.std` on a `Left` (either.gala:57). -/
def eitherGetRight : GEither α β → Prog σ β
  | .Right b => pure b
  | .Left _ => throw .std

/-- `Swap()` (either.gala:65). -/
def eitherSwap : GEither α β → GEither β α
  | .Left a => .Right a
  | .Right b => .Left b

/-- `OnRight(f)` (either.gala:74). -/
def eitherOnRight (e : GEither α β) (f : β → Prog σ Unit) : Prog σ (GEither α β) := do
  if let .Right b := e then f b
  pure e

/-- `OnLeft(f)` (either.gala:83). -/
def eitherOnLeft (e : GEither α β) (f : α → Prog σ Unit) : Prog σ (GEither α β) := do
  if let .Left a := e then f a
  pure e

/-- `Fold(f1, f2)`: `f1` on a `Left`, `f2` on a `Right` (either.gala:91). -/
def eitherFold (e : GEither α β) (f1 : α → Prog σ γ) (f2 : β → Prog σ γ) : Prog σ γ :=
  match e with
  | .Left a => f1 a
  | .Right b => f2 b

/-- `Map(f)`: `f` on a `Right` (either.gala:99). -/
def eitherMap (e : GEither α β) (f : β → Prog σ γ) : Prog σ (GEither α γ) :=
  match e with
  | .Left a => pure (.Left a)
  | .Right b => .Right <$> f b

/-- `FlatMap(f)`: `f` on a `Right` (either.gala:107). -/
def eitherFlatMap (e : GEither α β) (f : β → Prog σ (GEither α γ)) : Prog σ (GEither α γ) :=
  match e with
  | .Left a => pure (.Left a)
  | .Right b => f b
end

def seqGetOption (xs : List α) (i : Int64) : GOption α :=
  if i < 0 then .None else toGOption xs[i.toInt.toNat]?

/-- The integer GALA's `Size`, `Length`, `IndexOf` and `Count` give. -/
def seqSize (xs : List α) : Int64 := Int64.ofNat xs.length

/-- The outcome line a run ends with on stderr, in galaspec's spelling. -/
def outcomeLine (o : String) : String := "gala2lean: outcome=" ++ o

/-- Run a translated program from its initial state: print its output on stdout, then the
    outcome line on stderr. The exit code is 0 for `ok`, 1 for a panic and 2 for
    divergence (which a run only reaches when the recursion's least fixed point is
    undefined; an actual infinite loop runs forever). -/
def runMain [HasOut σ] (p : Prog σ Unit) (init : σ) : IO UInt32 := do
  match (p.run.run init) with
  | some (.ok _, s) =>
    IO.print (HasOut.out s)
    IO.eprintln (outcomeLine "ok")
    pure 0
  | some (.error e, s) =>
    IO.print (HasOut.out s)
    IO.eprintln (outcomeLine ("panic:" ++ e.name))
    pure 1
  | none =>
    IO.eprintln (outcomeLine "diverge")
    pure 2

end Gala.Gen
