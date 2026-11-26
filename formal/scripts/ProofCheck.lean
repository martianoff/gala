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
  `Gala.Spec.capturedEnv_captured,
  `Gala.Spec.defaultFrame_other,
  `Gala.Spec.defaultFrame_last,
  `Gala.Gala2Lean.Examples.Match.eval_total,
  `Gala.Spec.run_fuel_adequate,
  `Gala.Spec.defaultFrame_lookup,
  `Gala.Spec.bindDefaults_callerLocals,
  `Gala.Spec.evalList_acts,
  `Gala.Spec.eval_and_acts,
  `Gala.Spec.eval_or_acts,
  `Gala.Spec.argSlots_eq,
  `Gala.Spec.bindArgs_acts,
  `Gala.Spec.eval_compound_acts,
  `Gala.Spec.compound_eff,
  `Gala.Spec.matchPats_stops,
  `Gala.Spec.evalCases_nomatch,
  `Gala.Spec.evalCases_guard_false,
  `Gala.Spec.evalCases_guard_true,
  `Gala.Spec.evalCases_match,
  `Gala.Spec.evalCases_nil,
  `Gala.Spec.eval_binary_acts,
  `Gala.Spec.eval_tuple_acts,
  `Gala.Spec.eval_interp_acts,
  `Gala.Spec.bindDefaults_acts,
  `Gala.Spec.matchPats_append,
  `Gala.Spec.lookup_rel,
  `Gala.Spec.resolve_rel,
  `Gala.Spec.matchPat_rel,
  `Gala.Spec.argSlots_rel,
  `Gala.Spec.argSlots_same_rel,
  `Gala.Spec.byNameOk_rel,
  `Gala.Spec.binop_rel,
  `Gala.Spec.unop_rel,
  `Gala.Spec.fmtVs_rel,
  `Gala.Spec.getField_rel,
  `Gala.Spec.readPlace_rel,
  `Gala.Spec.assignTo_rel,
  `Gala.Spec.pushAll_rel,
  `Gala.Spec.construct_rel,
  `Gala.Spec.emit_rel,
  `Gala.Spec.setField_rel,
  `Gala.Spec.alloc_rel,
  `Gala.M3.valEq_enc,
  `Gala.M3.lowerPat_correct,
  `Gala.M3.lowerMatch_correct,
  `Gala.M3.leaky_not_correct,
  `Gala.M3.part_lit_int,
  `Gala.M3.part_var,
  `Gala.M3.part_add,
  `Gala.M3.part_gt,
  `Gala.M3.lowerMatch_instance,
  `Gala.Spec.eval_alpha,
  `Gala.Spec.run_alpha,
  `Gala.Spec.alphaCheck_sound,
  `Gala.Spec.alphaCheck_run,
  `Gala.M5.eval_pure,
  `Gala.M5.expr_correct,
  `Gala.M5.expr_correct_instance,
  `Gala.M5.inst_bitwise,
  `Gala.M5.inst_shift,
  `Gala.M5.inst_strcmp,
  `Gala.M5.stmts_correct,
  `Gala.M5.block_correct,
  `Gala.M5.stmts_correct_instance,
  `Gala.M5.inst_gocore_ok,
  `Gala.M5.hSs_lowered,
  `Gala.M5.gc_runsStep2,
  `Gala.M5.leftToRight_runsStep2,
  `Gala.M5.calls_correct,
  `Gala.M5.body_correct,
  `Gala.M5.stmts3_correct,
  `Gala.M5.lowerC_mono,
  `Gala.M5.lowerBody_mono,
  `Gala.M5.lowerFun_mono,
  `Gala.M5.calls_correct_instance,
  `Gala.M5.loop_calls_instance,
  `Gala.M5.sw_spec_ok,
  `Gala.M5.sw_gocore_ok,
  `Gala.M5.swProg_gc,
  `Gala.M5.tmpCall_no_funRel,
  `Gala.M5.tmpParam_not_lowered,
  `Gala.M5.sw_lowerFun,
  `Gala.M5.inc_spec_ok,
  `Gala.M5.inc_gocore_ok,
  `Gala.M5.canon_eq,
  `Gala.M5.gc_runs3,
  `Gala.M5.leftToRight_runs3,
  `Gala.M5.ordProg_gc,
  `Gala.M5.body3_div_call,
  `Gala.M5.body3_div_arg_call,
  `Gala.M5.body3_div_inside_call,
  `Gala.M5.tailIf_lowerBody,
  `Gala.M5.armNest_lowerBody,
  `Gala.M5.retInArm_not_lowered,
  `Gala.M5.iife_correct_instance,
  `Gala.M5.if_spec_ok,
  `Gala.M5.iife_gocore_ok,
  `Gala.M5.iifeProg_gc,
  `Gala.M5.body3_div_iife,
  `Gala.M5.body3_iife_div_inside,
  `Gala.M5.nestedBlock_not_lowered,
  `Gala.M5.paramShadow_not_lowered,
  `Gala.M5.scopeOK_redecl_in_arm,
  `Gala.M5.scopeOK_sibling_arms
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
