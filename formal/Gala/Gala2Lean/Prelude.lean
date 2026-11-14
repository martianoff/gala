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
  deriving Repr, Inhabited, BEq

def Panic.name : Panic → String
  | .divide => "divide"
  | .noArm => "match"
  | .shift => "other"

/-- The monad of a translated program: state `σ`, then a panic (which keeps the state, so
    the output printed before it survives), then divergence (`none`). -/
abbrev Prog (σ : Type) := ExceptT Panic (StateT σ Option)

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
