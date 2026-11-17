/-
  # Constant expressions

  GALA's literal arithmetic has Go's untyped-constant semantics (docs/GALA.MD: "Untyped
  constants still mix, as in Go"; docs/FORMAL_SEMANTICS.MD, "Decisions the spec forces",
  #1237). The transformer emits an operator expression whose operands are all literals as
  a Go constant expression, which Go evaluates exactly at compile time, and gives the
  result a type only where the program uses it: `(1 << 70) >> 68` is `4`, though `1 << 70`
  does not fit `int`.

  `foldConstants` is that compile-time phase. `run` (Gala/Eval.lean) runs the program it
  gives, so evaluation never sees a constant expression, only its value.

  * **A constant expression** is Go's (Go spec, "Constant expressions"), restricted to what
    GALA can write: `int`, `bool` and `string` literals combined by parentheses, the unary
    operators `-`, `+`, `^`, `!` and the binary operators, with operands of the kinds Go
    allows together. A name is never one: GALA has no `const`, and a `val` is a Go `var`.
    An expression of operands of mismatched kinds is not one; Go rejects it and evaluation
    reports it as it always did.
  * **Its value** is computed exactly, on mathematical integers (`Gala/Exact.lean`).
  * **The result** replaces each largest constant expression that has an operator: a
    literal of its value. An integer that does not fit `int` becomes
    `unsupported int-constant-overflow`, never a wrapped value: Go rejects it where it
    gives it type `int`, but Core JSON does not say which type the program gives it,
    and a constant of another type (`uint64`) may be valid. So a run that reaches one is
    inconclusive. A division by zero, a negative shift count, and a value or shift
    count beyond the model's bounds (`Exact.maxBits`, `Exact.maxShift`, within gc's)
    become `unsupported` too: Go rejects all of them at compile time.
  * **Where an error is reported.** Like any error galaspec finds, a constant Go rejects
    stops a run only when the run reaches it. Go rejects the whole program, so such a
    program is never compared with Go.
  * **A shift by a non-constant count** (`1 << n`) is not a constant expression. Its
    constant left operand takes the type it would have without the shift (Go spec,
    "Operators"), `int` wherever S1 can put it, which is the type evaluation gives it.
-/
import Gala.Syntax
import Gala.Exact

namespace Gala.Spec

open Gala.Exact

/-- The value of a constant expression. -/
inductive CVal
  | int (v : Int)
  | bool (b : Bool)
  | str (s : String)
  deriving Repr, DecidableEq

/-- A unary operator on a constant; `none` if Go does not apply it to that kind. -/
def unaryC (op : String) (v : CVal) : Option (Res CVal) :=
  match op, v with
  | "-", .int x => some (.val (.int (-x)))
  | "+", .int x => some (.val (.int x))
  | "^", .int x => some (.val (.int (bitNot x)))
  | "!", .bool b => some (.val (.bool !b))
  | _, _ => none

/-- A binary operator on two constants; `none` if Go does not apply it to those kinds. -/
def binaryC (op : String) (a b : CVal) : Option (Res CVal) :=
  match a, b with
  | .int x, .int y =>
    match op with
    | "==" => some (.val (.bool (x == y)))
    | "!=" => some (.val (.bool (x != y)))
    | "<" => some (.val (.bool (decide (x < y))))
    | "<=" => some (.val (.bool (decide (x ≤ y))))
    | ">" => some (.val (.bool (decide (x > y))))
    | ">=" => some (.val (.bool (decide (x ≥ y))))
    | _ => (intOp op x y).map fun
      | .val v => .val (.int v)
      | .err e => .err e
  | .str x, .str y =>
    match op with
    | "+" => some (.val (.str (x ++ y)))
    | "==" => some (.val (.bool (x == y)))
    | "!=" => some (.val (.bool (x != y)))
    | "<" => some (.val (.bool (decide (x < y))))
    | "<=" => some (.val (.bool (decide (x ≤ y))))
    | ">" => some (.val (.bool (decide (y < x))))
    | ">=" => some (.val (.bool (decide (y ≤ x))))
    | _ => none
  | .bool x, .bool y =>
    match op with
    | "&&" => some (.val (.bool (x && y)))
    | "||" => some (.val (.bool (x || y)))
    | "==" => some (.val (.bool (x == y)))
    | "!=" => some (.val (.bool (x != y)))
    | _ => none
  | _, _ => none

/-- The value of `e` if it is a constant expression, `none` if it is not. An error in an
    operand is the whole expression's: Go rejects the program either way. -/
def constEval : Expr → Option (Res CVal)
  | .lit (.int v) => some (match bounded v with
    | .val v => .val (.int v)
    | .err e => .err e)
  | .lit (.bool b) => some (.val (.bool b))
  | .lit (.str s) => some (.val (.str s))
  | .paren e => constEval e
  | .unary op e =>
    match constEval e with
    | some (.val v) => unaryC op v
    | some (.err k) => some (.err k)
    | none => none
  | .binary op l r =>
    match constEval l with
    | none => none
    | some a =>
      match a, constEval r with
      | .val x, some (.val y) => binaryC op x y
      | .err k, some _ | .val _, some (.err k) => some (.err k)
      | _, none => none
  | _ => none

/-- The smallest and largest `int`, Go's int64. -/
def intMin : Int := -2 ^ 63
def intMax : Int := 2 ^ 63 - 1

/-- What a constant expression is replaced by. -/
def constExpr : Res CVal → Expr
  | .val (.int v) => if intMin ≤ v ∧ v ≤ intMax then .lit (.int v) else .unsupported "int-constant-overflow"
  | .val (.bool b) => .lit (.bool b)
  | .val (.str s) => .lit (.str s)
  | .err .divByZero => .unsupported "constant-division-by-zero"
  | .err .negativeShift => .unsupported "constant-negative-shift"
  | .err .tooLarge => .unsupported "constant-too-large"

/-- An operator node `e`: its value if it is a constant expression, else `rest ()`, the
    node with its operands folded (built only then). -/
def foldOp (e : Expr) (rest : Unit → Expr) : Expr :=
  match constEval e with
  | some r => constExpr r
  | none => rest ()

mutual
/-- `e` with each largest constant expression that has an operator replaced by its value;
    a lone literal stays as it is. -/
def foldE : Expr → Expr
  | .unary op e => foldOp (.unary op e) fun _ => .unary op (foldE e)
  | .binary op l r => foldOp (.binary op l r) fun _ => .binary op (foldE l) (foldE r)
  | .paren e => foldOp (.paren e) fun _ => .paren (foldE e)
  | .call c as => .call (foldE c) (foldEs as)
  | .select t f => .select (foldE t) f
  | .tuple es => .tuple (foldEs es)
  | .ifExpr c a b => .ifExpr (foldE c) (foldE a) (foldE b)
  | .mtch s cs => .mtch (foldE s) (foldCases cs)
  | .block ss => .block (foldEs ss)
  | .ifStmt c a b => .ifStmt (foldE c) (foldE a) (foldOE b)
  | .ret v => .ret (foldOE v)
  | .assign op ts vs => .assign op (foldEs ts) (foldEs vs)
  | .incdec op t => .incdec op (foldE t)
  | .decl m ns d ty vs => .decl m ns d ty (foldEs vs)
  | .interp es => .interp (foldEs es)
  | .lambda ps r b => .lambda ps r (foldE b)
  | .named n v => .named n (foldE v)
  | e@(.lit _) | e@(.name _) | e@(.unsupported _) => e
  termination_by structural e => e
def foldEs : List Expr → List Expr
  | [] => []
  | e :: es => foldE e :: foldEs es
  termination_by structural es => es
def foldOE : Option Expr → Option Expr
  | none => none
  | some e => some (foldE e)
  termination_by structural o => o
def foldCases : List (Pat × Option Expr × Expr) → List (Pat × Option Expr × Expr)
  | [] => []
  | (p, g, b) :: cs => (p, foldOE g, foldE b) :: foldCases cs
  termination_by structural cs => cs
end

def foldField (f : Field) : Field := { f with default := foldOE f.default }

def foldFunc (f : Func) : Func := { f with defaults := f.defaults.map foldOE, body := foldE f.body }

def foldVariant (v : Variant) : Variant := { v with fields := v.fields.map foldField }

def foldDecl : Decl → Decl
  | .func f => .func (foldFunc f)
  | .struct_ n sh fs => .struct_ n sh (fs.map foldField)
  | .sealed n vs => .sealed n (vs.map foldVariant)
  | .global m ns d ty vs => .global m ns d ty (foldEs vs)
  | .unsupported k => .unsupported k

/-- The program after Go's compile-time evaluation of its constant expressions. -/
def foldConstants (p : Program) : Program :=
  { files := p.files.map fun f => { f with decls := f.decls.map foldDecl } }

-- The two programs of #1237, and Go's answers for them.
#guard foldE (.binary ">>" (.paren (.binary "<<" (.lit (.int 1)) (.lit (.int 70)))) (.lit (.int 68)))
  matches .lit (.int 4)
#guard foldE (.binary "/" (.binary "*" (.lit (.int 9223372036854775807)) (.lit (.int 2))) (.lit (.int 4)))
  matches .lit (.int 4611686018427387903)
#guard foldE (.binary "+" (.name "x") (.binary "/" (.lit (.int 1)) (.lit (.int 0))))
  matches .binary "+" (.name "x") (.unsupported "constant-division-by-zero")
#guard foldE (.unary "-" (.lit (.int 9223372036854775808))) matches .lit (.int (-9223372036854775808))
#guard foldE (.binary "+" (.lit (.int 1)) (.lit (.str "a"))) matches .binary "+" (.lit (.int 1)) (.lit (.str "a"))

end Gala.Spec
