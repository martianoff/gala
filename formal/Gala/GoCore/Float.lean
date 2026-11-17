/-
  # GoCore: floating-point values

  IEEE 754 binary64 (`float64`) and binary32 (`float32`) values, the way Go has them
  (Go spec, "Numeric types", "Arithmetic operators": IEEE 754 with round-half-to-even).
  Arithmetic is Lean's `Float`, one IEEE operation per Go operation (a float32
  operation done in binary64 and rounded to binary32, which for `+ - * /` gives the
  binary32 result exactly). What is computed here, exactly, with `Nat` arithmetic:

  * the rounding of an exact rational (an untyped constant, an integer) to the nearest
    float, ties to even (Go spec, "Conversions", "Constant expressions");
  * the shortest decimal that rounds back to a float, as `strconv.FormatFloat(f, 'g',
    -1, bits)` writes it (strconv/ftoa.go: Ryu's shortest digits, and the `%g` layout
    with its exponent threshold of 6 for the shortest form), which fmt's `%v` prints;
  * the digits of `%e`, `%f` and `%g` at a given precision: the exact value rounded half
    to even, laid out as strconv does.
-/
namespace Gala.GoCore.Full.FloatRep

/-- An IEEE binary format: fraction bits, exponent bias, the all-ones exponent. -/
structure Fmt where
  mbits : Nat
  bias : Nat
  emax : Nat

def f64 : Fmt := ⟨52, 1023, 2047⟩
def f32 : Fmt := ⟨23, 127, 255⟩

/-- The exponent of the least significant bit of a subnormal (`2^(1 - bias - mbits)`). -/
def Fmt.emin (f : Fmt) : Int := 1 - f.bias - f.mbits

/-- `num / den` (`den > 0`) rounded to the nearest float of format `f`, ties to even, as
    the bits of a positive float (infinity when too large). -/
def roundBits (f : Fmt) (num den : Nat) : Nat :=
  if num == 0 || den == 0 then 0 else
  let top := 2 ^ (f.mbits + 1)
  -- `m` is the rounded `num / den / 2^e`.
  let roundAt := fun (e : Int) =>
    let (n, d) := if e ≤ 0 then (num * 2 ^ (-e).toNat, den) else (num, den * 2 ^ e.toNat)
    let q := n / d
    let r := n % d
    if 2 * r > d || (2 * r == d && q % 2 == 1) then q + 1 else q
  -- The leading bit's exponent is `log2 num - log2 den` or one less: start one below
  -- and raise `e` until the rounded value fits in `mbits + 1` bits.
  let k : Int := (Nat.log2 num : Int) - (Nat.log2 den : Int)
  let rec fit : Nat → Int → Nat × Int
    | 0, e => (roundAt e, e)
    | j + 1, e => let m := roundAt e; if m < top then (m, e) else fit j (e + 1)
  let (m, e) := fit 4 (max f.emin (k - 1 - f.mbits))
  if m < 2 ^ f.mbits then m  -- subnormal (`e` is `emin`)
  else
    let biased := (e - f.emin + 1).toNat
    if biased ≥ f.emax then f.emax * 2 ^ f.mbits
    else biased * 2 ^ f.mbits + (m - 2 ^ f.mbits)

/-- A float's bits split up: sign, biased exponent, fraction. -/
def split (f : Fmt) (bits : Nat) : Bool × Nat × Nat :=
  let frac := bits % 2 ^ f.mbits
  let biased := (bits / 2 ^ f.mbits) % (f.emax + 1)
  let neg := bits / 2 ^ (f.mbits + (Nat.log2 (f.emax + 1))) % 2 == 1
  (neg, biased, frac)

/-- A finite float's magnitude as `m * 2^e`. -/
def mantExp (f : Fmt) (biased frac : Nat) : Nat × Int :=
  if biased == 0 then (frac, f.emin) else (frac + 2 ^ f.mbits, biased + f.emin - 1)

/-- The exact value of `a * 2^e2 * 10^s` as a fraction `(num, den)`. -/
def scaled (a : Nat) (e2 s : Int) : Nat × Nat :=
  let num := a * (if e2 ≥ 0 then 2 ^ e2.toNat else 1) * (if s ≥ 0 then 10 ^ s.toNat else 1)
  let den := (if e2 < 0 then 2 ^ (-e2).toNat else 1) * (if s < 0 then 10 ^ (-s).toNat else 1)
  (num, den)

/-- The shortest decimal of a positive finite float `m * 2^e` that rounds back to it
    (Ryu's shortest, strconv/ftoa.go `ryuFtoaShortest`): its digits, without trailing
    zeros, and the decimal point's position `dp` (value `0.d₁d₂… × 10^dp`). The
    rounding interval reaches halfway to the neighbours, closed when the mantissa is
    even (Go's parsing rounds half to even); among the shortest decimals inside it, the
    closest. `lowerGap` is set at a power of two, where the lower neighbour is half as
    far. -/
def shortest (f : Fmt) (m : Nat) (e : Int) (lowerGap : Bool) : String × Int := Id.run do
  let even := m % 2 == 0
  let v := 4 * m
  let up := 4 * m + 2
  let lo := if lowerGap then 4 * m - 1 else 4 * m - 2
  let e2 := e - 2
  -- An estimate of floor(log10 (m * 2^e)), corrected below.
  let lg2 : Int := (Nat.log2 m : Int) + e
  let lg10 : Int := (lg2 * 30103) / 100000
  for k in [1:f.mbits / 3 + 3] do
    let mut s : Int := (k : Int) - 1 - lg10
    -- Put floor(v * 10^s) in [10^(k-1), 10^k).
    for _ in [0:6] do
      let (n, d) := scaled v e2 s
      let c := n / d
      if c ≥ 10 ^ k then s := s - 1
      else if c < 10 ^ (k - 1) then s := s + 1
    let (n, d) := scaled v e2 s
    let (nl, _) := scaled lo e2 s
    let (nu, _) := scaled up e2 s
    let inside := fun (c : Nat) =>
      if even then nl ≤ c * d && c * d ≤ nu else nl < c * d && c * d < nu
    let cLo := n / d
    let cHi := cLo + 1
    let dist := fun (c : Nat) => if c * d ≥ n then c * d - n else n - c * d
    let pick : Option Nat :=
      match inside cLo, inside cHi with
      | true, true =>
        if dist cLo < dist cHi then some cLo
        else if dist cHi < dist cLo then some cHi
        else some (if cLo % 2 == 0 then cLo else cHi)
      | true, false => some cLo
      | false, true => some cHi
      | false, false => none
    if let some c := pick then
      let ds := (toString c).toList
      let nd := ds.length
      let trimmed := (ds.reverse.dropWhile (· == '0')).reverse
      return (String.ofList trimmed, (nd : Int) - s)
  -- Unreachable: 17 (binary64) or 9 (binary32) digits always suffice.
  return ("", 0)

/-- `%e` of decimal digits `ds` with point position `dp` and `prec` digits after the
    point (strconv/ftoa.go `fmtE`): at least two exponent digits. -/
def fmtE (ds : String) (dp : Int) (prec : Nat) : String :=
  let d := ds.toList
  let first := d.headD '0'
  let rest := (d.drop 1).take prec
  let frac := if prec > 0 then "." ++ String.ofList (rest ++ List.replicate (prec - rest.length) '0') else ""
  let exp := if d.isEmpty then 0 else dp - 1
  let sign := if exp < 0 then "-" else "+"
  let digits := toString exp.natAbs
  String.singleton first ++ frac ++ "e" ++ sign ++ (if digits.length < 2 then "0" ++ digits else digits)

/-- `%f` of decimal digits `ds` with point position `dp` and `prec` digits after the
    point (strconv/ftoa.go `fmtF`). -/
def fmtF (ds : String) (dp : Int) (prec : Nat) : String :=
  let d := ds.toList
  let nd := d.length
  let int := if dp > 0 then
      String.ofList (d.take dp.toNat ++ List.replicate (dp.toNat - min nd dp.toNat) '0')
    else "0"
  let frac := if prec > 0 then
      "." ++ String.ofList ((List.range prec).map fun (i : Nat) =>
        let j : Int := dp + i
        if 0 ≤ j && j < nd then d[j.toNat]! else '0')
    else ""
  int ++ frac

/-- `num / den` (`den > 0`) rounded to an integer, half to even. -/
def roundHalfEven (num den : Nat) : Nat :=
  let q := num / den
  let r := num % den
  if 2 * r > den || (2 * r == den && q % 2 == 1) then q + 1 else q

/-- The decimal point position `dp` of a positive `m * 2^e`: `10^(dp-1) ≤ v < 10^dp`. -/
def decExp (m : Nat) (e : Int) : Int := Id.run do
  let mut dp : Int := ((((Nat.log2 m : Int) + e) * 30103) / 100000) + 1
  -- The estimate is off by at most one or two; correct it exactly.
  for _ in [0:8] do
    let (n, d) := scaled m e (1 - dp)
    if n < d then dp := dp - 1
    else
      let (n, d) := scaled m e (-dp)
      if n ≥ d then dp := dp + 1
  return dp

/-- A positive `m * 2^e` rounded to `k ≥ 1` significant digits, half to even: its `k`
    digits and point position. -/
def sigDigits (m : Nat) (e : Int) (k : Nat) : String × Int :=
  let dp := decExp m e
  let c := Function.uncurry roundHalfEven (scaled m e ((k : Int) - dp))
  if c == 10 ^ k then (toString (10 ^ (k - 1)), dp + 1) else (toString c, dp)

/-- A non-negative `m * 2^e` rounded to `p` digits after the point, half to even: its
    digits (none for zero) and point position. -/
def fixedDigits (m : Nat) (e : Int) (p : Nat) : String × Int :=
  let c := Function.uncurry roundHalfEven (scaled m e p)
  if c == 0 then ("", 0) else let ds := toString c; (ds, (ds.length : Int) - p)

/-- `strconv`'s `%g` layout of digits `ds` (no trailing zeros) with point position `dp`
    at precision `prec` (`formatDigits`): `%e` form when the exponent is below -4 or
    at least `eprec`, else `%f` form, with no trailing zeros either way. -/
def layoutG (ds : String) (dp : Int) (prec : Nat) (shortest : Bool) : String :=
  let nd := ds.length
  let eprec : Int := if shortest then 6 else if prec > nd && (nd : Int) ≥ dp then nd else prec
  let exp := dp - 1
  if exp < -4 || exp ≥ eprec then fmtE ds dp (min prec nd - 1)
  else fmtF ds dp ((if (prec : Int) > dp then (nd : Int) else prec) - dp).toNat

/-- A float of format `f`, given by its bits, as `strconv.FormatFloat(x, verb, prec,
    size)` writes it, for `verb` `e`, `f` or `g` (`prec` absent: `g`'s shortest):
    whether it is negative, whether it is an infinity or NaN, and the text without
    its sign (`Inf`, `NaN`). The digits are the exact binary value rounded half to
    even, which is what strconv computes: `%f` (always) and long `%e`/`%g` go through
    `bigFtoa`, whose decimal holds the whole exact expansion (800 digits, more than the
    767 of the longest float64), so its `trunc` rounding never applies; the shorter
    ones through `ryuFtoaFixed`, which rounds an exact tie to even. -/
def formatVerb (f : Fmt) (bits : Nat) (verb : Char) (prec : Option Nat) : Bool × Bool × String :=
  let (neg, biased, frac) := split f bits
  if biased == f.emax then (neg && frac == 0, true, if frac != 0 then "NaN" else "Inf")
  else
    let (m, e) := if biased == 0 && frac == 0 then (0, 0) else mantExp f biased frac
    let trim := fun (s : String) => String.ofList (s.toList.reverse.dropWhile (· == '0')).reverse
    let text := match verb, prec with
      | 'e', p => let p := p.getD 6; if m == 0 then fmtE "" 0 p else let (ds, dp) := sigDigits m e (p + 1); fmtE ds dp p
      | 'f', p => let p := p.getD 6; let (ds, dp) := fixedDigits m e p; fmtF ds dp p
      | _, some p =>
        let p := max p 1
        if m == 0 then layoutG "" 0 p false
        else let (ds, dp) := sigDigits m e p; layoutG (trim ds) dp p false
      | _, none =>
        if m == 0 then "0"
        else let (ds, dp) := shortest f m e (frac == 0 && biased > 1); layoutG ds dp ds.length true
    (neg, false, text)

/-- A float of format `f`, given by its bits, as `strconv.FormatFloat(x, 'g', -1, size)`
    writes it: the shortest digits, in `%e` form when the exponent is below -4 or at
    least 6 (`formatDigits`: `if shortest { eprec = 6 }`), else in `%f` form; `+Inf`,
    `-Inf`, `NaN`. -/
def formatG (f : Fmt) (bits : Nat) : String :=
  match formatVerb f bits 'g' none with
  | (_, true, "NaN") => "NaN"
  | (neg, true, t) => (if neg then "-" else "+") ++ t
  | (neg, false, t) => (if neg then "-" else "") ++ t

/-- The bits of a binary64 value, and of a binary32 one held as a `Float`. -/
def bits64 (x : Float) : Nat := x.toBits.toNat
def bits32 (x : Float) : Nat := x.toFloat32.toBits.toNat

/-- A float as Go's `%v` prints it, `float64` or `float32` by `size`. -/
def format (size : Nat) (x : Float) : String :=
  if size == 32 then formatG f32 (bits32 x) else formatG f64 (bits64 x)

/-- The float of `size` nearest the rational `num / den` (`den > 0`), ties to even
    (Go spec, "Conversions": a constant is rounded; "Representability"). A binary32
    value is held as the `Float` it converts to exactly. -/
def ofRat (size : Nat) (num : Int) (den : Nat) : Float :=
  let bits := roundBits (if size == 32 then f32 else f64) num.natAbs den
  let mag := if size == 32 then (Float32.ofBits (UInt32.ofNat bits)).toFloat
    else Float.ofBits (UInt64.ofNat bits)
  -- A negative value that rounds to zero is +0: an exact value has no negative zero.
  if num < 0 && bits != 0 then -mag else mag

/-- The exact value of the float of `size` nearest `num / den`, in lowest terms: a typed
    constant's value (Go spec, "Constants": a typed constant is rounded to its type);
    `none` when it overflows the type, which Go rejects ("Representability"). -/
def roundRat (size : Nat) (num : Int) (den : Nat) : Option (Int × Nat) :=
  let f := if size == 32 then f32 else f64
  let bits := roundBits f num.natAbs den
  let (_, biased, frac) := split f bits
  if biased == f.emax then none
  else
    let (m, e) := mantExp f biased frac
    let (n, d) : Nat × Nat := if e ≥ 0 then (m * 2 ^ e.toNat, 1) else (m, 2 ^ (-e).toNat)
    let g := Nat.gcd n d
    let n : Int := (n / g : Nat)
    some (if num < 0 then -n else n, if g == 0 then 1 else d / g)

/-- A float64 or float32 value rounded to `size` bits: an operation's result. -/
def toSize (size : Nat) (x : Float) : Float :=
  if size == 32 then x.toFloat32.toFloat else x

/-- A finite float's value truncated toward zero (Go spec, "Conversions": a float
    converted to an integer type drops the fraction); `none` for an infinity or NaN. -/
def truncate (x : Float) : Option Int :=
  let (neg, biased, frac) := split f64 (bits64 x)
  if biased == f64.emax then none
  else
    let (m, e) := mantExp f64 biased frac
    let mag : Nat := if e ≥ 0 then m * 2 ^ e.toNat else m / 2 ^ (-e).toNat
    some (if neg then -(mag : Int) else mag)

end Gala.GoCore.Full.FloatRep
