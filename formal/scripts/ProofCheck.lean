/-
  Proof check for the Lean semantics of GALA.

  Lean only warns when a proof uses `sorry`, so `lake build` passing does not mean the
  proofs are complete. This script imports every module of the `Gala` library (the root
  `Gala.lean` and every file under `Gala/`), and fails if any declaration in them depends
  on `sorry` or on an axiom other than Lean's standard ones. It also fails if a theorem
  COVERAGE.md calls proved (`requiredTheorems`) is missing.

  Run it from `formal/` after `lake build`:

    lake env lean --run scripts/ProofCheck.lean

  The axiom check follows the `lean_proof_test` checker of rules_lean (Apache 2.0).
-/
import Lean

open Lean System

/-- The only axioms a declaration may depend on. `sorry` shows up as `sorryAx`. -/
def allowedAxioms : List Name := [``propext, ``Quot.sound, ``Classical.choice]

/-- The theorems COVERAGE.md calls proved. Each must exist and be a theorem, so deleting or
    renaming one fails the check instead of leaving COVERAGE.md wrong. -/
def requiredTheorems : List Name := [
  `Gala.MatchLowering.compile_correct,
  `Gala.MatchLowering.program_correct,
  `Gala.MatchLowering.leaky_not_correct,
  `Gala.Spec.run_deterministic,
  `Gala.Spec.eval_deterministic,
  `Gala.Spec.lookup_bind_self,
  `Gala.Spec.lookup_bind_other,
  `Gala.Spec.update_length,
  `Gala.Spec.exitScope_push,
  `Gala.Spec.lookup_rename,
  `Gala.Spec.update_rename,
  `Gala.Spec.exitScope_rename,
  `Gala.Spec.evalList_cons,
  `Gala.Spec.evalList_nil,
  `Gala.Spec.capturedEnv_captured,
  `Gala.Spec.lookup_captured
]

/-- The library's modules: `Gala` plus one module per `.lean` file under `Gala/`. -/
def galaModules : IO (Array Name) := do
  let mut mods : Array Name := #[`Gala]
  for path in ← FilePath.walkDir "Gala" do
    if path.extension != some "lean" then continue
    let rel := (path.withExtension "").components
    mods := mods.push (rel.foldl (fun n c => Name.str n c) Name.anonymous)
  return mods.qsort Name.lt

def main : IO UInt32 := do
  initSearchPath (← findSysroot)
  let mods ← galaModules
  let env ← importModules (mods.map fun m => { module := m }) {}
  let mut failures : Array String := #[]
  let mut checked := 0
  -- Only the constants of our own modules, not the whole imported environment.
  for (mod, data) in env.header.moduleNames.zip env.header.moduleData do
    unless mods.contains mod do continue
    for name in data.constNames do
      -- Every constant, private and auxiliary ones included: a private lemma's sorry
      -- counts even when no public theorem uses it.
      checked := checked + 1
      let (axioms, _) ← (Lean.collectAxioms name : CoreM (Array Name)).toIO
        { fileName := "<proofcheck>", fileMap := default } { env }
      let bad := axioms.filter (fun a => !allowedAxioms.contains a)
      unless bad.isEmpty do
        failures := failures.push s!"  {name} ({mod}) depends on sorry or a disallowed axiom: {bad.toList}"
  for name in requiredTheorems do
    unless (env.find? name).any (·.isTheorem) do
      failures := failures.push s!"  {name} is listed as proved but is not a theorem in the library"
  if checked == 0 then
    IO.eprintln s!"proofcheck: no declarations found in {mods.toList}"
    return 1
  unless failures.isEmpty do
    IO.eprintln "proofcheck: failed:"
    for f in failures do IO.eprintln f
    return 1
  IO.println s!"proofcheck: {checked} declarations in {mods.size} modules use only {allowedAxioms}"
  return 0
