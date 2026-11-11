/-
  Proof check for the Lean semantics of GALA.

  Lean only warns when a proof uses `sorry`, so `lake build` passing does not mean the
  proofs are complete. This script imports every module of the `Gala` library (the root
  `Gala.lean` and every file under `Gala/`), and fails if any declaration in them depends
  on `sorry` or on an axiom other than Lean's standard ones.

  Run it from `formal/` after `lake build`:

    lake env lean --run scripts/ProofCheck.lean

  The axiom check follows the `lean_proof_test` checker of rules_lean (Apache 2.0).
-/
import Lean

open Lean System

/-- The only axioms a declaration may depend on. `sorry` shows up as `sorryAx`. -/
def allowedAxioms : List Name := [``propext, ``Quot.sound, ``Classical.choice]

/-- The library's modules: `Gala` plus one module per `.lean` file under `Gala/`. -/
partial def galaModules : IO (Array Name) := do
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
  for (name, _) in env.constants.map₁.toList do
    let some idx := env.getModuleIdxFor? name | continue
    let mod := env.header.moduleNames[idx.toNat]!
    unless mods.contains mod do continue
    if name.isInternal then continue
    checked := checked + 1
    let (axioms, _) ← (Lean.collectAxioms name : CoreM (Array Name)).toIO
      { fileName := "<proofcheck>", fileMap := default } { env }
    let bad := axioms.filter (fun a => !allowedAxioms.contains a)
    unless bad.isEmpty do
      failures := failures.push s!"  {name} ({mod}) depends on {bad.toList}"
  if checked == 0 then
    IO.eprintln s!"proofcheck: no declarations found in {mods.toList}"
    return 1
  unless failures.isEmpty do
    IO.eprintln "proofcheck: declarations depend on sorry or on axioms that are not allowed:"
    for f in failures do IO.eprintln f
    return 1
  IO.println s!"proofcheck: {checked} declarations in {mods.size} modules use only {allowedAxioms}"
  return 0
