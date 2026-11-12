/-
  # Big-step semantics of GALA subset S1

  An executable, fuel-bounded evaluator. It is the definition of what an S1 program
  means: what it prints, and how it ends.

  * **Outcomes.** A run ends `ok`, in a `panic` of a class (`match`, `divide`, `index`,
    `other`), or out of `fuel`. Errors are values (`Stop`), never crashes. A construct
    outside S1 ends the run as `unsupported`, when execution reaches it.
  * **Fuel.** Every evaluation step costs one unit; a run that uses them all ends `fuel`,
    which says nothing about the program.
  * **Evaluation order** (docs/GALA.MD, "Evaluation Order"; docs/FORMAL_SEMANTICS.MD,
    "Decisions the spec forces"): strictly left to right. Operands, call arguments,
    tuple elements, constructor arguments and the parts of an interpolated string `s"..."`
    are evaluated in source order, each one
    completely before the next. `&&` and `||` evaluate their right operand only when it
    decides the result. A compound assignment `x op= e` reads `x`, then evaluates `e`.
  * **Named arguments and defaults** (S2; `bindArgs`): a call's arguments are evaluated
    as written, then the defaults of the parameters it leaves out, in parameter order,
    each in the callee's scope.
  * **`match`** (docs/GALA.MD, "Match Expression"), with Scala's semantics: the subject is
    evaluated once; arms are tried in source order; a pattern's variables are in scope in
    its own guard and body only; sub-patterns are tried left to right and the first one
    that fails stops the rest; a false guard falls through to the next arm; no matching
    arm is `panic match`.
  * **Integers** are Go's `int` on a 64-bit target: int64, wrapping on overflow. `/` and
    `%` truncate toward zero, as in Go; dividing by zero is `panic divide`. Shifts follow
    Go: a count of 64 or more shifts every bit out, a negative count panics.
  * **Package-level `val`/`var`** are initialized before `main` in Go's order: repeatedly
    the earliest declaration whose initializer refers (directly, or through the functions
    it names) to no uninitialized one.

  Static checks are the type checker's job, not this file's: a program GALA rejects can
  evaluate to anything here. Where evaluation meets something a type-correct S1 program
  cannot produce, it stops as `stuck` (reported as unsupported), so such a run is never counted as
  agreement.
-/
import Gala.Scope
import Gala.Print

namespace Gala.Spec

/-- The class of a panic, so a run can be compared with a Go panic. -/
inductive PanicClass
  /-- No `match` arm matched. -/
  | match_
  /-- Integer division or modulo by zero (Go: "integer divide by zero"). -/
  | divide
  /-- An index out of range (no S1 construct raises it yet). -/
  | index
  /-- Any other panic (Go: "negative shift amount"). -/
  | other
  deriving Repr, BEq, DecidableEq

/-- Why evaluation stopped before its end. `ret` is a `return` on its way to its call. -/
inductive Stop
  | panic (c : PanicClass) (msg : String)
  | fuel
  | unsupported (kind : String) (detail : String)
  /-- Evaluation met something no type-correct S1 program does (a type error, a wrong
      arity): the program is one GALA rejects, or the spec has a gap. -/
  | stuck (detail : String)
  | ret (v : Val)
  deriving Repr

/-- The evaluation state: the locals of the running function, the package-level
    variables, the output so far, and the steps left. -/
structure St where
  locals : Env
  globals : Env
  out : String
  steps : Nat
  /-- The cells of the local `var`s, by `Val.ref` index. A cell outlives its scope, so a
      closure that captured it shares it (S2). -/
  heap : Array Val := #[]

/-- Evaluation: state that survives a `Stop`, so output printed before a panic stays. -/
abbrev M := ExceptT Stop (StateM St)

/-- How a run ends. -/
inductive Outcome
  | ok
  | panic (c : PanicClass) (msg : String)
  | fuel
  | unsupported (kind : String) (detail : String)
  | stuck (detail : String)
  deriving Repr

/-! ## Integers -/

def minInt : Int := Int64.minValue.toInt
def maxInt : Int := Int64.maxValue.toInt

/-- Reduce to Go's int64, wrapping around. -/
def wrap (x : Int) : Int := x.bmod Int64.size

private def bits (f : Int64 → Int64 → Int64) (a b : Int) : Int :=
  (f (Int64.ofInt a) (Int64.ofInt b)).toInt

/-! ## Helpers -/

private def stuck (msg : String) : M α := throw (.stuck msg)
private def unsup (kind : String) (detail : String := "") : M α :=
  throw (.unsupported kind detail)

/-- Spend one step. -/
def tick : M Unit := do
  let s ← get
  match s.steps with
  | 0 => throw .fuel
  | n + 1 => set { s with steps := n }

/-- The value a binding holds: a local `var`'s is in its cell. -/
def deref : Val → M Val
  | .ref n => do
    match (← get).heap[n]? with
    | some v => pure v
    | none => stuck "a reference to no cell"
  | v => pure v

/-- A new cell holding `v`. -/
def alloc (v : Val) : M Val :=
  modifyGet fun s => (.ref s.heap.size, { s with heap := s.heap.push v })

/-- Store `v` in cell `n`. -/
def store (n : Nat) (v : Val) : M Unit := do
  unless n < (← get).heap.size do stuck "a reference to no cell"
  modify fun s => { s with heap := s.heap.set! n v }

/-- Is a value a function: a lambda's closure or a top-level function? -/
def isFn : Val → Bool
  | .closure .. | .fn _ => true
  | _ => false

/-- Structural equality: `==` on S1 values. Structs, tuples and sealed values compare
    by value (docs/GALA.MD, "Equality"); a sealed value by its case and that case's
    fields only. -/
def valEq : Val → Val → Bool
  | .int a, .int b => a == b
  | .bool a, .bool b => a == b
  | .str a, .str b => a == b
  | .unit, .unit => true
  | .tuple as, .tuple bs => valEqs as bs
  | .struct_ t as, .struct_ u bs => t == u && valEqs as bs
  | .sealed t c as, .sealed u d bs => t == u && c == d && valEqs as bs
  | _, _ => false
where
  valEqs : List Val → List Val → Bool
    | [], [] => true
    | a :: as, b :: bs => valEq a b && valEqs as bs
    | _, _ => false

mutual
/-- The S1 type a type annotation names: `int` (and `int64`, the same type on a 64-bit
    target), `bool`, `string`, a struct or a sealed type of the program. -/
def checkTy (ds : Decls) : Ty → M String
  | .unsupported k => unsup k
  | .name n =>
    if n ∈ ["int", "int64", "bool", "string"] || (ds.structs.lookup n).isSome
        || (ds.sealeds.lookup n).isSome then pure n
    else unsup s!"type-{n}"
  -- A function type (S2): its parts must be types of the subset. Its zero value is
  -- `nil`, which the subset does not model.
  | .func ps none => do checkTys ds ps; pure "func"
  | .func ps (some r) => do checkTys ds ps; discard (checkTy ds r); pure "func"
/-- Check each type of a list. -/
def checkTys (ds : Decls) : List Ty → M Unit
  | [] => pure ()
  | t :: ts => do discard (checkTy ds t); checkTys ds ts
end

/-- The S1 type of a field or parameter, which must state one. -/
def checkOptTy (ds : Decls) (what : String) : Option Ty → M String
  | some t => checkTy ds t
  | none => stuck s!"{what} has no type"

/-- The default value of a type (docs/GALA.MD, "Default Values"): Go's zero value for
    `int`, `bool`, `string`; each field's default for a struct; the default case for a
    sealed type, which must have one. `n` bounds the nesting of structs. -/
def zero (ds : Decls) : Nat → String → M Val
  | 0, _ => stuck "type nests too deep"
  | _ + 1, "int" | _ + 1, "int64" => pure (.int 0)
  | _ + 1, "bool" => pure (.bool false)
  | _ + 1, "func" => unsup "nil"
  | _ + 1, "string" => pure (.str "")
  | n + 1, t =>
    let zeroFields (fs : List Field) : M (List Val) :=
      fs.mapM fun f => do zero ds n (← checkOptTy ds s!"field {f.name}" f.type)
    match ds.structs.lookup t with
    | some fs => do pure (.struct_ t (← zeroFields fs))
    | none =>
    match ds.sealeds.lookup t with
    | some vs =>
      match vs.find? (·.isDefault) with
      | some v => do pure (.sealed t v.name (← zeroFields v.fields))
      | none => stuck s!"sealed type {t} has no default case"
    | none => unsup s!"type-{t}"

/-- A unary operator on a value. -/
def unop (op : String) (v : Val) : M Val :=
  match op, v with
  | "-", .int n => pure (.int (wrap (-n)))
  | "+", .int n => pure (.int n)
  | "^", .int n => pure (.int (wrap (-n - 1)))
  | "!", .bool b => pure (.bool (!b))
  | _, _ => stuck s!"unary {op} on {repr v}"

/-- A binary operator other than `&&` and `||`, on evaluated operands. -/
def binop (op : String) (a b : Val) : M Val :=
  match op, a, b with
  | "==", x, y => pure (.bool (valEq x y))
  | "!=", x, y => pure (.bool (!valEq x y))
  | "+", .int x, .int y => pure (.int (wrap (x + y)))
  | "-", .int x, .int y => pure (.int (wrap (x - y)))
  | "*", .int x, .int y => pure (.int (wrap (x * y)))
  | "/", .int x, .int y =>
    if y == 0 then throw (.panic .divide "runtime error: integer divide by zero")
    else pure (.int (wrap (x.tdiv y)))
  | "%", .int x, .int y =>
    if y == 0 then throw (.panic .divide "runtime error: integer divide by zero")
    else pure (.int (wrap (x.tmod y)))
  | "<<", .int x, .int y =>
    if y < 0 then throw (.panic .other "runtime error: negative shift amount")
    else if y ≥ 64 then pure (.int 0)
    else pure (.int (wrap (x * 2 ^ y.toNat)))
  | ">>", .int x, .int y =>
    if y < 0 then throw (.panic .other "runtime error: negative shift amount")
    else if y ≥ 64 then pure (.int (if x < 0 then -1 else 0))
    else pure (.int (x / 2 ^ y.toNat))
  | "&", .int x, .int y => pure (.int (bits (· &&& ·) x y))
  | "|", .int x, .int y => pure (.int (bits (· ||| ·) x y))
  | "^", .int x, .int y => pure (.int (bits (· ^^^ ·) x y))
  | "&^", .int x, .int y => pure (.int (bits (fun p q => p &&& ~~~q) x y))
  | "<", .int x, .int y => pure (.bool (decide (x < y)))
  | "<=", .int x, .int y => pure (.bool (decide (x ≤ y)))
  | ">", .int x, .int y => pure (.bool (decide (x > y)))
  | ">=", .int x, .int y => pure (.bool (decide (x ≥ y)))
  | "+", .str x, .str y => pure (.str (x ++ y))
  | "<", .str x, .str y => pure (.bool (decide (x < y)))
  | "<=", .str x, .str y => pure (.bool (decide (x ≤ y)))
  | ">", .str x, .str y => pure (.bool (decide (y < x)))
  | ">=", .str x, .str y => pure (.bool (decide (y ≤ x)))
  | _, _, _ => stuck s!"binary {op} on {repr a} and {repr b}"

/-- The value of an integer literal, which must fit `int`. -/
def intLit (n : Int) : M Val :=
  if minInt ≤ n ∧ n ≤ maxInt then pure (.int n)
  else unsup "int-constant-overflow" (toString n)

/-- A name that resolves to nothing. With top-level declarations outside S1 (an
    `import`, a method), it most likely names one of them, and the run is unsupported
    for that reason; otherwise it is a builtin S1 does not model. -/
def unresolved (ds : Decls) (x : String) : M α :=
  match ds.unsupported with
  | k :: _ => unsup k s!"name {x}"
  | [] => unsup "unresolved-name" x

/-- Something S1 cannot give a meaning to: with top-level declarations outside S1, most
    likely one of them (a custom extractor needs a method), else a stuck program. -/
def outsideS1 (ds : Decls) (detail : String) : M α :=
  match ds.unsupported with
  | k :: _ => unsup k detail
  | [] => stuck detail

/-- Read field `f` of a value. A sealed value's field that its active case lacks reads as
    the zero value of the field's type, as in the Go struct that holds every case. -/
def getField (ds : Decls) (v : Val) (f : String) : M Val :=
  match v with
  | .struct_ ty fs =>
    match ((ds.structs.lookup ty).getD []).findIdx? (·.name == f) with
    | some i => match fs[i]? with
      | some x => pure x
      | none => stuck "struct value with missing fields"
    | none => match ds.unsupported with
      | k :: _ => unsup k s!"member {f}"
      | [] => unsup "method-value" f
  | .sealed ty c fs =>
    match (ds.caseFields ty c).findIdx? (·.name == f) with
    | some i => match fs[i]? with
      | some x => pure x
      | none => stuck "sealed value with missing fields"
    | none =>
      let other := ((ds.sealeds.lookup ty).getD []).findSome? fun v =>
        v.fields.find? (·.name == f)
      match other.bind (·.type) with
      | some t => do zero ds 64 (← checkTy ds t)
      | none => unsup "method-value" f
  | .tuple vs =>
    -- A tuple's fields are V1, V2, ... (docs/GALA.MD, "Tuple").
    match if f.startsWith "V" then (f.drop 1).toNat?.bind (fun i => if i = 0 then none else vs[i - 1]?) else none with
    | some x => pure x
    | none => unsup "method-value" f
  | _ => unsup "method-value" f

/-- Set field `f` of a struct value. -/
def setField (ds : Decls) (v : Val) (f : String) (x : Val) : M Val :=
  match v with
  | .struct_ ty fs =>
    match ((ds.structs.lookup ty).getD []).findIdx? (·.name == f) with
    | some i => pure (.struct_ ty (fs.set i x))
    | none => stuck s!"no field {f}"
  | _ => stuck s!"assignment to field {f} of a non-struct"

/-- The value a place (a variable, or a field path of one) holds. -/
def readPlace (ds : Decls) : Expr → M Val
  | .name x => do
    let s ← get
    match lookupVar s.locals s.globals x with
    | some b => deref b.val
    | none => stuck s!"assignment to {x}, which is not a variable"
  | .select t f => do getField ds (← readPlace ds t) f
  | .paren e => readPlace ds e
  | _ => stuck "assignment to something that is not a variable or field"

/-- Store `v` in a place. -/
def assignTo (ds : Decls) : Expr → Val → M Unit
  | .name x, v => do
    let s ← get
    -- A local `var` is assigned in its cell, which closures that captured it share.
    if let some ⟨.ref n, _⟩ := lookup s.locals x then store n v else
    match update s.locals x v with
    | some l => set { s with locals := l }
    | none => match update s.globals x v with
      | some g => set { s with globals := g }
      | none => stuck s!"assignment to {x}, which is not a variable"
  | .select t f, v => do assignTo ds t (← setField ds (← readPlace ds t) f v)
  | .paren e, v => assignTo ds e v
  | _, _ => stuck "assignment to something that is not a variable or field"

/-! ## Patterns -/

/-- Does a literal pattern match a value? -/
def litMatches (l : Lit) (v : Val) : M Bool :=
  match l, v with
  | .int n, .int m => pure (n == m)
  | .bool a, .bool b => pure (a == b)
  | .str a, .str b => pure (a == b)
  | _, _ => stuck "literal pattern of another type than its subject"

mutual
/-- Match a value against a pattern: the pattern's bindings in order, or `none`. Bare
    names are classified by `classifyPatName` (Gala/Scope.lean). -/
def matchPat (ds : Decls) (locals globals : Env) : Pat → Val → M (Option (List (String × Val)))
  | .wild, _ => pure (some [])
  | .unsupported k, _ => unsup k
  | .lit l, v => do pure (if ← litMatches l v then some [] else none)
  | .name x, v =>
    let cases := match v with
      | .sealed ty _ _ => (ds.sealeds.lookup ty).getD []
      | _ => []
    match classifyPatName cases locals globals x with
    | .variant c =>
      if !c.fields.isEmpty then stuck s!"bare pattern {x} for a case with fields"
      else match v with
        | .sealed _ c' _ => pure (if c' == c.name then some [] else none)
        | _ => pure none
    | .stable b => do pure (if valEq (← deref b.val) v then some [] else none)
    | .binder => pure (some [(x, v)])
  | .tuple ps, v =>
    match v with
    | .tuple vs => if ps.length == vs.length then matchPats ds locals globals ps vs
      else stuck "tuple pattern of another arity than its subject"
    | _ => stuck "tuple pattern on a non-tuple"
  | .extract c ps, v =>
    match v with
    | .sealed ty c' fs =>
      match ((ds.sealeds.lookup ty).getD []).find? (·.name == c) with
      | none => outsideS1 ds s!"{c} is not a case of {ty}"
      | some cv =>
        if c' != c then pure none
        else if cv.fields.length != ps.length then stuck s!"pattern {c} has the wrong arity"
        else matchPats ds locals globals ps fs
    | .struct_ ty fs =>
      if c != ty then outsideS1 ds s!"extractor {c} on a {ty}"
      else if fs.length != ps.length then stuck s!"pattern {c} has the wrong arity"
      else matchPats ds locals globals ps fs
    | _ => outsideS1 ds s!"extractor {c} on a value that is not a struct or sealed value"
/-- Match sub-patterns left to right; the first that fails stops the rest. -/
def matchPats (ds : Decls) (locals globals : Env) :
    List Pat → List Val → M (Option (List (String × Val)))
  | p :: ps, v :: vs => do
    match ← matchPat ds locals globals p v with
    | none => pure none
    | some b => match ← matchPats ds locals globals ps vs with
      | none => pure none
      | some bs => pure (some (b ++ bs))
  | _, _ => pure (some [])
end

/-! ## Expressions and statements -/

private def asBool : Val → M Bool
  | .bool b => pure b
  | v => stuck s!"condition is not a bool: {repr v}"

/-- Bind names in the current scope. A `var` gets a cell of its own (S2: closures share it). -/
private def pushAll (bs : List (String × Val)) (mutable : Bool) : M Unit := do
  for (x, v) in bs do
    let b ← if mutable then do pure ⟨← alloc v, true⟩ else pure ⟨v, false⟩
    modify fun s => { s with locals := bind s.locals x b }

/-- The locals a lambda closes over, as it captures them: a `var` by its cell. -/
def captured (ρ : Env) : List (String × Val) := ρ.map fun (k, b) => (k, b.val)

/-- A closure's captured locals as a scope again: a cell is a `var`. -/
def capturedEnv (env : List (String × Val)) : Env :=
  env.map fun (k, v) => (k, ⟨v, match v with | .ref _ => true | _ => false⟩)

/-- Is an argument a named one, `name = value`? -/
def isNamed : Expr → Bool
  | .named .. => true
  | _ => false

/-- An argument's value expression. -/
def unnamed : Expr → Expr
  | .named _ v => v
  | e => e

/-- The scope a function's default value is evaluated in: the parameters before it, bound
    to their values, and nothing of the caller's (docs/GALA.MD, "Default Parameter Values"). -/
def defaultFrame (earlier : List (String × Val)) : Env :=
  earlier.foldl (fun ρ (x, v) => bind ρ x ⟨v, false⟩) []

/-- A by-name argument (docs/GALA.MD, "By-Name Arguments"): a plain expression passed to a
    parameter of type `func() T` is lifted into `() => expr`. The subset passes lambdas and
    names of function values only; anything else is outside it. A parameter whose type is
    left out (a lambda's, inferred) may be such a parameter too, so only a lambda, a name
    or a literal is passed to it. -/
private def byNameOk (ds : Decls) (paramTy : Option Ty) (arg : Expr) : M Bool := do
  let s ← get
  let fnName : Expr → M Bool
    | .name x => match resolve ds s.locals s.globals x with
      | .func _ => pure true
      | .var b => do pure (isFn (← deref b.val))
      | _ => pure false
    | _ => pure false
  match paramTy, arg with
  | some (.func [] _), .lambda .. => pure true
  | some (.func [] _), a => fnName a
  | none, .lambda .. | none, .name _ | none, .lit _ => pure true
  | none, _ => pure false
  | some _, _ => pure true

private def popTo (depth : Nat) : M Unit :=
  modify fun s => { s with locals := exitScope s.locals depth }

/-- The text of a value printed, or why it cannot be. -/
private def printed (r : Print.P String) (ds : Decls) : M String := do
  match r with
  | .ok text => pure text
  | .error k => match ds.unsupported.find? (· == "method") with
    | some k' => unsup k' "printing"
    | none => unsup k

private def emit (r : Print.P String) (ds : Decls) : M Unit := do
  let text ← printed r ds
  modify fun s => { s with out := s.out ++ text }

/-- A program's own `String` method changes how its values print, so with methods
    outside S1 only basic values are printed. -/
private def printable (ds : Decls) (vs : List Val) : M Unit :=
  if ds.unsupported.contains "method" &&
      vs.any (fun | .int _ | .bool _ | .str _ => false | _ => true) then
    unsup "method" "printing a value whose type may have methods"
  else pure ()

/-- The fields of a struct or case built from evaluated arguments, one per field. -/
private def construct (ds : Decls) (name : String) (fs : List Field) (vs : List Val) :
    M (List Val) := do
  for f in fs do discard (checkOptTy ds s!"field {f.name}" f.type)
  if vs.length != fs.length then stuck s!"{name} takes {fs.length} fields"
  pure vs

mutual
/-- Evaluate an expression or statement. A statement's value is `unit`. `d` bounds the
    nesting of evaluation, `St.steps` the total work. -/
def eval (ds : Decls) : Nat → Expr → M Val
  | 0, _ => throw .fuel
  | d + 1, e => do
    tick
    match e with
    | .lit (.int n) => intLit n
    | .lit (.bool b) => pure (.bool b)
    | .lit (.str s) => pure (.str s)
    | .unsupported k => unsup k
    | .paren x => eval ds d x
    | .name x =>
      let s ← get
      match resolve ds s.locals s.globals x with
      | .var b => deref b.val
      | .variant ty c =>
        if c.fields.isEmpty then pure (.sealed ty c.name []) else unsup "func-value" x
      | .func f => pure (.fn f)
      | .unbound => unresolved ds x
      | _ => unsup "func-value" x
    | .named .. => stuck "a named argument outside a call's arguments"
    | .lambda ps _ body => do
      -- A lambda closes over the locals in scope: a `var` by its cell (by reference).
      pure (.closure ps body (captured (← get).locals))
    | .unary "-" (.lit (.int n)) => intLit (-n)
    | .unary op x => do unop op (← eval ds d x)
    | .binary "&&" l r => do
      if ← asBool (← eval ds d l) then pure (.bool (← asBool (← eval ds d r)))
      else pure (.bool false)
    | .binary "||" l r => do
      if ← asBool (← eval ds d l) then pure (.bool true)
      else pure (.bool (← asBool (← eval ds d r)))
    | .binary op l r => do
      let a ← eval ds d l
      let b ← eval ds d r
      binop op a b
    | .tuple es => do pure (.tuple (← evalList ds d es))
    | .interp parts => do
      let vs ← evalList ds d parts
      printable ds vs
      pure (.str (← printed (Print.interp ds vs) ds))
    | .select t f => do getField ds (← eval ds d t) f
    | .call (.name x) args => do
      let s ← get
      match resolve ds s.locals s.globals x with
      | .func f => do callFunc ds d f (← funcArgs ds d f args)
      | .struct_ n fs => do pure (.struct_ n (← construct ds n fs (← fieldArgs ds d fs args)))
      | .variant ty c => do
        pure (.sealed ty c.name (← construct ds c.name c.fields (← fieldArgs ds d c.fields args)))
      | .print nl => do
        if args.any isNamed then stuck "a named argument to Println or Print"
        let vs ← evalList ds d args
        printable ds vs
        emit (if nl then Print.println ds vs else Print.print ds vs) ds
        pure .unit
      | .unbound => unresolved ds x
      | .var b => do callValue ds d (← deref b.val) args
    | .call (.select t m) args => do
      -- A field of function type called (`p.F(x)`); a method or package member is
      -- outside the subset.
      let s ← get
      let isVar := match t with
        | .name x => (lookupVar s.locals s.globals x).isSome
        | _ => true
      if !isVar then unsup "method-call" m
      let tv ← eval ds d t
      let hasField := match tv with
        | .struct_ ty _ => ((ds.structs.lookup ty).getD []).any (·.name == m)
        | .sealed ty c _ => (ds.caseFields ty c).any (·.name == m)
        | _ => false
      if !hasField then unsup "method-call" m
      callValue ds d (← getField ds tv m) args
    | .call (.unsupported k) _ => unsup k
    | .call callee args => do callValue ds d (← eval ds d callee) args
    | .ifExpr c a b | .ifStmt c a (some b) => do
      if ← asBool (← eval ds d c) then eval ds d a else eval ds d b
    | .ifStmt c a none => do
      if ← asBool (← eval ds d c) then discard (eval ds d a)
      pure .unit
    | .block stmts => do
      let depth := (← get).locals.length
      let v ← evalBlock ds d stmts
      popTo depth
      pure v
    | .mtch subj cases => do evalCases ds d (← eval ds d subj) cases
    | .ret none => throw (.ret .unit)
    | .ret (some x) => do throw (.ret (← eval ds d x))
    | .decl m names destr ty values => do
      let bs ← declare ds d names destr ty values
      pushAll bs (m == .var)
      pure .unit
    | .assign "=" targets values => do
      let vs ← evalList ds d values
      if vs.length != targets.length then stuck "assignment count mismatch"
      for (t, v) in targets.zip vs do assignTo ds t v
      pure .unit
    | .assign op [t] [x] => do
      let cur ← readPlace ds t
      let v ← eval ds d x
      let bop := match op with
        | "+=" => "+" | "-=" => "-" | "*=" => "*" | "/=" => "/" | o => o
      assignTo ds t (← binop bop cur v)
      pure .unit
    | .assign _ _ _ => stuck "compound assignment to several targets"
    | .incdec op t => do
      let cur ← readPlace ds t
      assignTo ds t (← binop (if op == "++" then "+" else "-") cur (.int 1))
      pure .unit
  termination_by d _ => (d, 0, 0)
/-- Evaluate expressions left to right. -/
def evalList (ds : Decls) : Nat → List Expr → M (List Val)
  | _, [] => pure []
  | d, e :: es => do
    let v ← eval ds d e
    pure (v :: (← evalList ds d es))
  termination_by d es => (d, 1, es.length)
/-- Bind a call's arguments to parameters (or a constructor's to fields), in parameter
    order (docs/GALA.MD, "Named Arguments", "Default Parameter Values", "Evaluation
    Order"; docs/FORMAL_SEMANTICS.MD, "Decisions the spec forces"):

    1. the arguments are evaluated in the order they are written, a named one included,
       and each goes to the parameter at its position or the one it names;
    2. then each parameter the call leaves out takes its default, in parameter order,
       evaluated in the callee's scope: a function's default sees the parameters before
       it, bound to the values the call gives them (explicitly or by their own
       defaults), and never the caller's locals; a field's default sees no field
       (`fieldScope`, GALA-E0098), only package-level names.

    A plain expression for a `func() T` parameter is a by-name argument (`byNameOk`). -/
def bindArgs (ds : Decls) : Nat → List String → List (Option Ty) → List (Option Expr) → Bool →
    List Expr → M (List Val)
  | d, names, tys, dflts, fieldScope, args => do
    -- Each argument's parameter: its position, or the one it names.
    let mut slots : List Nat := []
    let mut named := false
    for a in args do
      match a with
      | .named n _ =>
        named := true
        match names.idxOf? n with
        | some j => slots := slots ++ [j]
        | none => stuck s!"no parameter {n}"
      | _ =>
        -- Constructors reject it; a function call passes it to the next free position.
        -- docs/GALA.MD does not say which, so the subset leaves it out.
        if named then unsup "positional-after-named"
        slots := slots ++ [slots.length]
    if slots.eraseDups.length != slots.length || slots.any (· ≥ names.length) then
      stuck "an argument given twice, or too many"
    for (a, j) in args.zip slots do
      unless ← byNameOk ds (tys.getD j none) (unnamed a) do
        unsup "by-name-arg" "a plain expression passed as a func() T"
    let given := slots.zip (← evalList ds d (args.map unnamed))
    let mut vals : List Val := []
    for (n, j) in names.zipIdx do
      match given.lookup j, dflts.getD j none with
      | some v, _ => vals := vals ++ [v]
      | none, some e =>
        -- A default for a `func() T` parameter is a by-name argument too.
        unless ← byNameOk ds (tys.getD j none) e do
          unsup "by-name-arg" "a plain expression as the default of a func() T"
        let frame := if fieldScope then [] else defaultFrame (names.zip vals)
        vals := vals ++ [← runIn ds d frame e]
      | none, none => stuck s!"no argument for {n}"
    pure vals
  termination_by d _ _ _ _ _ => (d, 2, 0)
/-- A function's arguments, bound to its parameters. -/
def funcArgs (ds : Decls) : Nat → Func → List Expr → M (List Val)
  | d, f, args => bindArgs ds d (f.params.map (·.name)) (f.params.map (·.type)) f.defaults false args
  termination_by d _ _ => (d, 3, 0)
/-- A constructor's arguments, bound to the fields. -/
def fieldArgs (ds : Decls) : Nat → List Field → List Expr → M (List Val)
  | d, fs, args => bindArgs ds d (fs.map (·.name)) (fs.map (·.type)) (fs.map (·.default)) true args
  termination_by d _ _ => (d, 3, 0)
/-- Call a function value on argument expressions: the callee is evaluated first, then
    the arguments left to right (docs/GALA.MD, "Evaluation Order"). -/
def callValue (ds : Decls) : Nat → Val → List Expr → M Val
  -- A function value (a top-level function's included) is a Go func value: it takes its
  -- arguments by position, all of them, as GALA type-checks it.
  | d, .fn f, args => do
    if args.any isNamed then unsup "named-arg" "a named argument to a function value"
    callFunc ds d f (← bindArgs ds d (f.params.map (·.name)) (f.params.map (·.type)) [] false args)
  | d, .closure ps body env, args => do
    if args.any isNamed then unsup "named-arg" "a named argument to a function value"
    let vs ← bindArgs ds d (ps.map (·.name)) (ps.map (·.type)) [] false args
    -- The body sees the captured locals and its parameters, never the caller's. A block
    -- body's trailing value is the result: in a void lambda, GALA's types never use it.
    runIn ds d ((ps.zip vs).foldl (fun ρ (p, v) => bind ρ p.name ⟨v, false⟩) (capturedEnv env)) body
  -- A struct called as a function uses its `Apply` method, outside the subset.
  | _, v, _ => outsideS1 ds s!"call of a value that is not a function: {repr v}"
  termination_by d _ _ => (d, 4, 0)
/-- Run a block's statements in order; its value is the last one's. -/
def evalBlock (ds : Decls) : Nat → List Expr → M Val
  | _, [] => pure .unit
  | d, [e] => eval ds d e
  | d, e :: es => do
    discard (eval ds d e)
    evalBlock ds d es
  termination_by d es => (d, 1, es.length)
/-- Try the arms of a `match` on the value `v`, in order. -/
def evalCases (ds : Decls) : Nat → Val → List (Pat × Option Expr × Expr) → M Val
  | _, _, [] => throw (.panic .match_ "no match arm matched")
  | d, v, (p, g, body) :: rest => do
    let s ← get
    match ← matchPat ds s.locals s.globals p v with
    | none => evalCases ds d v rest
    | some bs =>
      let depth := s.locals.length
      pushAll bs false
      let pass ← match g with
        | none => pure true
        | some ge => do asBool (← eval ds d ge)
      if pass then
        let r ← eval ds d body
        popTo depth
        pure r
      else
        popTo depth
        evalCases ds d v rest
  termination_by d _ cs => (d, 1, cs.length)
/-- The bindings a `val`/`var` declaration makes, in order. -/
def declare (ds : Decls) : Nat → List String → Bool → Option Ty → List Expr →
    M (List (String × Val))
  | d, names, destr, ty, values => do
    let tyName ← match ty with
      | some t => some <$> checkTy ds t
      | none => pure none
    if destr then
      match values with
      | [x] => match ← eval ds d x with
        | .tuple vs =>
          if vs.length == names.length then pure (names.zip vs)
          else stuck "destructuring of another arity"
        | _ => stuck "destructuring a non-tuple"
      | _ => stuck "destructuring needs one value"
    else if values.isEmpty then
      match tyName with
      | some t => do
        let z ← zero ds 64 t
        pure (names.map (·, z))
      | none => stuck "declaration without a type or value"
    else
      let vs ← evalList ds d values
      if vs.length == names.length then pure (names.zip vs)
      else stuck "declaration count mismatch"
  termination_by d _ _ _ _ => (d, 2, 0)
/-- Call a function on evaluated arguments: a fresh scope with only its parameters; a
    `return` ends the call with its value. -/
def callFunc (ds : Decls) : Nat → Func → List Val → M Val
  | d, f, args => do
    if args.length != f.params.length then stuck s!"{f.name} takes {f.params.length} arguments"
    for p in f.params do discard (checkOptTy ds s!"parameter {p.name}" p.type)
    if let some t := f.result then discard (checkTy ds t)
    let r ← runIn ds d ((f.params.zip args).foldl (fun ρ (p, v) => bind ρ p.name ⟨v, false⟩) []) f.body
    match f.result with
    | none => pure .unit
    | some _ => match r with
      | .unit => stuck s!"{f.name} ended without a value"
      | v => pure v
  termination_by d _ _ => (d, 2, 0)
/-- Run a function's body in its own scope `frame`: a `return` ends it with its value, and
    the caller's locals are back afterwards. -/
def runIn (ds : Decls) : Nat → Env → Expr → M Val
  | d, frame, body => do
    let saved := (← get).locals
    modify fun s => { s with locals := frame }
    let r ← tryCatch (eval ds d body) fun
      | .ret v => pure v
      | e => throw e
    modify fun s => { s with locals := saved }
    pure r
  termination_by d _ _ => (d, 1, 0)
end

/-! ## Programs -/

/-- The bare names a pattern mentions: each may bind, or (capitalized) compare with a
    package-level value. -/
partial def patNames : Pat → List String
  | .name x => [x]
  | .tuple ps | .extract _ ps => ps.flatMap patNames
  | _ => []

/-- The default values a call of `x` with arguments `as` leaves out, each with the names
    in its scope: a function's earlier parameters, or none for a field's default. -/
def omittedDefaults (ds : Decls) (x : String) (as : List Expr) : List (List String × Expr) :=
  let given (names : List String) : List Nat :=
    (List.range (as.filter (!isNamed ·)).length) ++
      as.filterMap fun | .named n _ => names.idxOf? n | _ => none
  let omitted (names : List String) (dflts : List (Option Expr)) (scope : Nat → List String) :=
    (dflts.zipIdx).filterMap fun (e, j) =>
      if (given names).contains j then none else e.map (scope j, ·)
  match ds.funcs.lookup x with
  | some f =>
    let ps := f.params.map (·.name)
    omitted ps f.defaults (ps.take ·)
  | none =>
    let fields : Option (List Field) := match ds.structs.lookup x with
      | some fs => some fs
      | none => (ds.variant? x).map (·.2.fields)
    match fields with
    | some fs => omitted (fs.map (·.name)) (fs.map (·.default)) fun _ => []
    | none => []

/-- The free names an expression mentions, for the initialization order of package-level
    variables: names bound by an enclosing declaration, parameter or pattern in `bound`
    are left out, as Go's order follows references only. A pattern name counts as a
    mention too, since it may be a stable identifier. -/
partial def mentions (ds : Decls) (bound : List String) : Expr → List String
  | .lit _ | .unsupported _ | .ret none => []
  | .name x => if bound.contains x then [] else [x]
  | .unary _ e | .paren e | .select e _ | .incdec _ e | .ret (some e) => mentions ds bound e
  | .binary _ l r => mentions ds bound l ++ mentions ds bound r
  | .call c as => mentions ds bound c ++ as.flatMap (mentions ds bound) ++
    -- The defaults a call leaves out are evaluated there (each in its callee's scope).
    match c with
    | .name x => if bound.contains x then [] else
      (omittedDefaults ds x as).flatMap fun (scope, e) => mentions ds scope e
    | _ => []
  | .tuple es | .interp es => es.flatMap (mentions ds bound)
  | .lambda ps _ b => mentions ds (ps.map (·.name) ++ bound) b
  | .named _ e => mentions ds bound e
  | .block es =>
    (es.foldl (init := (bound, [])) fun (b, acc) e =>
      let b' := match e with | .decl _ ns _ _ _ => ns ++ b | _ => b
      (b', acc ++ mentions ds b e)).2
  | .ifExpr c a b => mentions ds bound c ++ mentions ds bound a ++ mentions ds bound b
  | .ifStmt c a b => mentions ds bound c ++ mentions ds bound a ++ (b.map (mentions ds bound)).getD []
  | .mtch s cs => mentions ds bound s ++ cs.flatMap fun (p, g, b) =>
    let ns := patNames p
    ns.filter (!bound.contains ·) ++ (g.map (mentions ds (ns ++ bound))).getD []
      ++ mentions ds (ns ++ bound) b
  | .assign _ ts vs => ts.flatMap (mentions ds bound) ++ vs.flatMap (mentions ds bound)
  | .decl _ _ _ _ vs => vs.flatMap (mentions ds bound)

/-- Everything a list of names reaches: the names, and through every function among them,
    the free names of its body, transitively. -/
def reach (ds : Decls) (start : List String) : List String :=
  go (ds.funcs.length + 1) start []
where
  /-- The names a function's body mentions. The defaults a call leaves out are
      mentions of the call (`mentions`). -/
  expand (x : String) : Option (List String) :=
    (ds.funcs.lookup x).map fun f => mentions ds (f.params.map (·.name)) f.body
  go : Nat → List String → List String → List String
    | 0, _, seen => seen
    | _, [], seen => seen
    | n + 1, x :: todo, seen =>
      if seen.contains x then go (n + 1) todo seen
      else match expand x with
        | some ms => go n (ms ++ todo) (x :: seen)
        | none => go (n + 1) todo (x :: seen)
  termination_by n todo => (n, todo.length)

/-- A package-level declaration. -/
structure GlobalDecl where
  mutable : Bool
  names : List String
  destructure : Bool
  type : Option Ty
  values : List Expr

/-- Initialize the package-level variables in Go's order (see the module doc). -/
def initGlobals (ds : Decls) (d : Nat) (gs : List GlobalDecl) : M Unit := do
  let all := gs.flatMap (·.names)
  -- Each declaration with its position and the package-level variables it depends on.
  let mut pending := (List.range gs.length).zip (gs.zip (gs.map fun g =>
    (reach ds (g.values.flatMap (mentions ds []))).filter (all.contains ·)))
  let mut done : List String := []
  for _ in gs do
    match pending.find? (fun (_, _, deps) => deps.all done.contains) with
    | none => unsup "init-cycle"
    | some (i, g, _) =>
      pending := pending.filter (·.1 != i)
      modify fun s => { s with locals := [] }
      let bs ← declare ds d g.names g.destructure g.type g.values
      modify fun s => { s with globals := bs.foldl (fun ρ (x, v) => bind ρ x ⟨v, g.mutable⟩) s.globals }
      done := done ++ g.names


/-- The declarations of a program, and its package-level variables in source order. -/
def collect (p : Program) : Decls × List GlobalDecl :=
  p.files.foldl (init := ({ funcs := [], structs := [], shorthands := [], sealeds := [], unsupported := [] }, []))
    fun acc file => file.decls.foldl (init := acc) fun (ds, gs) d => match d with
      | .func f => ({ ds with funcs := ds.funcs ++ [(f.name, f)] }, gs)
      | .struct_ n sh fs => ({ ds with structs := ds.structs ++ [(n, fs)],
                                        shorthands := if sh then ds.shorthands ++ [n] else ds.shorthands }, gs)
      | .sealed n vs => ({ ds with sealeds := ds.sealeds ++ [(n, vs)] }, gs)
      | .global m ns dz t vs =>
        let g : GlobalDecl := ⟨m == Mut.var, ns, dz, t, vs⟩
        (ds, gs ++ [g])
      | .unsupported k => ({ ds with unsupported := ds.unsupported ++ [k] }, gs)

/-- Run a program: initialize its package-level variables, then call `main`. -/
def runM (p : Program) (fuel : Nat) : M Unit := do
  let (ds, gs) := collect p
  initGlobals ds fuel gs
  match ds.funcs.lookup "main" with
  | some f => discard (callFunc ds fuel f [])
  | none => unsup "no-main"

/-- The meaning of a program with `fuel` steps: what it prints, and how it ends. -/
def run (p : Program) (fuel : Nat) : Outcome × String :=
  let (r, s) := (runM p fuel).run.run { locals := [], globals := [], out := "", steps := fuel }
  let o := match r with
    | .ok () => .ok
    | .error (.panic c m) => .panic c m
    | .error .fuel => .fuel
    | .error (.unsupported k m) => .unsupported k m
    | .error (.stuck m) => .stuck m
    | .error (.ret _) => .stuck "return outside a function"
  (o, s.out)

end Gala.Spec
