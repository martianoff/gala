/-
  # M5 step 3d: GoCore's fuel adequacy on a run

  A whole program's GoCore run is not evaluated by Lean's kernel: `buildCtx` builds hash
  maps keyed by strings, whose hash the kernel does not compute. So
  `runProgramAt_adequate`'s hypothesis is discharged for concrete programs by step 5's
  whole-program instances, and the per-function results (`simAll`, `syncAll`) are pinned
  here on the run of `sw(1, 2, 2)` that step 3c proved ends with `-1` (`sw_gocore_ok`:
  40 steps, depth at least 88).
-/
import Gala.M5.GoFuel
import Gala.M5.LoopInst

namespace Gala.M5

open Gala.GoCore.Full

/-- More steps and more depth on `sw(1, 2, 2)`: with `j` more steps and any depth
    `N' ≥ N`, GoCore's run ends with the same value, in the same state with `j` more
    steps left. -/
theorem sw_gocore_more (N N' j : Nat) (hN : N ≥ 88) (hN' : N ≤ N') :
    ∃ w g', (evalExpr swCtx N' incEnv swCallG).run.run { incSt with steps := 40 + j } =
      (.ok w, shift j g') ∧ VRel (.int (-1)) w := by
  obtain ⟨w, g', hg, hv⟩ := sw_gocore_ok N hN
  refine ⟨w, g', ?_, hv⟩
  have h := ((simAll swCtx j N N' hN').evalExpr incEnv swCallG).prop { incSt with steps := 40 }
    (by rw [hg]; trivial)
  rw [hg] at h
  exact h

/-- The state `sw(1, 2, 2)`'s run ends in, at any depth, has as many cell types as cells:
    `syncAll` applied to a start with none of either. -/
theorem sw_gocore_sync (N : Nat) :
    Sync ((evalExpr swCtx N incEnv swCallG).run.run { incSt with steps := 40 }).2 :=
  (syncAll swCtx N).evalExpr incEnv swCallG _ rfl

end Gala.M5
