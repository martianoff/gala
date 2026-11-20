/-
  # T2, part c: whole programs

  `run_alpha`: a program and its renaming (`AlphaProgram`) print the same output and end
  the same way, up to the text of `stuck` and `unsupported`. Built on `eval_alpha`
  (Gala/AlphaEval.lean) for the package-level initializers and for `main`, and on
  `mentions_alpha` and `reach_alpha` here for the order `initGlobals` initializes them in.
-/
import Gala.AlphaConst

namespace Gala.Spec

/-! ## The initialization order

`mentions` lists the free names of an expression. Renaming changes the names it lists
only outside `pkg`: a renamed binder is never a package-level name, and one kept keeps its
name. So the names in `pkg` it lists are the same on both sides, and so is whether it cut
a cycle of defaults off. -/

/-- The names of a list in `pkg`. -/
def inPkg (pkg : List String) (l : List String) : List String := l.filter (fun x => decide (x ∈ pkg))

/-- `mentions` results related by a renaming: the same names in `pkg`. -/
def MenR (pkg : List String) (m m' : List String) : Prop := inPkg pkg m = inPkg pkg m'

/-- A scope of `mentions` related to the renaming's: every binder of `Γ` is a renamed one,
    one that is a package-level name is in `b`, and `b`, `b'` agree on package-level names. -/
structure MenScope (pkg : List String) (Γ : Ren) (b b' : List String) : Prop where
  binders : ∀ p ∈ Γ, BinderR pkg p.1 p.2
  bound : ∀ p ∈ Γ, p.1 ∈ pkg → p.1 ∈ b
  same : ∀ y ∈ pkg, y ∈ b ↔ y ∈ b'

theorem inPkg_append (pkg : List String) (l l' : List String) :
    inPkg pkg (l ++ l') = inPkg pkg l ++ inPkg pkg l' := List.filter_append ..

theorem MenR.cat {pkg : List String} {a a' b b' : List String} (ha : MenR pkg a a') (hb : MenR pkg b b') :
    MenR pkg (a ++ b) (a' ++ b') := by
  unfold MenR at *; rw [inPkg_append, inPkg_append, ha, hb]

theorem MenR.nil {pkg : List String} : MenR pkg [] [] := rfl

/-- Mentions of a list, related element by element. -/
theorem MenR.all {pkg : List String} {f : α → List String} {g : β → List String} :
    ∀ {l : List α} {l' : List β}, Fall2 (MenR pkg) (l.map f) (l'.map g) → MenR pkg (l.flatMap f) (l'.flatMap g)
  | [], [], _ => rfl
  | _ :: _, _ :: _, .cons h t => by
    simp only [List.flatMap_cons]
    exact h.cat (MenR.all t)
  | [], _ :: _, h | _ :: _, [], h => by cases h

theorem mem_of_bound : ∀ {Γ : Ren} {x x' : String}, Γ.bound x x' → (x, x') ∈ Γ
  | [], _, _, h => absurd h (by simp [Ren.bound])
  | (a, a') :: Γ, x, x', h => by
    simp only [Ren.bound] at h
    rcases h with ⟨rfl, rfl⟩ | ⟨_, _, h⟩
    · exact List.mem_cons_self
    · exact List.mem_cons_of_mem _ (mem_of_bound h)

theorem contains_iff (b : List String) (x : String) : b.contains x = true ↔ x ∈ b :=
  List.contains_iff_mem

/-- A name's mention, when it refers to the same thing on both sides. -/
theorem menR_name {pkg : List String} {Γ : Ren} {b b' : List String} {x x' : String}
    (hs : MenScope pkg Γ b b') (hx : NameR Γ x x') :
    MenR pkg (if b.contains x then [] else [x]) (if b'.contains x' then [] else [x']) := by
  unfold MenR
  have key : x ∈ pkg ∨ x' ∈ pkg → x = x' ∧ (x ∈ b ↔ x' ∈ b') := by
    intro hp
    rcases hx with hb | ⟨rfl, _⟩
    · have hm := mem_of_bound hb
      rcases hs.binders _ hm with he | ⟨_, _, _, _, h1, h2⟩
      · simp only at he; subst he
        have hxp : x ∈ pkg := by rcases hp with h | h <;> exact h
        exact ⟨rfl, hs.same x hxp⟩
      · rcases hp with h | h
        · exact absurd h h1
        · exact absurd h h2
    · rcases hp with h | h <;> exact ⟨rfl, hs.same x h⟩
  by_cases hp : x ∈ pkg ∨ x' ∈ pkg
  · obtain ⟨rfl, hiff⟩ := key hp
    by_cases hb : x ∈ b
    · have hb' := hiff.1 hb
      simp [hb, hb']
    · have hb' : x ∉ b' := fun h => hb (hiff.2 h)
      simp [hb, hb']
  · simp only [not_or] at hp
    split <;> split <;> simp [inPkg, hp.1, hp.2]

/-! ### Scopes -/

theorem mem_pushAll : ∀ {Γ Δ : Ren} {p : String × String}, p ∈ Γ.pushAll Δ → p ∈ Δ ∨ p ∈ Γ
  | _, [], _, h => Or.inr h
  | Γ, xx :: Δ, p, h => by
    simp only [Ren.pushAll, List.foldl_cons] at h
    rcases mem_pushAll (Γ := Γ.push xx) (Δ := Δ) h with h | h
    · exact Or.inl (List.mem_cons_of_mem _ h)
    · unfold Ren.push at h
      split at h
      · exact Or.inr h
      · rcases List.mem_cons.mp h with rfl | h
        · exact Or.inl List.mem_cons_self
        · exact Or.inr h

/-- Pushing binders `Δ` named in `ns`, `ns'`, which agree on package-level names. -/
theorem MenScope.push {pkg : List String} {Γ Δ : Ren} {b b' ns ns' : List String}
    (hs : MenScope pkg Γ b b') (hΔ : ∀ p ∈ Δ, BinderR pkg p.1 p.2 ∧ p.1 ∈ ns)
    (hn : ∀ y ∈ pkg, y ∈ ns ↔ y ∈ ns') : MenScope pkg (Γ.pushAll Δ) (ns ++ b) (ns' ++ b') where
  binders p hp := by
    rcases mem_pushAll hp with h | h
    · exact (hΔ p h).1
    · exact hs.binders p h
  bound p hp hk := by
    rcases mem_pushAll hp with h | h
    · exact List.mem_append_left _ (hΔ p h).2
    · exact List.mem_append_right _ (hs.bound p h hk)
  same y hy := by
    simp only [List.mem_append]
    exact or_congr (hn y hy) (hs.same y hy)

/-- Names renamed pairwise as binders agree on package-level names. -/
theorem zip_same {pkg : List String} :
    ∀ {ns ns' : List String}, ns.length = ns'.length → (∀ p ∈ ns.zip ns', BinderR pkg p.1 p.2) →
      ∀ y ∈ pkg, y ∈ ns ↔ y ∈ ns'
  | [], [], _, _, _, _ => Iff.rfl
  | a :: ns, a' :: ns', hl, h, y, hy => by
    have ih := zip_same (ns := ns) (ns' := ns') (by simpa using hl)
      (fun p hp => h p (by simp only [List.zip_cons_cons]; exact List.mem_cons_of_mem _ hp)) y hy
    have hb := h (a, a') (by simp)
    simp only [List.mem_cons]
    rcases hb with he | ⟨_, _, _, _, h1, h2⟩
    · simp only at he; subst he; exact or_congr Iff.rfl ih
    · constructor
      · rintro (rfl | h)
        · exact absurd hy h1
        · exact Or.inr (ih.1 h)
      · rintro (rfl | h)
        · exact absurd hy h2
        · exact Or.inr (ih.2 h)
  | [], _ :: _, hl, _, _, _ | _ :: _, [], hl, _, _, _ => by simp at hl

theorem NamesR.same {pkg : List String} {ns ns' : List String} (h : NamesR pkg ns ns') :
    ∀ y ∈ pkg, y ∈ ns ↔ y ∈ ns' := zip_same h.1 h.2

theorem NamesR.push {pkg : List String} {Γ : Ren} {b b' ns ns' : List String}
    (hs : MenScope pkg Γ b b') (h : NamesR pkg ns ns') :
    MenScope pkg (Γ.pushAll (ns.zip ns')) (ns ++ b) (ns' ++ b') :=
  hs.push (fun p hp => ⟨h.2 p hp, (List.of_mem_zip hp).1⟩) h.same

/-! ### Pattern names -/

/-- A slot's name on the one side and on the other. -/
def Slot.l : Slot → String
  | .bind x _ => x
  | .maybe x => x

def Slot.r : Slot → String
  | .bind _ x' => x'
  | .maybe x => x

/-- A slot is a renamed binder. -/
def Slot.binder (pkg : List String) : Slot → Prop
  | .bind x x' => BinderR pkg x x'
  | .maybe _ => True

theorem patNames_tuple (ps : List Pat) : patNames (.tuple ps) = ps.flatMap patNames := by
  rw [patNames]
theorem patNames_extract (c : String) (ps : List Pat) :
    patNames (.extract c ps) = ps.flatMap patNames := by
  rw [patNames]

mutual
theorem patR_names {pkg : List String} {Γ : Ren} : ∀ {p p' : Pat} {ss : List Slot},
    PatR pkg Γ p p' ss → patNames p = ss.map Slot.l ∧ patNames p' = ss.map Slot.r ∧
      ∀ s ∈ ss, s.binder pkg
  | _, _, _, .wild | _, _, _, .lit _ | _, _, _, .unsupported _ => by simp [patNames]
  | _, _, _, .bind h1 h2 h3 h4 h5 h6 => by
    simp only [patNames, List.map_cons, List.map_nil, Slot.l, Slot.r, List.mem_singleton,
      forall_eq, Slot.binder, true_and]
    exact Or.inr ⟨h1, h2, h3, h4, h5, h6⟩
  | _, _, _, .same _ => by simp [patNames, Slot.l, Slot.r, Slot.binder]
  | _, _, _, .tuple h => by rw [patNames_tuple, patNames_tuple]; exact patsR_names h
  | _, _, _, .extract _ h => by rw [patNames_extract, patNames_extract]; exact patsR_names h
theorem patsR_names {pkg : List String} {Γ : Ren} : ∀ {ps ps' : List Pat} {ss : List Slot},
    PatsR pkg Γ ps ps' ss → ps.flatMap patNames = ss.map Slot.l ∧
      ps'.flatMap patNames = ss.map Slot.r ∧ ∀ s ∈ ss, s.binder pkg
  | _, _, _, .nil => by simp
  | _, _, _, .cons h t => by
    obtain ⟨a1, a2, a3⟩ := patR_names h
    obtain ⟨b1, b2, b3⟩ := patsR_names t
    simp only [List.flatMap_cons, List.map_append, a1, a2, b1, b2, true_and]
    intro s hs
    rcases List.mem_append.mp hs with hs | hs
    · exact a3 s hs
    · exact b3 s hs
end

theorem outs_mem {pkg : List String} : ∀ {ss : List Slot}, (∀ s ∈ ss, s.binder pkg) →
    ∀ Δ ∈ Slot.outs ss, ∀ p ∈ Δ, BinderR pkg p.1 p.2 ∧ p.1 ∈ ss.map Slot.l
  | [], _, Δ, hΔ, p, hp => by simp [Slot.outs] at hΔ; subst hΔ; simp at hp
  | s :: ss, hok, Δ, hΔ, p, hp => by
    have ih := outs_mem (ss := ss) (fun s hs => hok s (List.mem_cons_of_mem _ hs))
    have hs := hok s List.mem_cons_self
    cases s with
    | bind x x' =>
      simp only [Slot.outs, List.mem_map] at hΔ
      obtain ⟨Δ', hΔ', rfl⟩ := hΔ
      rcases List.mem_cons.mp hp with rfl | hp
      · exact ⟨hs, by simp [Slot.l]⟩
      · have := ih Δ' hΔ' p hp
        exact ⟨this.1, List.mem_cons_of_mem _ this.2⟩
    | maybe x =>
      simp only [Slot.outs, List.mem_append, List.mem_map] at hΔ
      rcases hΔ with ⟨Δ', hΔ', rfl⟩ | hΔ
      · rcases List.mem_cons.mp hp with rfl | hp
        · exact ⟨Or.inl rfl, by simp [Slot.l]⟩
        · have := ih Δ' hΔ' p hp
          exact ⟨this.1, List.mem_cons_of_mem _ this.2⟩
      · have := ih Δ hΔ p hp
        exact ⟨this.1, List.mem_cons_of_mem _ this.2⟩

/-- The slots' names agree on package-level names: a renamed binder is none. -/
theorem slots_same {pkg : List String} : ∀ {ss : List Slot}, (∀ s ∈ ss, s.binder pkg) →
    ∀ y ∈ pkg, y ∈ ss.map Slot.l ↔ y ∈ ss.map Slot.r
  | [], _, _, _ => Iff.rfl
  | s :: ss, hok, y, hy => by
    have ih := slots_same (ss := ss) (fun s hs => hok s (List.mem_cons_of_mem _ hs)) y hy
    have hs := hok s List.mem_cons_self
    simp only [List.map_cons, List.mem_cons]
    cases s with
    | maybe x => exact or_congr Iff.rfl ih
    | bind x x' =>
      simp only [Slot.l, Slot.r]
      rcases hs with rfl | ⟨_, _, _, _, h1, h2⟩
      · exact or_congr Iff.rfl ih
      · constructor
        · rintro (rfl | h)
          · exact absurd hy h1
          · exact Or.inr (ih.1 h)
        · rintro (rfl | h)
          · exact absurd hy h2
          · exact Or.inr (ih.2 h)

/-- Some way the slots go: each binds. -/
def Slot.all : List Slot → Ren
  | [] => []
  | s :: ss => (s.l, s.r) :: Slot.all ss

theorem all_mem_outs : ∀ ss : List Slot, Slot.all ss ∈ Slot.outs ss
  | [] => by simp [Slot.all, Slot.outs]
  | .bind x x' :: ss => by
    simp only [Slot.all, Slot.outs, List.mem_map, Slot.l, Slot.r]
    exact ⟨_, all_mem_outs ss, rfl⟩
  | .maybe x :: ss => by
    simp only [Slot.all, Slot.outs, List.mem_append, List.mem_map, Slot.l, Slot.r]
    exact Or.inl ⟨_, all_mem_outs ss, rfl⟩

/-- The pattern names a `match` arm's `mentions` lists. -/
theorem menR_patNames {pkg : List String} {b b' : List String} (hb : ∀ y ∈ pkg, y ∈ b ↔ y ∈ b') :
    ∀ {ss : List Slot}, (∀ s ∈ ss, s.binder pkg) →
      inPkg pkg ((ss.map Slot.l).filter (!b.contains ·)) = inPkg pkg ((ss.map Slot.r).filter (!b'.contains ·))
  | [], _ => rfl
  | s :: ss, hok => by
    have ih := menR_patNames hb (ss := ss) (fun s hs => hok s (List.mem_cons_of_mem _ hs))
    have hs := hok s List.mem_cons_self
    simp only [List.map_cons, List.filter_cons]
    have key : (s.l ∈ pkg ∨ s.r ∈ pkg) → s.l = s.r := by
      intro h
      cases s with
      | maybe x => rfl
      | bind x x' =>
        rcases hs with he | ⟨_, _, _, _, h1, h2⟩
        · exact he
        · rcases h with h | h
          · exact absurd h h1
          · exact absurd h h2
    by_cases hp : s.l ∈ pkg ∨ s.r ∈ pkg
    · have he := key hp
      have hl : s.l ∈ pkg := by rcases hp with h | h; exact h; rw [he]; exact h
      have hc : b.contains s.l = b'.contains s.r := by
        rw [← he]; exact Bool.eq_iff_iff.mpr (by simp only [contains_iff]; exact hb _ hl)
      rw [hc]
      split
      · rw [← he]
        simp only [inPkg, List.filter_cons, hl, decide_true, ↓reduceIte, List.cons.injEq, true_and]
        exact ih
      · exact ih
    · simp only [not_or] at hp
      split <;> split <;> simp only [inPkg, List.filter_cons, hp.1, hp.2, decide_false,
        Bool.false_eq_true, ↓reduceIte] <;> exact ih

/-! ### Omitted defaults -/
/-- Two omitted defaults related: the same slot, the expressions by the renaming, in related scopes. -/
def OmitR (pkg : List String) (pm : String → Ren) (a a' : Nat × List String × Expr) : Prop :=
  a.1 = a'.1 ∧ ∃ Γ, AlphaE pkg pm Γ a.2.2 a'.2.2 ∧ MenScope pkg Γ a.2.1 a'.2.1

theorem fall2_filterMap {S : α → β → Prop} {R : γ → δ → Prop} {F : α → Option γ} {G : β → Option δ}
    (h : ∀ a b, S a b → (F a = none ∧ G b = none) ∨ ∃ x y, F a = some x ∧ G b = some y ∧ R x y) :
    ∀ {l : List α} {l' : List β}, Fall2 S l l' → Fall2 R (l.filterMap F) (l'.filterMap G)
  | _, _, .nil => .nil
  | a :: _, b :: _, .cons hs t => by
    simp only [List.filterMap_cons]
    rcases h a b hs with ⟨h1, h2⟩ | ⟨x, y, h1, h2, hr⟩
    · rw [h1, h2]; exact fall2_filterMap h t
    · rw [h1, h2]; exact .cons hr (fall2_filterMap h t)

theorem fall2_zipIdx {P : Nat → α → β → Prop} :
    ∀ {l : List α} {l' : List β} (n : Nat), l.length = l'.length →
      (∀ j (hj : j < l.length) (hj' : j < l'.length), P (n + j) l[j] l'[j]) →
      Fall2 (fun a b => a.2 = b.2 ∧ P a.2 a.1 b.1) (l.zipIdx n) (l'.zipIdx n)
  | [], [], _, _, _ => .nil
  | a :: l, b :: l', n, hl, h => by
    simp only [List.zipIdx_cons]
    have h0 := h 0 (by simp) (by simp)
    simp only [List.getElem_cons_zero, Nat.add_zero] at h0
    refine .cons ⟨rfl, h0⟩ (fall2_zipIdx (n + 1) (by simpa using hl) ?_)
    intro j hj hj'
    have := h (j + 1) (by simpa using hj) (by simpa using hj')
    simpa [Nat.add_assoc, Nat.add_comm 1 j] using this
  | [], _ :: _, _, hl, _ | _ :: _, [], _, hl, _ => by simp at hl

/-- A named argument's position in a function's renamed parameters. -/
theorem idx_zip : ∀ {ps ps' : List String}, ps.length = ps'.length → ps'.Nodup →
    ∀ {n n' : String}, (ps.zip ps').lookup n = some n' → ps.idxOf? n = ps'.idxOf? n'
  | [], [], _, _, _, _, h => by simp at h
  | a :: ps, a' :: ps', hl, hnd, n, n', h => by
    simp only [List.zip_cons_cons, List.lookup_cons] at h
    simp only [List.idxOf?_cons]
    by_cases hn : n = a
    · subst hn
      simp only [beq_self_eq_true] at h
      cases h
      simp
    · have hb : (n == a) = false := by simp [hn]
      rw [hb] at h
      simp only at h
      have hna : (a == n) = false := by simp [Ne.symm hn]
      have hmem : n' ∈ ps' := (List.of_mem_zip (mem_of_lookup h)).2
      have hnd' := List.nodup_cons.mp hnd
      have hna' : (a' == n') = false := by
        simp only [beq_eq_false_iff_ne]; rintro rfl; exact hnd'.1 hmem
      rw [hna, hna', idx_zip (by simpa using hl) hnd'.2 h]
  | [], _ :: _, hl, _, _, _, _ | _ :: _, [], hl, _, _, _, _ => by simp at hl

/-- The positions a call's arguments give, on both sides. -/
theorem given_eq {pkg : List String} {pm : String → Ren} {m Γ : Ren} {g g' : Expr → Option Nat}
    (hpos : ∀ e, isNamed e = false → g e = none ∧ g' e = none)
    (hnamed : ∀ n n' v v', (m.lookup n = some n' ∨ (n = n' ∧ n ∉ m.map (·.1) ∧ n ∉ m.map (·.2))) →
      g (.named n v) = g' (.named n' v')) :
    ∀ {as as' : List Expr}, ArgsR pkg pm m Γ as as' →
      (as.filter (!isNamed ·)).length = (as'.filter (!isNamed ·)).length ∧
        as.filterMap g = as'.filterMap g'
  | _, _, .nil => ⟨rfl, rfl⟩
  | _, _, .pos (e := e) (e' := e') hn he t => by
    have hn' := alphaE_not_named he
    obtain ⟨ih1, ih2⟩ := given_eq hpos hnamed t
    simp only [List.filter_cons, hn, hn', Bool.not_false, ↓reduceIte, List.length_cons, ih1,
      List.filterMap_cons, (hpos e hn).1, (hpos e' hn').2, ih2, and_self]
  | _, _, .named (n := n) (n' := n') (v := v) (v' := v') hm _ t => by
    obtain ⟨ih1, ih2⟩ := given_eq hpos hnamed t
    have e1 : isNamed (.named n v) = true := rfl
    have e2 : isNamed (.named n' v') = true := rfl
    simp only [List.filter_cons, e1, e2, Bool.not_true, Bool.false_eq_true, ↓reduceIte, ih1,
      List.filterMap_cons, true_and]
    rw [hnamed _ _ _ _ hm, ih2]

theorem take_zip : ∀ (l : List α) (l' : List β) (j : Nat), (l.zip l').take j = (l.take j).zip (l'.take j)
  | [], _, j => by simp
  | _ :: _, [], j => by simp
  | a :: l, b :: l', 0 => rfl
  | a :: l, b :: l', j + 1 => by simp [take_zip l l' j]

theorem NamesR.take {pkg : List String} {ns ns' : List String} (h : NamesR pkg ns ns') (j : Nat) :
    NamesR pkg (ns.take j) (ns'.take j) :=
  ⟨by simp [h.1], fun p hp => h.2 p (by rw [← take_zip] at hp; exact List.mem_of_mem_take hp)⟩

theorem menScope_nil (pkg : List String) : MenScope pkg [] [] [] :=
  ⟨by simp, by simp, fun _ _ => Iff.rfl⟩

/-- An omitted default, or none, on both sides. -/
def OptOmitR (pkg : List String) (pm : String → Ren) (j : Nat) (sc sc' : List String) (o o' : Option Expr) : Prop :=
  (o = none ∧ o' = none) ∨ ∃ e e', o = some e ∧ o' = some e' ∧ OmitR pkg pm (j, sc, e) (j, sc', e')

theorem OptR.omit {pkg : List String} {pm : String → Ren} {Γ : Ren} {o o' : Option Expr} (j : Nat)
    {sc sc' : List String} (h : OptR pkg pm Γ o o') (hs : MenScope pkg Γ sc sc') :
    OptOmitR pkg pm j sc sc' o o' := by
  cases h with
  | none => exact Or.inl ⟨rfl, rfl⟩
  | some he => exact Or.inr ⟨_, _, rfl, rfl, rfl, Γ, he, hs⟩

/-- The defaults left out at the same given slots, related slot by slot. -/
theorem omittedSlots_rel {pkg : List String} {pm : String → Ren} (G : List Nat)
    {d d' : List (Option Expr)} {s s' : Nat → List String} (hl : d.length = d'.length)
    (hd : ∀ j, OptOmitR pkg pm j (s j) (s' j) (d.getD j none) (d'.getD j none)) :
    Fall2 (OmitR pkg pm) (omittedDefaults.omittedSlots G d s) (omittedDefaults.omittedSlots G d' s') := by
  unfold omittedDefaults.omittedSlots
  refine fall2_filterMap ?_ (fall2_zipIdx (P := fun j o o' => OptOmitR pkg pm j (s j) (s' j) o o') 0 hl
    (fun j hj hj' => by
      have := hd j
      simp only [List.getD_eq_getElem?_getD, List.getElem?_eq_getElem hj,
        List.getElem?_eq_getElem hj', Option.getD_some] at this
      simpa using this))
  rintro ⟨o, j⟩ ⟨o', j'⟩ ⟨hj, ho⟩
  simp only at hj ho
  subst hj
  by_cases hc : G.contains j = true
  · left; dsimp only; simp only [hc, ↓reduceIte, and_self]
  · simp only [hc, Bool.false_eq_true, ↓reduceIte]
    rcases ho with ⟨rfl, rfl⟩ | ⟨e, e', rfl, rfl, hr⟩
    · exact Or.inl ⟨rfl, rfl⟩
    · exact Or.inr ⟨_, _, rfl, rfl, hr⟩

/-- The slots a call's arguments give are the same on both sides when each named argument
    names the same slot. -/
theorem givenSlots_eq {pkg : List String} {pm : String → Ren} {m Γ : Ren} {ns ns' : List String}
    (hnamed : ∀ n n', (m.lookup n = some n' ∨ (n = n' ∧ n ∉ m.map (·.1) ∧ n ∉ m.map (·.2))) →
      ns.idxOf? n = ns'.idxOf? n')
    {as as' : List Expr} (ha : ArgsR pkg pm m Γ as as') :
    omittedDefaults.givenSlots ns as = omittedDefaults.givenSlots ns' as' := by
  unfold omittedDefaults.givenSlots
  have hpos : ∀ (names : List String) e, isNamed e = false → omittedDefaults.namedSlot names e = none :=
    fun names e he => by cases e <;> simp_all [isNamed, omittedDefaults.namedSlot]
  obtain ⟨g1, g2⟩ := given_eq (g := omittedDefaults.namedSlot ns) (g' := omittedDefaults.namedSlot ns')
    (fun e he => ⟨hpos _ e he, hpos _ e he⟩)
    (fun n n' v v' h => by simp only [omittedDefaults.namedSlot]; exact hnamed n n' h) ha
  rw [g1, g2]

/-- A function name on both sides: no function, or related functions. -/
theorem funcs_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') (x : String) :
    (ds.funcs.lookup x = none ∧ ds'.funcs.lookup x = none) ∨
      ∃ f f', ds.funcs.lookup x = some f ∧ ds'.funcs.lookup x = some f' ∧ FuncR pkg pm f f' :=
  lookup_fall2 (fun _ _ h => ⟨h.1, h.2.2⟩) x hD.funcs

/-- The defaults a call of `x` leaves out are related on both sides. -/
theorem omitted_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') (x : String) {Γ : Ren} {as as' : List Expr}
    (ha : ArgsR pkg pm (pm x) Γ as as') :
    Fall2 (OmitR pkg pm) (omittedDefaults ds x as) (omittedDefaults ds' x as') := by
  unfold omittedDefaults fieldsOf
  rcases funcs_rel hD x with
    ⟨h1, h2⟩ | ⟨f, f', h1, h2, hf⟩
  · -- Not a function: a struct or a case, the same on both sides.
    rw [h1, h2]
    have hm : pm x = [] := hD.noFunc x h1
    rw [hm] at ha
    have hfields : ∀ fs : List Field, Fall2 (FieldR pkg pm) fs fs →
        Fall2 (OmitR pkg pm)
          (omittedDefaults.omittedSlots (omittedDefaults.givenSlots (fs.map (·.name)) as)
            (fs.map (·.default)) fun _ => [])
          (omittedDefaults.omittedSlots (omittedDefaults.givenSlots (fs.map (·.name)) as')
            (fs.map (·.default)) fun _ => []) := by
      intro fs h
      rw [givenSlots_eq (ns' := fs.map (·.name)) (fun n n' hn => by
        rcases hn with hn | ⟨rfl, _⟩
        · simp at hn
        · rfl) ha]
      exact omittedSlots_rel _ rfl fun j => (fieldDefaults_self h j).omit j (menScope_nil pkg)
    rcases lookup_fall2 (S := fun (a b : String × List Field) => a.1 = b.1 ∧ Fall2 (FieldR pkg pm) a.2 b.2)
        (R := Fall2 (FieldR pkg pm)) (fun _ _ h => h) x hD.structs with
      ⟨s1, s2⟩ | ⟨fs, fs', s1, s2, hfs⟩
    · simp only [s1, s2]
      rcases variant_rel hD x with ⟨v1, v2⟩ | ⟨ty, v, w, v1, v2, hvw⟩
      · simp only [v1, v2, Option.map_none]; exact .nil
      · simp only [v1, v2, Option.map_some]
        have he := VarR.eq hvw
        subst he
        exact hfields _ hvw.2.2
    · simp only [s1, s2]
      have he := fieldsR_eq hfs
      subst he
      exact hfields _ hfs
  · -- A function: its parameters renamed, a named argument following them.
    rw [h1, h2]
    dsimp only
    have hx := funcs_name hD h1
    have hmap : pm x = pairsOf (f.params.map (·.name)) (f'.params.map (·.name)) := hx ▸ hf.map
    rw [hmap] at ha
    have hl := hf.names.1
    rw [givenSlots_eq (ns' := f'.params.map (·.name)) (fun n n' hn => by
      rcases hn with hn | ⟨rfl, h1, h2⟩
      · exact idx_zip hl hf.nodup.2 hn
      · have e1 : (pairsOf (f.params.map (·.name)) (f'.params.map (·.name))).map (·.1) =
            f.params.map (·.name) := List.map_fst_zip (by omega)
        have e2 : (pairsOf (f.params.map (·.name)) (f'.params.map (·.name))).map (·.2) =
            f'.params.map (·.name) := List.map_snd_zip (by omega)
        rw [e1] at h1; rw [e2] at h2
        rw [List.idxOf?_eq_none_iff.mpr h1, List.idxOf?_eq_none_iff.mpr h2]) ha]
    refine omittedSlots_rel _ hf.dlen fun j => (hf.defaults j).omit j ?_
    have := NamesR.push (menScope_nil pkg) (hf.names.take j)
    simp only [List.append_nil] at this
    unfold pairsOf
    rw [take_zip]
    exact this

/-- A name outside `pkg` names no function, struct or case, on either side, so a call of it
    leaves no default out. -/
theorem omitted_nil {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') {x : String} (hx : x ∉ pkg) (as as' : List Expr) :
    omittedDefaults ds x as = [] ∧ omittedDefaults ds' x as' = [] := by
  have hf : ds.funcs.lookup x = none := by
    cases h : ds.funcs.lookup x with
    | none => rfl
    | some _ => exact absurd (hD.inPkg x (Or.inl (by simp [h]))) hx
  have hs : ds.structs.lookup x = none := by
    cases h : ds.structs.lookup x with
    | none => rfl
    | some _ => exact absurd (hD.inPkg x (Or.inr (Or.inl (by simp [h])))) hx
  have hv : ds.variant? x = none := by
    cases h : ds.variant? x with
    | none => rfl
    | some _ => exact absurd (hD.inPkg x (Or.inr (Or.inr (by simp [h])))) hx
  have hf' : ds'.funcs.lookup x = none := by
    rcases funcs_rel hD x with
      ⟨_, h2⟩ | ⟨_, _, h1, _, _⟩
    · exact h2
    · rw [hf] at h1; cases h1
  have hs' : ds'.structs.lookup x = none := by rw [← hD.structs_eq]; exact hs
  have hv' : ds'.variant? x = none := by
    rcases variant_rel hD x with ⟨_, h2⟩ | ⟨_, _, _, h1, _, _⟩
    · exact h2
    · rw [hv] at h1; cases h1
  unfold omittedDefaults fieldsOf
  simp only [hf, hs, hv, hf', hs', hv', Option.map_none, and_self]

/-- `mentionsUpTo` at budget `k` commutes with the renaming. -/
def MenStmt (pkg : List String) (pm : String → Ren) (ds ds' : Decls) (k : Nat) : Prop :=
  ∀ {c : List (String × Nat)} {Γ : Ren} {e e' : Expr} {b b' : List String}, AlphaE pkg pm Γ e e' →
    MenScope pkg Γ b b' → MenR pkg (mentionsUpTo ds k c b e) (mentionsUpTo ds' k c b' e')

theorem MenR.self_nil {pkg : List String} {m m' : List String} (h : m = []) (h' : m' = []) :
    MenR pkg m m' := by subst h h'; exact MenR.nil

/-- The omitted defaults' mentions: each cut off on both sides (a slot the chain is already
    expanding, or the budget spent), or expanded one budget down. -/
theorem menR_omitted {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {k : Nat}
    (hk : ∀ j, k = j + 1 → MenStmt pkg pm ds ds' j) (x : String) (c : List (String × Nat))
    {F F' : Nat × List String × Expr → List String}
    (hF : ∀ j sc e, F (j, sc, e) = if c.contains (x, j) then [] else
      match k with | 0 => [] | k + 1 => mentionsUpTo ds k ((x, j) :: c) sc e)
    (hF' : ∀ j sc e, F' (j, sc, e) = if c.contains (x, j) then [] else
      match k with | 0 => [] | k + 1 => mentionsUpTo ds' k ((x, j) :: c) sc e) :
    ∀ {l l' : List (Nat × List String × Expr)}, Fall2 (OmitR pkg pm) l l' →
      Fall2 (MenR pkg) (l.map F) (l'.map F')
  | _, _, .nil => .nil
  | (j, sc, e) :: _, (j', sc', e') :: _, .cons ⟨hj, _, he, hs⟩ t => by
    simp only at hj
    subst hj
    simp only [List.map_cons, hF, hF']
    refine .cons ?_ (menR_omitted hk x c hF hF' t)
    split
    · exact MenR.nil
    · cases k with
      | zero => exact MenR.nil
      | succ k => exact hk k rfl he hs

section
variable {pkg : List String} {pm : String → Ren} {ds ds' : Decls}

/-- The declared field defaults a declaration with no value evaluates: the same fields on
    both sides, each default related to itself with no local in scope. -/
theorem declDefaults_rel (hD : DeclsR pkg pm ds ds') (ty : Option Ty) :
    Fall2 (fun a b => a.1 = b.1 ∧ a.2.1 = b.2.1 ∧ AlphaE pkg pm [] a.2.2 b.2.2)
      (declDefaults ds ty) (declDefaults ds' ty) := by
  unfold declDefaults
  cases ty with
  | none => exact .nil
  | some t =>
    dsimp only
    simp only [checkTy_congr hD.same, defaultOf_congr hD.same]
    split
    · refine fall2_filterMap (S := Eq) (fun h _ e => ?_) (fall2_refl (fun _ => rfl) _)
      subst e
      obtain ⟨x, j, _⟩ := h
      have hr := fieldDefault_rel hD x j
      generalize fieldDefault ds x j = o at hr ⊢
      generalize fieldDefault ds' x j = o' at hr ⊢
      cases hr with
      | none => exact Or.inl ⟨rfl, rfl⟩
      | some he => exact Or.inr ⟨_, _, rfl, rfl, rfl, rfl, he⟩
    · exact .nil

/-- Declared defaults keyed by their field's owner and slot, related on both sides, each
    cut off on both sides (a key the chain is already expanding, or the budget spent) or
    expanded one budget down with no local in scope. -/
theorem menR_keyed {k : Nat} (hk : ∀ j, k = j + 1 → MenStmt pkg pm ds ds' j)
    (c : List (String × Nat)) {l l' : List (String × Nat × Expr)}
    (h : Fall2 (fun a b => a.1 = b.1 ∧ a.2.1 = b.2.1 ∧ AlphaE pkg pm [] a.2.2 b.2.2) l l') :
    MenR pkg
      (l.flatMap fun x => if c.contains (x.1, x.2.1) then [] else
        match k with | 0 => [] | k + 1 => mentionsUpTo ds k ((x.1, x.2.1) :: c) [] x.2.2)
      (l'.flatMap fun x => if c.contains (x.1, x.2.1) then [] else
        match k with | 0 => [] | k + 1 => mentionsUpTo ds' k ((x.1, x.2.1) :: c) [] x.2.2) := by
  refine MenR.all (fall2_map (fun a b ⟨h1, h2, he⟩ => ?_) h)
  rw [h1, h2]
  split
  · exact MenR.nil
  · cases k with
    | zero => exact MenR.nil
    | succ k => exact hk k rfl he (menScope_nil pkg)

theorem fall2_cat {R : α → β → Prop} :
    ∀ {as as' : List α} {bs bs' : List β}, Fall2 R as bs → Fall2 R as' bs' → Fall2 R (as ++ as') (bs ++ bs')
  | _, _, _, _, .nil, h => h
  | _, _, _, _, .cons hab h, h' => .cons hab (fall2_cat h h')

theorem fall2_flatMap_self {R : β → γ → Prop} {F : α → List β} {G : α → List γ} :
    ∀ {l : List α}, (∀ a ∈ l, Fall2 R (F a) (G a)) → Fall2 R (l.flatMap F) (l.flatMap G)
  | [], _ => .nil
  | a :: _, h => by
    simp only [List.flatMap_cons]
    exact fall2_cat (h a (by simp)) (fall2_flatMap_self fun b hb => h b (by simp [hb]))

/-- The declared field defaults a construction evaluates for the fields it leaves out that
    declare none: the same fields on both sides, each default related to itself with no
    local in scope. -/
theorem omittedTypeDefaults_rel (hD : DeclsR pkg pm ds ds') (x : String) {Γ : Ren}
    {as as' : List Expr} (ha : ArgsR pkg pm (pm x) Γ as as') :
    Fall2 (fun a b => a.1 = b.1 ∧ a.2.1 = b.2.1 ∧ AlphaE pkg pm [] a.2.2 b.2.2)
      (omittedTypeDefaults ds x as) (omittedTypeDefaults ds' x as') := by
  unfold omittedTypeDefaults
  rcases funcs_rel hD x with ⟨h1, h2⟩ | ⟨_, _, h1, h2, _⟩
  · rw [h1, h2]
    rw [hD.noFunc x h1] at ha
    have hf : fieldsOf ds x = fieldsOf ds' x := by
      simp only [fieldsOf, Decls.variant?, hD.structs_eq, hD.sealeds_eq]
    dsimp only
    rw [← hf]
    cases fieldsOf ds x with
    | none => exact .nil
    | some fs =>
      dsimp only
      rw [givenSlots_eq (ns' := fs.map (·.name)) (fun n n' hn => by
        rcases hn with hn | ⟨rfl, _⟩
        · simp at hn
        · rfl) ha]
      exact fall2_flatMap_self fun f _ => declDefaults_rel hD f.1.type
  · rw [h1, h2]; exact .nil

/-- A name outside `pkg` names no struct or case, on either side, so a call of it leaves no
    field's default value to build. -/
theorem omittedTypeDefaults_nil (hD : DeclsR pkg pm ds ds') {x : String} (hx : x ∉ pkg)
    (as as' : List Expr) : omittedTypeDefaults ds x as = [] ∧ omittedTypeDefaults ds' x as' = [] := by
  have hs : ds.structs.lookup x = none := by
    cases h : ds.structs.lookup x with
    | none => rfl
    | some _ => exact absurd (hD.inPkg x (Or.inr (Or.inl (by simp [h])))) hx
  have hv : ds.variant? x = none := by
    cases h : ds.variant? x with
    | none => rfl
    | some _ => exact absurd (hD.inPkg x (Or.inr (Or.inr (by simp [h])))) hx
  have hs' : ds'.structs.lookup x = none := by rw [← hD.structs_eq]; exact hs
  have hv' : ds'.variant? x = none := by
    rw [show ds'.variant? x = ds.variant? x by simp only [Decls.variant?, hD.sealeds_eq], hv]
  unfold omittedTypeDefaults fieldsOf
  simp only [hs, hv, hs', hv', Option.map_none]
  constructor <;> split <;> rfl

/-- What a call of a name adds to its mentions, on both sides, when the name is in `pkg` and
    the same on both sides, and the arguments follow its parameter renaming: the defaults it
    leaves out, and those in the default values of the fields it leaves out that declare
    none. -/
theorem menR_callTail (hD : DeclsR pkg pm ds ds') {k : Nat} (hk : ∀ j, k = j + 1 → MenStmt pkg pm ds ds' j)
    (c : List (String × Nat)) {Γ : Ren} {b b' : List String} {f : String} (hb : f ∈ b ↔ f ∈ b')
    {as as' : List Expr} (ha : ArgsR pkg pm (pm f) Γ as as') {F F' : Nat × List String × Expr → List String}
    (hF : ∀ j sc e, F (j, sc, e) = if c.contains (f, j) then [] else
      match k with | 0 => [] | k + 1 => mentionsUpTo ds k ((f, j) :: c) sc e)
    (hF' : ∀ j sc e, F' (j, sc, e) = if c.contains (f, j) then [] else
      match k with | 0 => [] | k + 1 => mentionsUpTo ds' k ((f, j) :: c) sc e) :
    MenR pkg
      (if b.contains f then [] else (omittedDefaults ds f as).flatMap F ++
        (omittedTypeDefaults ds f as).flatMap fun x => if c.contains (x.1, x.2.1) then [] else
          match k with | 0 => [] | k + 1 => mentionsUpTo ds k ((x.1, x.2.1) :: c) [] x.2.2)
      (if b'.contains f then [] else (omittedDefaults ds' f as').flatMap F' ++
        (omittedTypeDefaults ds' f as').flatMap fun x => if c.contains (x.1, x.2.1) then [] else
          match k with | 0 => [] | k + 1 => mentionsUpTo ds' k ((x.1, x.2.1) :: c) [] x.2.2) := by
  have hc : b.contains f = b'.contains f := Bool.eq_iff_iff.mpr (by simp only [contains_iff]; exact hb)
  rw [hc]
  split
  · exact MenR.nil
  · exact (MenR.all (menR_omitted hk f c hF hF' (omitted_rel hD f ha))).cat
      (menR_keyed hk c (omittedTypeDefaults_rel hD f ha))

theorem alphaL_isEmpty {Γ : Ren} {es es' : List Expr} (h : AlphaL pkg pm Γ es es') :
    es.isEmpty = es'.isEmpty := by
  cases h <;> rfl

theorem alphaE_not_decl {Γ : Ren} {e e' : Expr} (h : AlphaE pkg pm Γ e e') :
    ∀ m ns d ty vs, e ≠ .decl m ns d ty vs := by
  intro m ns d ty vs he; subst he; cases h

theorem menR_nameE {k : Nat} {c : List (String × Nat)} {Γ : Ren} {b b' : List String} {x x' : String}
    (hs : MenScope pkg Γ b b') (hx : NameR Γ x x') :
    MenR pkg (mentionsUpTo ds k c b (.name x)) (mentionsUpTo ds' k c b' (.name x')) := by
  unfold mentionsUpTo; exact menR_name hs hx

theorem alphaE_not_decl_right {Γ : Ren} {e e' : Expr} (h : AlphaE pkg pm Γ e e') :
    ∀ m ns d ty vs, e' ≠ .decl m ns d ty vs := by
  intro m ns d ty vs he; subst he; cases h

/-- `mentionsUpTo` at budget `k` commutes with the renaming on renamings of `e`. -/
abbrev MenP (pkg : List String) (pm : String → Ren) (ds ds' : Decls) (k : Nat) (c : List (String × Nat)) (e : Expr) : Prop :=
  ∀ {Γ : Ren} {e' : Expr} {b b' : List String}, AlphaE pkg pm Γ e e' → MenScope pkg Γ b b' →
    MenR pkg (mentionsUpTo ds k c b e) (mentionsUpTo ds' k c b' e')

section
variable {k N : Nat} {c : List (String × Nat)} (ih : ∀ e : Expr, sizeOf e < N → MenP pkg pm ds ds' k c e)
include ih

theorem mentions_alphaL : ∀ {Γ : Ren} {es es' : List Expr} {b b' : List String}, sizeOf es ≤ N →
    AlphaL pkg pm Γ es es' → MenScope pkg Γ b b' →
      Fall2 (MenR pkg) (es.map (mentionsUpTo ds k c b)) (es'.map (mentionsUpTo ds' k c b'))
  | _, _, _, _, _, _, .nil, _ => .nil
  | _, _, _, _, _, hsz, .cons h t, hs => by
    simp only [List.cons.sizeOf_spec] at hsz
    exact .cons (ih _ (by omega) h hs) (mentions_alphaL (by omega) t hs)

theorem mentions_argsR : ∀ {m Γ : Ren} {as as' : List Expr} {b b' : List String}, sizeOf as ≤ N →
    ArgsR pkg pm m Γ as as' → MenScope pkg Γ b b' →
      Fall2 (MenR pkg) (as.map (mentionsUpTo ds k c b)) (as'.map (mentionsUpTo ds' k c b'))
  | _, _, _, _, _, _, _, .nil, _ => .nil
  | _, _, _, _, _, _, hsz, .pos _ h t, hs => by
    simp only [List.cons.sizeOf_spec] at hsz
    exact .cons (ih _ (by omega) h hs) (mentions_argsR (by omega) t hs)
  | _, _, _, _, _, _, hsz, .named _ h t, hs => by
    simp only [List.cons.sizeOf_spec, Expr.named.sizeOf_spec] at hsz
    refine .cons ?_ (mentions_argsR (by omega) t hs)
    unfold mentionsUpTo; (try dsimp only)
    exact ih _ (by omega) h hs

theorem mentions_blockR (hD : DeclsR pkg pm ds ds') (hk : ∀ j, k = j + 1 → MenStmt pkg pm ds ds' j)
    {F F' : List String × List String → Expr → List String × List String}
    (hF1 : ∀ b acc m ns d ty vs, F (b, acc) (.decl m ns d ty vs) =
      (ns ++ b, acc ++ mentionsUpTo ds k c b (.decl m ns d ty vs)))
    (hF1' : ∀ b acc m ns d ty vs, F' (b, acc) (.decl m ns d ty vs) =
      (ns ++ b, acc ++ mentionsUpTo ds' k c b (.decl m ns d ty vs)))
    (hF2 : ∀ b acc e, (∀ m ns d ty vs, e ≠ .decl m ns d ty vs) →
      F (b, acc) e = (b, acc ++ mentionsUpTo ds k c b e))
    (hF2' : ∀ b acc e, (∀ m ns d ty vs, e ≠ .decl m ns d ty vs) →
      F' (b, acc) e = (b, acc ++ mentionsUpTo ds' k c b e)) :
    ∀ {Γ : Ren} {ss ss' : List Expr} {b b' : List String} {acc acc' : List String}, sizeOf ss ≤ N →
      BlockR pkg pm Γ ss ss' → MenScope pkg Γ b b' → MenR pkg acc acc' →
      MenR pkg (ss.foldl F (b, acc)).2 (ss'.foldl F' (b', acc')).2
  | _, _, _, _, _, _, _, _, .nil, _, hacc => hacc
  | _, _, _, _, _, _, _, hsz, .decl _ _ _ hn hvs hrest, hs, hacc => by
    simp only [List.cons.sizeOf_spec, Expr.decl.sizeOf_spec] at hsz
    simp only [List.foldl_cons, hF1, hF1']
    refine mentions_blockR hD hk hF1 hF1' hF2 hF2' (by omega) hrest (hn.push hs) (hacc.cat ?_)
    unfold mentionsUpTo; (try dsimp only)
    refine (MenR.all (mentions_alphaL ih (by omega) hvs hs)).cat ?_
    rw [alphaL_isEmpty hvs]
    split
    · exact MenR.nil
    · exact menR_keyed hk c (declDefaults_rel hD _)
  | _, _, _, _, _, _, _, hsz, .stmt he hrest, hs, hacc => by
    simp only [List.cons.sizeOf_spec] at hsz
    simp only [List.foldl_cons]
    rw [hF2 _ _ _ (alphaE_not_decl he), hF2' _ _ _ (alphaE_not_decl_right he)]
    exact mentions_blockR hD hk hF1 hF1' hF2 hF2' (by omega) hrest hs (hacc.cat (ih _ (by omega) he hs))

theorem mentions_casesR :
    ∀ {Γ : Ren} {cs cs' : List (Pat × Option Expr × Expr)} {b b' : List String}
      {A A' : Pat × Option Expr × Expr → List String}, sizeOf cs ≤ N →
      CasesR pkg pm Γ cs cs' → MenScope pkg Γ b b' →
      (∀ p bd, A (p, none, bd) = (patNames p).filter (!b.contains ·) ++
        [] ++ mentionsUpTo ds k c (patNames p ++ b) bd) →
      (∀ p bd, A' (p, none, bd) = (patNames p).filter (!b'.contains ·) ++
        [] ++ mentionsUpTo ds' k c (patNames p ++ b') bd) →
      (∀ p g bd, A (p, some g, bd) = (patNames p).filter (!b.contains ·) ++
        mentionsUpTo ds k c (patNames p ++ b) g ++ mentionsUpTo ds k c (patNames p ++ b) bd) →
      (∀ p g bd, A' (p, some g, bd) = (patNames p).filter (!b'.contains ·) ++
        mentionsUpTo ds' k c (patNames p ++ b') g ++ mentionsUpTo ds' k c (patNames p ++ b') bd) →
      Fall2 (MenR pkg) (cs.map A) (cs'.map A')
  | _, _, _, _, _, _, _, _, .nil, _, _, _, _, _ => .nil
  | Γ, _, _, b, b', _, _, hsz, .cons hp hg hb hrest,
      hs, hA1, hA1', hA2, hA2' => by
    rename_i p g bd _ p' _ _ ss _
    simp only [List.cons.sizeOf_spec, Prod.mk.sizeOf_spec] at hsz
    obtain ⟨n1, n2, hok⟩ := patR_names hp
    have hs' : MenScope pkg (Γ.pushAll (Slot.all ss)) (patNames p ++ b) (patNames p' ++ b') := by
      rw [n1, n2]
      exact hs.push (outs_mem hok _ (all_mem_outs ss)) (slots_same hok)
    have hpat : MenR pkg ((patNames p).filter (!b.contains ·)) ((patNames p').filter (!b'.contains ·)) := by
      unfold MenR; rw [n1, n2]; exact menR_patNames hs.same hok
    refine .cons ?_ (mentions_casesR (by omega) hrest hs hA1 hA1' hA2 hA2')
    have hgd := hg _ (all_mem_outs ss)
    have hbd := ih bd (by omega) (hb _ (all_mem_outs ss)) hs'
    cases hgd with
    | none => rw [hA1, hA1']; exact (hpat.cat MenR.nil).cat hbd
    | some he =>
      rw [hA2, hA2']
      simp only [Option.some.sizeOf_spec] at hsz
      exact (hpat.cat (ih _ (by omega) he hs')).cat hbd

end

/-- `mentionsUpTo` at budget `k` commutes with the renaming, given that it does one budget
    down, by induction on the size of the expression. -/
theorem mentions_lt (hD : DeclsR pkg pm ds ds') {k : Nat} {c : List (String × Nat)}
    (hk : ∀ j, k = j + 1 → MenStmt pkg pm ds ds' j) :
    ∀ n (e : Expr), sizeOf e < n → MenP pkg pm ds ds' k c e
  | 0, _, h => absurd h (Nat.not_lt_zero _)
  | n + 1, e, hlt => by
    have ih := mentions_lt hD (c := c) hk n
    intro he hs
    rename_i Γ e' b b'
    cases he with
    | lit _ | unsupported _ => unfold mentionsUpTo; (try dsimp only); exact MenR.nil
    | name hx => exact menR_nameE hs hx
    | unary _ h | paren h | select _ h | incdec _ h =>
      simp only [Expr.unary.sizeOf_spec, Expr.paren.sizeOf_spec, Expr.select.sizeOf_spec,
        Expr.incdec.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only); exact ih _ (by omega) h hs
    | binary _ hl hr =>
      simp only [Expr.binary.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact (ih _ (by omega) hl hs).cat (ih _ (by omega) hr hs)
    | tuple h | interp h =>
      simp only [Expr.tuple.sizeOf_spec, Expr.interp.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact MenR.all (mentions_alphaL ih (by omega) h hs)
    | ifExpr hc ha hb =>
      simp only [Expr.ifExpr.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact ((ih _ (by omega) hc hs).cat (ih _ (by omega) ha hs)).cat (ih _ (by omega) hb hs)
    | ifStmt hc ha hb =>
      simp only [Expr.ifStmt.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      refine ((ih _ (by omega) hc hs).cat (ih _ (by omega) ha hs)).cat ?_
      cases hb with
      | none => exact MenR.nil
      | some he =>
        simp only [Option.some.sizeOf_spec] at hlt
        exact ih _ (by omega) he hs
    | ret hv =>
      cases hv with
      | none => unfold mentionsUpTo; (try dsimp only); exact MenR.nil
      | some he =>
        simp only [Expr.ret.sizeOf_spec, Option.some.sizeOf_spec] at hlt
        unfold mentionsUpTo; (try dsimp only); exact ih _ (by omega) he hs
    | assign _ hts hvs =>
      simp only [Expr.assign.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact (MenR.all (mentions_alphaL ih (by omega) hts hs)).cat
        (MenR.all (mentions_alphaL ih (by omega) hvs hs))
    | lambda _ _ hns hb =>
      simp only [Expr.lambda.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact ih _ (by omega) hb (hns.push hs)
    | block h =>
      simp only [Expr.block.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact mentions_blockR ih hD hk (fun _ _ _ _ _ _ _ => rfl) (fun _ _ _ _ _ _ _ => rfl)
        (fun _ _ e he => by cases e <;> first | rfl | exact absurd rfl (he _ _ _ _ _))
        (fun _ _ e he => by cases e <;> first | rfl | exact absurd rfl (he _ _ _ _ _))
        (by omega) h hs MenR.nil
    | mtch hsub hcs =>
      simp only [Expr.mtch.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      exact (ih _ (by omega) hsub hs).cat (MenR.all (mentions_casesR ih (by omega) hcs hs
        (fun _ _ => rfl) (fun _ _ => rfl) (fun _ _ _ => rfl) (fun _ _ _ => rfl)))
    | callFree hf ha =>
      rename_i f as as'
      simp only [Expr.call.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      refine ((menR_nameE hs (Or.inr ⟨rfl, hf⟩)).cat
        (MenR.all (mentions_argsR ih (by omega) ha hs))).cat ?_
      by_cases hp : f ∈ pkg
      · exact menR_callTail hD hk c (hs.same f hp) ha (fun _ _ _ => rfl) (fun _ _ _ => rfl)
      · obtain ⟨o1, o2⟩ := omitted_nil hD hp as as'
        obtain ⟨t1, t2⟩ := omittedTypeDefaults_nil hD hp as as'
        refine MenR.self_nil ?_ ?_ <;> simp [o1, o2, t1, t2]
    | call hnf hc ha =>
      rename_i c c' as as'
      simp only [Expr.call.sizeOf_spec] at hlt
      unfold mentionsUpTo; (try dsimp only)
      refine ((ih _ (by omega) hc hs).cat (MenR.all (mentions_argsR ih (by omega) ha hs))).cat ?_
      cases hc with
      | name hx =>
        rename_i x x'
        (try dsimp only)
        have hb : Γ.bound x x' := by
          rcases hx with hb | ⟨rfl, hf⟩
          · exact hb
          · exact absurd hf (hnf x rfl)
        have hm := mem_of_bound hb
        by_cases hp : x ∈ pkg
        · rcases hs.binders _ hm with he | ⟨_, _, _, _, h1, _⟩
          · simp only at he; subst he
            have h1 := hs.bound _ hm hp
            have h2 := (hs.same _ hp).1 h1
            refine MenR.self_nil ?_ ?_ <;> simp [h1, h2]
          · exact absurd hp h1
        · have hp' : x' ∉ pkg := by
            rcases hs.binders _ hm with he | ⟨_, _, _, _, _, h2⟩
            · simp only at he; subst he; exact hp
            · exact h2
          have o1 := (omitted_nil hD hp as as).1
          have o2 := (omitted_nil hD hp' as' as').2
          have t1 := (omittedTypeDefaults_nil hD hp as as).1
          have t2 := (omittedTypeDefaults_nil hD hp' as' as').2
          refine MenR.self_nil ?_ ?_ <;> simp [o1, o2, t1, t2]
      | _ => exact MenR.nil

/-- `mentionsUpTo` commutes with the renaming: on related expressions in related scopes it
    lists the same package-level names, at every budget and chain. -/
theorem mentionsUpTo_alpha (hD : DeclsR pkg pm ds ds') : ∀ k, MenStmt pkg pm ds ds' k
  | 0 => fun he hs => mentions_lt hD (fun _ h => absurd h (by omega)) _ _ (Nat.lt_succ_self _) he hs
  | k + 1 => fun he hs => mentions_lt hD (fun j h => by
      cases h; exact mentionsUpTo_alpha hD k) _ _ (Nat.lt_succ_self _) he hs

theorem OptR.isSome_eq {Γ : Ren} {o o' : Option Expr} (h : OptR pkg pm Γ o o') :
    o.isSome = o'.isSome := by cases h <;> rfl

theorem count_isSome :∀ {l l' : List (Option Expr)}, l.length = l'.length →
    (∀ j, (l.getD j none).isSome = (l'.getD j none).isSome) →
    (l.filter (·.isSome)).length = (l'.filter (·.isSome)).length
  | [], [], _, _ => rfl
  | a :: l, b :: l', hl, h => by
    have h0 := h 0
    simp only [List.getD_cons_zero] at h0
    have ih := count_isSome (l := l) (l' := l') (by simpa using hl) (fun j => by simpa using h (j + 1))
    simp only [List.filter_cons, h0]
    split <;> simp [ih]
  | [], _ :: _, hl, _ | _ :: _, [], hl, _ => by simp at hl

/-- Related programs have the same number of defaults. -/
theorem defaultCount_eq (hD : DeclsR pkg pm ds ds') : defaultCount ds = defaultCount ds' := by
  unfold defaultCount
  rw [← hD.structs_eq, ← hD.sealeds_eq]
  congr 2
  have := hD.funcs
  generalize ds.funcs = l at this
  generalize ds'.funcs = l' at this
  induction this with
  | nil => rfl
  | @cons a b _ _ hab _ ih =>
    simp only [List.map_cons, List.sum_cons, ih]
    congr 1
    obtain ⟨_, _, hf⟩ := hab
    exact count_isSome hf.dlen fun j => OptR.isSome_eq (hf.defaults j)

/-- **`mentions` commutes with the renaming**: on related expressions in related scopes it
    lists the same package-level names. -/
theorem mentions_alpha (hD : DeclsR pkg pm ds ds') {Γ : Ren} {e e' : Expr} {b b' : List String}
    (he : AlphaE pkg pm Γ e e') (hs : MenScope pkg Γ b b') :
    MenR pkg (mentions ds b e) (mentions ds' b' e') := by
  unfold mentions
  rw [← defaultCount_eq hD]
  exact mentionsUpTo_alpha hD _ he hs

/-! ### `reach` -/

/-- A name outside `pkg` is no function on either side. -/
theorem funcs_none (hD : DeclsR pkg pm ds ds') {x : String} (hx : x ∉ pkg) :
    ds.funcs.lookup x = none ∧ ds'.funcs.lookup x = none := by
  have hf : ds.funcs.lookup x = none := by
    cases h : ds.funcs.lookup x with
    | none => rfl
    | some _ => exact absurd (hD.inPkg x (Or.inl (by simp [h]))) hx
  refine ⟨hf, ?_⟩
  rcases funcs_rel hD x with
    ⟨_, h2⟩ | ⟨_, _, h1, _, _⟩
  · exact h2
  · rw [hf] at h1; cases h1

/-- What `reach` expands a name to, related: nothing on either side outside `pkg`; in `pkg`,
    a function's body on both sides or neither, mentioning the same package-level names. -/
theorem expand_rel (hD : DeclsR pkg pm ds ds') (x : String) :
    (x ∉ pkg → reach.expand ds x = none ∧ reach.expand ds' x = none) ∧
    ((reach.expand ds x).map (inPkg pkg) = (reach.expand ds' x).map (inPkg pkg)) := by
  unfold reach.expand
  rcases funcs_rel hD x with
    ⟨h1, h2⟩ | ⟨f, f', h1, h2, hf⟩
  · simp [h1, h2]
  · refine ⟨fun hx => ?_, ?_⟩
    · rw [(funcs_none hD hx).1] at h1; cases h1
    · simp only [h1, h2, Option.map_some]
      have hs := NamesR.push (menScope_nil pkg) hf.names
      simp only [List.append_nil] at hs
      exact congrArg some (mentions_alpha hD hf.body hs)

theorem mem_inPkg {pkg l : List String} {x : String} (hx : x ∈ pkg) : x ∈ inPkg pkg l ↔ x ∈ l := by
  simp [inPkg, hx]

theorem contains_of_inPkg {pkg l l' : List String} {x : String} (hx : x ∈ pkg)
    (h : inPkg pkg l = inPkg pkg l') : l.contains x = l'.contains x := by
  apply Bool.eq_iff_iff.mpr
  simp only [contains_iff]
  rw [← mem_inPkg hx, h, mem_inPkg hx]

/-- `reach` follows the same package-level names on both sides: names outside `pkg` are no
    functions, so they cost no fuel and expand to nothing. By induction on the fuel, then on
    a bound `m` on the names left to visit. -/
theorem reach_go_alpha (hD : DeclsR pkg pm ds ds') :
    ∀ (n m : Nat) (todo todo' seen seen' : List String), todo.length + todo'.length ≤ m →
      inPkg pkg todo = inPkg pkg todo' → inPkg pkg seen = inPkg pkg seen' →
      inPkg pkg (reach.go ds n todo seen) = inPkg pkg (reach.go ds' n todo' seen') := by
  have hcons : ∀ x s, x ∉ pkg → inPkg pkg (x :: s) = inPkg pkg s := by
    intro x s hx; simp [inPkg, hx]
  intro n
  induction n with
  | zero => intro _ _ _ _ _ _ _ hseen; rw [reach.go.eq_1, reach.go.eq_1]; exact hseen
  | succ n ihn =>
    -- A head outside `pkg` on either side is skipped: a step of that side alone.
    have skip : ∀ {d : Decls} (x : String) (t s : List String), x ∉ pkg → reach.expand d x = none →
        inPkg pkg (reach.go d (n + 1) (x :: t) s) = inPkg pkg (reach.go d (n + 1) t s) ∨
        inPkg pkg (reach.go d (n + 1) (x :: t) s) = inPkg pkg (reach.go d (n + 1) t (x :: s)) := by
      intro d x t s _ he
      rw [reach.go.eq_3]
      split
      · exact Or.inl rfl
      · rw [he]; exact Or.inr rfl
    intro m
    induction m with
    | zero =>
      intro todo todo' seen seen' hm _ hseen
      have h1 : todo = [] := List.eq_nil_of_length_eq_zero (by omega)
      have h2 : todo' = [] := List.eq_nil_of_length_eq_zero (by omega)
      subst h1 h2
      rw [reach.go.eq_2 _ _ _ (by omega), reach.go.eq_2 _ _ _ (by omega)]; exact hseen
    | succ m ihm =>
      intro todo todo' seen seen' hm htodo hseen
      -- Skipping a head outside `pkg` on the right.
      have right : ∀ (x' : String) (t' : List String), x' ∉ pkg →
          todo.length + t'.length ≤ m → inPkg pkg todo = inPkg pkg t' →
          inPkg pkg (reach.go ds (n + 1) todo seen) =
            inPkg pkg (reach.go ds' (n + 1) (x' :: t') seen') := by
        intro x' t' hx' hm' ht'
        rcases skip (d := ds') x' t' seen' hx' ((expand_rel hD x').1 hx').2 with h | h <;> rw [h]
        · exact ihm todo t' seen seen' hm' ht' hseen
        · exact ihm todo t' seen (x' :: seen') hm' ht' (by rw [hseen, hcons _ _ hx'])
      match todo, todo', htodo, hm, right with
      | [], [], _, _, _ =>
        rw [reach.go.eq_2 _ _ _ (by omega), reach.go.eq_2 _ _ _ (by omega)]; exact hseen
      | [], x' :: t', htodo, hm, right =>
        by_cases hx' : x' ∈ pkg
        · simp [inPkg, hx'] at htodo
        · exact right x' t' hx' (by simp at hm ⊢; omega) (by rw [htodo, hcons _ _ hx'])
      | x :: t, todo', htodo, hm, rt =>
        by_cases hx : x ∈ pkg
        · match todo', htodo, hm, rt with
          | [], htodo, _, _ => simp [inPkg, hx] at htodo
          | x' :: t', htodo, hm, rt =>
            by_cases hx' : x' ∈ pkg
            · have hxx : x = x' ∧ inPkg pkg t = inPkg pkg t' := by
                simp only [inPkg, List.filter_cons, hx, hx', decide_true, ↓reduceIte,
                  List.cons.injEq] at htodo
                exact htodo
              obtain ⟨rfl, ht⟩ := hxx
              rw [reach.go.eq_3, reach.go.eq_3, contains_of_inPkg hx hseen]
              have hex := (expand_rel hD x).2
              split
              · exact ihm t t' seen seen' (by simp at hm; omega) ht hseen
              · have hseen' : inPkg pkg (x :: seen) = inPkg pkg (x :: seen') := by
                  simp only [inPkg, List.filter_cons, hx, decide_true, ↓reduceIte, List.cons.injEq,
                    true_and] at hseen ⊢
                  exact hseen
                cases h1 : reach.expand ds x <;> cases h2 : reach.expand ds' x <;>
                  rw [h1, h2] at hex <;> simp only [Option.map_none, Option.map_some, reduceCtorEq,
                    Option.some.injEq] at hex
                · exact ihm t t' _ _ (by simp at hm; omega) ht hseen'
                · exact ihn _ _ _ _ _ (Nat.le_refl _) (by simp only [inPkg_append]; rw [hex, ht]) hseen'
            · exact rt x' t' hx' (by simp at hm ⊢; omega) (by rw [htodo, hcons _ _ hx'])
        · rcases skip (d := ds) x t seen hx ((expand_rel hD x).1 hx).1 with h | h <;> rw [h]
          · exact ihm t todo' seen seen' (by simp at hm ⊢; omega) (by rw [← htodo, hcons _ _ hx]) hseen
          · exact ihm t todo' (x :: seen) seen' (by simp at hm ⊢; omega)
              (by rw [← htodo, hcons _ _ hx]) (by rw [← hseen, hcons _ _ hx])

/-- **`reach` commutes with the renaming**, on the package-level names. -/
theorem reach_alpha (hD : DeclsR pkg pm ds ds') {start start' : List String}
    (h : inPkg pkg start = inPkg pkg start') :
    inPkg pkg (reach ds start) = inPkg pkg (reach ds' start') := by
  unfold reach
  rw [fall2_length hD.funcs]
  exact reach_go_alpha hD _ _ _ _ _ _ (Nat.le_refl _) h rfl

end

/-! ## Whole programs -/

/-- A package-level declaration and its renaming: the same names (package-level names are
    not renamed), the same kind and type, and initializers related with no local in scope. -/
structure GlobalR (pkg : List String) (pm : String → Ren) (g g' : GlobalDecl) : Prop where
  mutable : g.mutable = g'.mutable
  names : g.names = g'.names
  destructure : g.destructure = g'.destructure
  type : g.type = g'.type
  values : AlphaL pkg pm [] g.values g'.values

/-- **A program and its renaming** (T2): the declarations `run` sees (`collect`) related by
    `DeclsR`, and the package-level variables, in the same order, by `GlobalR`, with every
    package-level variable's name in `pkg`. Files and declarations correspond one to one,
    in the same order: only binders inside them are renamed. -/
structure AlphaProgram (pkg : List String) (pm : String → Ren) (p p' : Program) : Prop where
  decls : DeclsR pkg pm (collect p).1 (collect p').1
  globals : Fall2 (GlobalR pkg pm) (collect p).2 (collect p').2
  globalsInPkg : ∀ g ∈ (collect p).2, ∀ x ∈ g.names, x ∈ pkg

/-- How two runs end, up to the text of `unsupported` and `stuck`, which may name a binder. -/
inductive OutcomeR : Outcome → Outcome → Prop
  | ok : OutcomeR .ok .ok
  | panic (c : PanicClass) (m : String) : OutcomeR (.panic c m) (.panic c m)
  | fuel : OutcomeR .fuel .fuel
  | unsupported (k : String) (m m' : String) : OutcomeR (.unsupported k m) (.unsupported k m')
  | stuck (m m' : String) : OutcomeR (.stuck m) (.stuck m')

section
variable {pkg : List String} {pm : String → Ren} {ds ds' : Decls}

/-- Changing what a program sees, related to related. -/
theorem relM_modify {Γ Γ' : Ren} {f f' : Core → Core}
    (h : ∀ c c', CoreR pkg pm Γ c c' → CoreR pkg pm Γ' (f c) (f' c')) :
    RelM pkg pm Γ (· = Γ') Eq (liftM (modifyCore f)) (liftM (modifyCore f')) := by
  intro s s' ⟨hc, hn⟩
  rw [run_liftM, run_liftM]
  exact ⟨rfl, Γ', ⟨h _ _ hc, hn⟩, fun _ => rfl⟩

theorem mem_zip_self : ∀ {l : List String} {p : String × String}, p ∈ l.zip l → p.1 = p.2
  | [], _, h => by simp at h
  | a :: l, p, h => by
    simp only [List.zip_cons_cons, List.mem_cons] at h
    rcases h with rfl | h
    · rfl
    · exact mem_zip_self h

theorem namesR_self (ns : List String) : NamesR pkg ns ns :=
  ⟨rfl, fun _ hp => Or.inl (mem_zip_self hp)⟩

/-- Binding a declaration's values as package-level variables keeps them related. -/
theorem globR_fold (m : Bool) :
    ∀ {bs bs' : List (String × Val)} {g g' : Env},
      Fall2 (fun (a b : String × Val) => a.1 = b.1 ∧ ValR pkg pm a.2 b.2) bs bs' →
      GlobR pkg pm g g' →
      GlobR pkg pm (bs.foldl (fun ρ (x, v) => bind ρ x ⟨v, m⟩) g)
        (bs'.foldl (fun ρ (x, v) => bind ρ x ⟨v, m⟩) g')
  | _, _, _, _, .nil, h => h
  | (x, v) :: _, (x', v') :: _, g, g', .cons ⟨hx, hv⟩ t, h => by
    simp only at hx
    subst hx
    simp only [List.foldl_cons]
    refine globR_fold m t ?_
    unfold bind
    split
    · exact h
    · exact .cons rfl hv h

/-- The bindings of a declaration whose names are not renamed. -/
theorem bindsR_same {ns : List String} {bs bs' : List (String × Val)} (h : BindsR pkg pm ns ns bs bs') :
    Fall2 (fun (a b : String × Val) => a.1 = b.1 ∧ ValR pkg pm a.2 b.2) bs bs' := by
  obtain ⟨hf, h1, h2⟩ := h
  rw [← h2] at h1
  clear h2
  induction hf with
  | nil => exact .nil
  | cons hab _ ih =>
    simp only [List.map_cons, List.cons.injEq] at h1
    exact .cons ⟨h1.1, hab.2⟩ (ih h1.2)

/-- The package-level variables a declaration depends on are the same on both sides. -/
theorem deps_eq (hD : DeclsR pkg pm ds ds') {all : List String} (hall : ∀ x ∈ all, x ∈ pkg)
    {g g' : GlobalDecl} (hg : GlobalR pkg pm g g') :
    (reach ds (mentions ds [] (.decl .val g.names g.destructure g.type g.values))).filter
        (all.contains ·) =
      (reach ds' (mentions ds' [] (.decl .val g'.names g'.destructure g'.type g'.values))).filter
        (all.contains ·) := by
  have hm : MenR pkg (mentions ds [] (.decl .val g.names g.destructure g.type g.values))
      (mentions ds' [] (.decl .val g'.names g'.destructure g'.type g'.values)) := by
    unfold mentions
    rw [← defaultCount_eq hD]
    unfold mentionsUpTo
    refine (MenR.all (fall2_map (fun _ _ he => mentionsUpTo_alpha hD _ he (menScope_nil pkg))
      (alphaL_fall2 hg.values))).cat ?_
    rw [alphaL_isEmpty hg.values, hg.type]
    split
    · exact MenR.nil
    · exact menR_keyed (fun j _ => mentionsUpTo_alpha hD j) [] (declDefaults_rel hD _)
  have hr := reach_alpha hD hm
  have key : ∀ l : List String, l.filter (all.contains ·) = (inPkg pkg l).filter (all.contains ·) := by
    intro l
    unfold inPkg
    rw [List.filter_filter]
    congr 1
    funext x
    by_cases hx : x ∈ all
    · simp [hx, hall x hx]
    · simp [hx]
  rw [key, hr, ← key]

/-- A package-level declaration waiting to be initialized, on both sides. -/
def PendR (pkg : List String) (pm : String → Ren) (a b : Nat × GlobalDecl × List String) : Prop :=
  a.1 = b.1 ∧ GlobalR pkg pm a.2.1 b.2.1 ∧ a.2.2 = b.2.2

theorem find_pendR {P : Nat × GlobalDecl × List String → Bool}
    (hP : ∀ a b, PendR pkg pm a b → P a = P b) :
    ∀ {l l' : List (Nat × GlobalDecl × List String)}, Fall2 (PendR pkg pm) l l' →
      (l.find? P = none ∧ l'.find? P = none) ∨
        ∃ a b, l.find? P = some a ∧ l'.find? P = some b ∧ PendR pkg pm a b
  | _, _, .nil => Or.inl ⟨rfl, rfl⟩
  | a :: _, b :: _, .cons hab t => by
    simp only [List.find?_cons, hP a b hab]
    split
    · exact Or.inr ⟨a, b, rfl, rfl, hab⟩
    · exact find_pendR hP t

theorem filter_pendR (i : Nat) :
    ∀ {l l' : List (Nat × GlobalDecl × List String)}, Fall2 (PendR pkg pm) l l' →
      Fall2 (PendR pkg pm) (l.filter (·.1 != i)) (l'.filter (·.1 != i))
  | _, _, .nil => .nil
  | a :: _, b :: _, .cons hab t => by
    simp only [List.filter_cons, hab.1]
    split
    · exact .cons hab (filter_pendR i t)
    · exact filter_pendR i t

theorem zip_map_self (l : List α) (f : α → β) : l.zip (l.map f) = l.map fun a => (a, f a) := by
  induction l with
  | nil => rfl
  | cons a l ih => simp [ih]

theorem range_zip_pendR (s : Nat) :
    ∀ {l l' : List (GlobalDecl × List String)},
      Fall2 (fun a b => GlobalR pkg pm a.1 b.1 ∧ a.2 = b.2) l l' →
      Fall2 (PendR pkg pm) ((List.range' s l.length).zip l) ((List.range' s l'.length).zip l')
  | _, _, .nil => by simp; exact .nil
  | _, _, .cons ⟨h1, h2⟩ t => by
    simp only [List.length_cons, List.range'_succ, List.zip_cons_cons]
    exact .cons ⟨rfl, h1, h2⟩ (range_zip_pendR (s + 1) t)

/-- **Initializing the package-level variables commutes with the renaming**: in the same
    order, with related values. -/
theorem initGlobals_alpha (hD : DeclsR pkg pm ds ds') (d : Nat) {gs gs' : List GlobalDecl}
    (hg : Fall2 (GlobalR pkg pm) gs gs') (hin : ∀ g ∈ gs, ∀ x ∈ g.names, x ∈ pkg) :
    RelM pkg pm [] (· = []) Eq (initGlobals ds d gs) (initGlobals ds' d gs') := by
  have hall : ∀ x ∈ gs.flatMap (·.names), x ∈ pkg := by
    intro x hx
    obtain ⟨g, hg, hx⟩ := List.mem_flatMap.mp hx
    exact hin g hg x hx
  have hnames : gs.flatMap (·.names) = gs'.flatMap (·.names) := by
    clear hin hall
    induction hg with
    | nil => rfl
    | cons h _ ih => simp only [List.flatMap_cons, h.names, ih]
  unfold initGlobals
  dsimp only
  rw [← hnames]
  -- The loop's state: the declarations still pending, and the names initialized.
  refine relM_bind (relM_forIn_st (R := fun _ _ => True)
    (S := fun (x y : List (Nat × GlobalDecl × List String) × List String) =>
      Fall2 (PendR pkg pm) x.1 y.1 ∧ x.2 = y.2) ?_ (fall2_mono (fun _ _ _ => trivial) hg) ?_)
    fun _ _ _ => relM_pure rfl
  · rintro _ _ ⟨p, dn⟩ ⟨p', dn'⟩ _ ⟨hp, hdn⟩
    simp only at hp hdn
    subst hdn
    dsimp only
    have hP : ∀ a b, PendR pkg pm a b →
        (fun x => match x with | (_, _, deps) => deps.all dn.contains : Nat × GlobalDecl × List String → Bool) a =
        (fun x => match x with | (_, _, deps) => deps.all dn.contains : Nat × GlobalDecl × List String → Bool) b := by
      rintro ⟨_, _, _⟩ ⟨_, _, _⟩ ⟨_, _, h3⟩
      simp only at h3 ⊢
      rw [h3]
    rcases find_pendR hP hp with
      ⟨h1, h2⟩ | ⟨⟨i, g, dep⟩, ⟨i', g', dep'⟩, h1, h2, hpr⟩
    · rw [h1, h2]
      exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
    · rw [h1, h2]
      obtain ⟨hi, hgr, _⟩ := hpr
      simp only at hi hgr
      subst hi
      obtain ⟨m1, n1, d1, t1, v1⟩ := g
      obtain ⟨m2, n2, d2, t2, v2⟩ := g'
      obtain ⟨hm, hn, hd, ht, hv⟩ := hgr
      simp only at hm hn hd ht hv
      subst hm hn hd ht
      dsimp only
      refine relM_bind (relM_modify (Γ' := []) fun c c' hc =>
        ⟨.nil, hc.2.1, hc.2.2.1, hc.2.2.2⟩) fun _ _ _ => ?_
      refine relM_bind (declare_alpha hD d d1 t1 (namesR_self n1) hv) fun bs bs' hbs => ?_
      refine relM_bind (relM_modify (Γ' := []) fun c c' hc =>
        ⟨hc.1, globR_fold m1 (bindsR_same hbs) hc.2.1, hc.2.2.1, hc.2.2.2⟩) fun _ _ _ => ?_
      exact relM_pure ⟨filter_pendR i hp, rfl⟩
  · refine ⟨?_, rfl⟩
    dsimp only
    rw [zip_map_self, zip_map_self]
    have hl : ∀ (l : List GlobalDecl) (F : GlobalDecl → List String),
        l.length = (l.map fun g => (g, F g)).length := by simp
    rw [hl gs, hl gs', List.range_eq_range', List.range_eq_range']
    exact range_zip_pendR 0 (fall2_map
      (f := fun g => (g, (reach ds (mentions ds [] (.decl .val g.names g.destructure g.type g.values))).filter
        ((gs.flatMap (·.names)).contains ·)))
      (g := fun g => (g, (reach ds' (mentions ds' [] (.decl .val g.names g.destructure g.type g.values))).filter
        ((gs.flatMap (·.names)).contains ·)))
      (fun a b h => ⟨h, deps_eq hD hall h⟩) hg)

/-- A program and its renaming stay related once their constant expressions are folded
    (`Gala/Const.lean`), which `run` does first. -/
theorem alphaProgram_fold {p p' : Program} (h : AlphaProgram pkg pm p p') :
    AlphaProgram pkg pm (foldConstants p) (foldConstants p') where
  decls := by rw [collect_fold, collect_fold]; exact declsR_fold h.decls
  globals := by
    rw [collect_fold, collect_fold]
    exact fall2_map (fun g g' hg => ⟨hg.mutable, hg.names, hg.destructure, hg.type,
      foldL_alpha hg.values⟩) h.globals
  globalsInPkg := by
    rw [collect_fold]
    intro g hg x hx
    obtain ⟨g₀, hg₀, rfl⟩ := List.mem_map.mp hg
    exact h.globalsInPkg g₀ hg₀ x hx

/-- Running a program commutes with the renaming. -/
theorem runM_alpha {p p' : Program} (h : AlphaProgram pkg pm p p') (n : Nat) :
    RelM pkg pm [] (fun _ => True) Eq (runM p n) (runM p' n) := by
  have hD := h.decls
  unfold runM
  rcases hc : collect p with ⟨ds₁, gs₁⟩
  rcases hc' : collect p' with ⟨ds₂, gs₂⟩
  rw [hc] at hD
  rw [hc'] at hD
  have hg := h.globals
  rw [hc, hc'] at hg
  have hin := h.globalsInPkg
  rw [hc] at hin
  dsimp only at hD hg hin ⊢
  rw [← hD.unsupported]
  rcases hk : List.find? (fun k => k == "import" || k == "blank-import") ds₁.unsupported with _ | k
  · dsimp only
    refine relM_bind_gen (initGlobals_alpha hD n hg hin) fun Γ _ _ hΓ _ => ?_
    subst hΓ
    rcases funcs_rel hD "main" with
      ⟨h1, h2⟩ | ⟨f, f', h1, h2, hf⟩
    · rw [h1, h2]
      exact relM_stop fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩
    · rw [h1, h2]
      exact (relM_discard (callFunc_alpha hD n hf .nil)).mono fun _ _ => trivial
  · dsimp only
    exact relM_stop_bind fun _ _ => ⟨_, _, rfl, rfl, .unsupported _ _ _⟩

end

/-- **T2, alpha-invariance.** A program and its renaming (`AlphaProgram`: binders renamed
    apart, capture-avoiding, builtins such as `Println` included; never to `_`; never to a
    package-level name; lowercase; named arguments following their parameters) print exactly
    the same output and end the same way, for every fuel, up to the text of `unsupported`
    and `stuck` (`OutcomeR`). -/
theorem run_alpha {pkg : List String} {pm : String → Ren} {p p' : Program}
    (h : AlphaProgram pkg pm p p') (n : Nat) :
    OutcomeR (run p n).1 (run p' n).1 ∧ (run p n).2 = (run p' n).2 := by
  let s₀ : St := { locals := [], globals := [], out := "", steps := n }
  have hs : StR pkg pm [] s₀ s₀ := ⟨⟨.nil, .nil, ⟨rfl, fun _ h _ => absurd h (by simp [s₀])⟩, rfl⟩, rfl⟩
  obtain ⟨hr, _, ⟨⟨_, _, _, hout⟩, _⟩, _⟩ := runM_alpha (alphaProgram_fold h) n s₀ s₀ hs
  unfold run
  refine ⟨?_, hout⟩
  revert hr
  rcases (runM (foldConstants p) n).run.run s₀ with ⟨r, _⟩
  rcases (runM (foldConstants p') n).run.run s₀ with ⟨r', _⟩
  intro hr
  cases r <;> cases r' <;> simp only [ResR] at hr
  · cases hr <;> first | exact .panic .. | exact .fuel | exact .unsupported .. | exact .stuck ..
  · exact .ok

/-! ## The checker is sound -/

namespace Check

mutual
theorem tyEq_eq : ∀ {a b : Ty}, tyEq a b = true → a = b
  | .name _, .name _, h => by simp [tyEq] at h; rw [h]
  | .func _ _, .func _ _, h => by
    simp only [tyEq, Bool.and_eq_true] at h; rw [tysEq_eq h.1, optTyEq_eq h.2]
  | .unsupported _, .unsupported _, h => by simp [tyEq] at h; rw [h]
  | .name _, .func _ _, h | .name _, .unsupported _, h | .func _ _, .name _, h
  | .func _ _, .unsupported _, h | .unsupported _, .name _, h | .unsupported _, .func _ _, h => by
    simp [tyEq] at h
theorem tysEq_eq : ∀ {a b : List Ty}, tysEq a b = true → a = b
  | [], [], _ => rfl
  | _ :: _, _ :: _, h => by
    simp only [tysEq, Bool.and_eq_true] at h; rw [tyEq_eq h.1, tysEq_eq h.2]
  | [], _ :: _, h | _ :: _, [], h => by simp [tysEq] at h
theorem optTyEq_eq : ∀ {a b : Option Ty}, optTyEq a b = true → a = b
  | none, none, _ => rfl
  | some _, some _, h => by simp only [optTyEq] at h; rw [tyEq_eq h]
  | none, some _, h | some _, none, h => by simp [optTyEq] at h
end

theorem optTysEq_eq : ∀ {a b : List (Option Ty)}, optTysEq a b = true → a = b
  | [], [], _ => rfl
  | _ :: _, _ :: _, h => by
    simp only [optTysEq, Bool.and_eq_true] at h; rw [optTyEq_eq h.1, optTysEq_eq h.2]
  | [], _ :: _, h | _ :: _, [], h => by simp [optTysEq] at h

theorem litEq_eq : ∀ {a b : Lit}, litEq a b = true → a = b := by
  intro a b h; cases a <;> cases b <;> simp_all [litEq]

mutual
theorem patEq_eq : ∀ {a b : Pat}, patEq a b = true → a = b
  | .tuple _, .tuple _, h => by simp only [patEq] at h; rw [patsEq_eq h]
  | .extract _ _, .extract _ _, h => by
    simp only [patEq, Bool.and_eq_true, beq_iff_eq] at h; rw [h.1, patsEq_eq h.2]
  | .lit _, .lit _, h => by simp only [patEq] at h; rw [litEq_eq h]
  | .wild, b, h | .name _, b, h | .unsupported _, b, h | .tuple _, b, h | .extract _ _, b, h
  | .lit _, b, h => by
    cases b <;> simp_all [patEq] <;> first | exact patsEq_eq h | exact litEq_eq h | exact patsEq_eq h.2
theorem patsEq_eq : ∀ {a b : List Pat}, patsEq a b = true → a = b
  | [], [], _ => rfl
  | _ :: _, _ :: _, h => by
    simp only [patsEq, Bool.and_eq_true] at h; rw [patEq_eq h.1, patsEq_eq h.2]
  | [], _ :: _, h | _ :: _, [], h => by simp [patsEq] at h
end

theorem paramsEq_eq : ∀ {a b : List Param}, paramsEq a b = true → a = b
  | [], [], _ => rfl
  | ⟨_, _⟩ :: _, ⟨_, _⟩ :: _, h => by
    simp only [paramsEq, Bool.and_eq_true, beq_iff_eq] at h
    rw [h.1.1, optTyEq_eq h.1.2, paramsEq_eq h.2]
  | [], _ :: _, h | _ :: _, [], h => by simp [paramsEq] at h

mutual
theorem exprEq_eq : ∀ {a b : Expr}, exprEq a b = true → a = b
  | a, b, h => by
    cases a <;> cases b <;>
      simp only [exprEq, Bool.and_eq_true, beq_iff_eq, decide_eq_true_eq, Bool.false_eq_true] at h
    · rw [litEq_eq h]
    · rw [h]
    · rw [h.1, exprEq_eq h.2]
    · rw [h.1.1, exprEq_eq h.1.2, exprEq_eq h.2]
    · rw [exprEq_eq h.1, exprsEq_eq h.2]
    · rw [h.1, exprEq_eq h.2]
    · rw [exprEq_eq h]
    · rw [exprsEq_eq h]
    · rw [exprEq_eq h.1.1, exprEq_eq h.1.2, exprEq_eq h.2]
    · rw [exprEq_eq h.1, casesEq_eq h.2]
    · rw [exprsEq_eq h]
    · rw [exprEq_eq h.1.1, exprEq_eq h.1.2, optExprEq_eq h.2]
    · rw [optExprEq_eq h]
    · rw [h.1.1, exprsEq_eq h.1.2, exprsEq_eq h.2]
    · rw [h.1, exprEq_eq h.2]
    · rw [h.1.1.1.1, h.1.1.1.2, h.1.1.2, optTyEq_eq h.1.2, exprsEq_eq h.2]
    · rw [exprsEq_eq h]
    · rw [paramsEq_eq h.1.1, optTyEq_eq h.1.2, exprEq_eq h.2]
    · rw [h.1, exprEq_eq h.2]
    · rw [h]
theorem exprsEq_eq : ∀ {a b : List Expr}, exprsEq a b = true → a = b
  | [], [], _ => rfl
  | _ :: _, _ :: _, h => by
    simp only [exprsEq, Bool.and_eq_true] at h; rw [exprEq_eq h.1, exprsEq_eq h.2]
  | [], _ :: _, h | _ :: _, [], h => by simp [exprsEq] at h
theorem optExprEq_eq : ∀ {a b : Option Expr}, optExprEq a b = true → a = b
  | none, none, _ => rfl
  | some _, some _, h => by simp only [optExprEq] at h; rw [exprEq_eq h]
  | none, some _, h | some _, none, h => by simp [optExprEq] at h
theorem casesEq_eq : ∀ {a b : List (Pat × Option Expr × Expr)}, casesEq a b = true → a = b
  | [], [], _ => rfl
  | (_, _, _) :: _, (_, _, _) :: _, h => by
    simp only [casesEq, Bool.and_eq_true] at h
    rw [patEq_eq h.1.1.1, optExprEq_eq h.1.1.2, exprEq_eq h.1.2, casesEq_eq h.2]
  | [], _ :: _, h | _ :: _, [], h => by simp [casesEq] at h
end

theorem fieldEq_eq {a b : Field} (h : fieldEq a b = true) : a = b := by
  obtain ⟨_, _, _, _⟩ := a
  obtain ⟨_, _, _, _⟩ := b
  simp only [fieldEq, Bool.and_eq_true, beq_iff_eq] at h
  rw [h.1.1.1, optTyEq_eq h.1.1.2, h.1.2, optExprEq_eq h.2]

/-! ### Names and binders -/

theorem boundB_sound : ∀ {Γ : Ren} {x x' : String}, boundB Γ x x' = true → Γ.bound x x'
  | [], _, _, h => by simp [boundB] at h
  | (a, a') :: Γ, x, x', h => by
    simp only [boundB, Bool.or_eq_true, Bool.and_eq_true, beq_iff_eq, bne_iff_ne, ne_eq] at h
    simp only [Ren.bound]
    rcases h with h | ⟨⟨h1, h2⟩, h3⟩
    · exact Or.inl h
    · exact Or.inr ⟨h1, h2, boundB_sound h3⟩

theorem not_mem_of_contains {l : List String} {x : String} (h : l.contains x = false) : x ∉ l := by
  simpa using h

theorem freeB_sound {Γ : Ren} {x : String} (h : freeB Γ x = true) : Γ.free x := by
  simp only [freeB, Bool.and_eq_true, Bool.not_eq_true'] at h
  exact ⟨not_mem_of_contains h.1, not_mem_of_contains h.2⟩

theorem nameB_sound {Γ : Ren} {x x' : String} (h : nameB Γ x x' = true) : NameR Γ x x' := by
  simp only [nameB, Bool.or_eq_true, Bool.and_eq_true, beq_iff_eq] at h
  rcases h with h | ⟨rfl, h⟩
  · exact Or.inl (boundB_sound h)
  · exact Or.inr ⟨rfl, freeB_sound h⟩

theorem renamedB_sound {pkg : List String} {x x' : String} (h : renamedB pkg x x' = true) :
    x ≠ "_" ∧ x' ≠ "_" ∧ capitalized x = false ∧ capitalized x' = false ∧ x ∉ pkg ∧ x' ∉ pkg := by
  simp only [renamedB, Bool.and_eq_true, bne_iff_ne, ne_eq, Bool.not_eq_true'] at h
  exact ⟨h.1.1.1.1.1, h.1.1.1.1.2, h.1.1.1.2, h.1.1.2, not_mem_of_contains h.1.2,
    not_mem_of_contains h.2⟩

theorem binderB_sound {pkg : List String} {x x' : String} (h : binderB pkg x x' = true) :
    BinderR pkg x x' := by
  simp only [binderB, Bool.or_eq_true, beq_iff_eq] at h
  rcases h with h | h
  · exact Or.inl h
  · exact Or.inr (renamedB_sound h)

theorem namesB_sound {pkg : List String} {ns ns' : List String} (h : namesB pkg ns ns' = true) :
    NamesR pkg ns ns' := by
  simp only [namesB, Bool.and_eq_true, beq_iff_eq, List.all_eq_true] at h
  exact ⟨h.1, fun p hp => binderB_sound (h.2 p hp)⟩

theorem fall2B_sound {r : α → β → Bool} {R : α → β → Prop} (hr : ∀ a b, r a b = true → R a b) :
    ∀ {l : List α} {l' : List β}, fall2B r l l' = true → Fall2 R l l'
  | [], [], _ => .nil
  | _ :: _, _ :: _, h => by
    simp only [fall2B, Bool.and_eq_true] at h
    exact .cons (hr _ _ h.1) (fall2B_sound hr h.2)
  | [], _ :: _, h | _ :: _, [], h => by simp [fall2B] at h

/-! ### Patterns and expressions -/

mutual
theorem patB_sound {pkg : List String} {Γ : Ren} :
    ∀ {p p' : Pat} {ss : List Slot}, patB pkg Γ p p' = some ss → PatR pkg Γ p p' ss
  | .wild, .wild, _, h => by simp only [patB, Option.some.injEq] at h; subst h; exact .wild
  | .lit l, .lit l', _, h => by
    simp only [patB] at h
    split at h
    · rename_i hl; rw [litEq_eq hl]; simp only [Option.some.injEq] at h; subst h; exact .lit _
    · cases h
  | .unsupported k, .unsupported k', _, h => by
    simp only [patB, beq_iff_eq] at h
    split at h
    · rename_i hk; subst hk; simp only [Option.some.injEq] at h; subst h; exact .unsupported _
    · cases h
  | .name x, .name x', _, h => by
    simp only [patB] at h
    split at h
    · rename_i hc
      simp only [Bool.and_eq_true, beq_iff_eq] at hc
      obtain ⟨rfl, hn⟩ := hc
      simp only [Option.some.injEq] at h; subst h
      exact .same (nameB_sound hn)
    · split at h
      · rename_i hr
        obtain ⟨h1, h2, h3, h4, h5, h6⟩ := renamedB_sound hr
        simp only [Option.some.injEq] at h; subst h
        exact .bind h1 h2 h3 h4 h5 h6
      · cases h
  | .tuple ps, .tuple ps', _, h => by simp only [patB] at h; exact .tuple (patsB_sound h)
  | .extract c ps, .extract c' ps', _, h => by
    simp only [patB, beq_iff_eq] at h
    split at h
    · rename_i hc; subst hc; exact .extract _ (patsB_sound h)
    · cases h
  | .wild, .name _, _, h | .wild, .lit _, _, h | .wild, .tuple _, _, h | .wild, .extract _ _, _, h
  | .wild, .unsupported _, _, h | .name _, .wild, _, h | .name _, .lit _, _, h
  | .name _, .tuple _, _, h | .name _, .extract _ _, _, h | .name _, .unsupported _, _, h
  | .lit _, .wild, _, h | .lit _, .name _, _, h | .lit _, .tuple _, _, h | .lit _, .extract _ _, _, h
  | .lit _, .unsupported _, _, h | .tuple _, .wild, _, h | .tuple _, .name _, _, h
  | .tuple _, .lit _, _, h | .tuple _, .extract _ _, _, h | .tuple _, .unsupported _, _, h
  | .extract _ _, .wild, _, h | .extract _ _, .name _, _, h | .extract _ _, .lit _, _, h
  | .extract _ _, .tuple _, _, h | .extract _ _, .unsupported _, _, h
  | .unsupported _, .wild, _, h | .unsupported _, .name _, _, h | .unsupported _, .lit _, _, h
  | .unsupported _, .tuple _, _, h | .unsupported _, .extract _ _, _, h => by simp [patB] at h
theorem patsB_sound {pkg : List String} {Γ : Ren} :
    ∀ {ps ps' : List Pat} {ss : List Slot}, patsB pkg Γ ps ps' = some ss → PatsR pkg Γ ps ps' ss
  | [], [], _, h => by simp only [patsB, Option.some.injEq] at h; subst h; exact .nil
  | p :: ps, p' :: ps', _, h => by
    simp only [patsB] at h
    split at h
    · rename_i s ss h1 h2
      simp only [Option.some.injEq] at h; subst h
      exact .cons (patB_sound h1) (patsB_sound h2)
    · cases h
  | [], _ :: _, _, h | _ :: _, [], _, h => by simp [patsB] at h
end

theorem freeB_complete {Γ : Ren} {x : String} (h : Γ.free x) : freeB Γ x = true := by
  simp [freeB, h.1, h.2]

theorem namedB_sound {m : Ren} {n n' : String} (h : namedB m n n' = true) :
    m.lookup n = some n' ∨ (n = n' ∧ n ∉ m.map (·.1) ∧ n ∉ m.map (·.2)) := by
  simp only [namedB, Bool.or_eq_true, Bool.and_eq_true, beq_iff_eq, Bool.not_eq_true'] at h
  rcases h with h | ⟨⟨h1, h2⟩, h3⟩
  · exact Or.inl h
  · exact Or.inr ⟨h1, not_mem_of_contains h2, not_mem_of_contains h3⟩

mutual
theorem exprB_sound {pkg : List String} {pm : String → Ren} :
    ∀ {Γ : Ren} {e e' : Expr}, exprB pkg pm Γ e e' = true → AlphaE pkg pm Γ e e'
  | Γ, e, e', h => by
    cases e <;> cases e' <;> simp only [exprB, Bool.false_eq_true] at h
    · rw [litEq_eq h]; exact AlphaE.lit _
    · exact .name (nameB_sound h)
    · simp only [Bool.and_eq_true, beq_iff_eq] at h; rw [h.1]; exact .unary _ (exprB_sound h.2)
    · simp only [Bool.and_eq_true, beq_iff_eq] at h
      rw [h.1.1]; exact .binary _ (exprB_sound h.1.2) (exprB_sound h.2)
    · rename_i c as c' as'
      split at h
      · rename_i f f'
        split at h
        · rename_i hf
          simp only [Bool.and_eq_true, beq_iff_eq] at h
          obtain ⟨rfl, ha⟩ := h
          exact .callFree (freeB_sound hf) (argsB_sound ha)
        · rename_i hf
          simp only [Bool.and_eq_true] at h
          refine .call (fun g hg => ?_) (.name (nameB_sound h.1)) (argsB_sound h.2)
          cases hg
          exact fun hfr => hf (freeB_complete hfr)
      · cases h
      · rename_i h1 h2
        simp only [Bool.and_eq_true] at h
        exact .call (fun g hg => (h1 g hg).elim) (exprB_sound h.1) (argsB_sound h.2)
    · simp only [Bool.and_eq_true, beq_iff_eq] at h; rw [h.1]; exact .select _ (exprB_sound h.2)
    · exact .paren (exprB_sound h)
    · exact .tuple (listB_sound h)
    · simp only [Bool.and_eq_true] at h
      exact .ifExpr (exprB_sound h.1.1) (exprB_sound h.1.2) (exprB_sound h.2)
    · simp only [Bool.and_eq_true] at h
      exact .mtch (exprB_sound h.1) (casesB_sound h.2)
    · exact .block (blockB_sound h)
    · simp only [Bool.and_eq_true] at h
      exact .ifStmt (exprB_sound h.1.1) (exprB_sound h.1.2) (optB_sound h.2)
    · exact .ret (optB_sound h)
    · simp only [Bool.and_eq_true, beq_iff_eq] at h
      rw [h.1.1]; exact .assign _ (listB_sound h.1.2) (listB_sound h.2)
    · simp only [Bool.and_eq_true, beq_iff_eq] at h; rw [h.1]; exact .incdec _ (exprB_sound h.2)
    · exact .interp (listB_sound h)
    · simp only [Bool.and_eq_true] at h
      rw [optTyEq_eq h.1.1.1]
      exact .lambda _ (optTysEq_eq h.1.1.2) (namesB_sound h.1.2) (exprB_sound h.2)
    · simp only [beq_iff_eq] at h; subst h; exact .unsupported _
  termination_by structural _ e _ _ => e
theorem listB_sound {pkg : List String} {pm : String → Ren} :
    ∀ {Γ : Ren} {es es' : List Expr}, listB pkg pm Γ es es' = true → AlphaL pkg pm Γ es es'
  | _, [], [], _ => .nil
  | _, _ :: _, _ :: _, h => by
    simp only [listB, Bool.and_eq_true] at h; exact .cons (exprB_sound h.1) (listB_sound h.2)
  | _, [], _ :: _, h | _, _ :: _, [], h => by simp [listB] at h
  termination_by structural _ es _ _ => es
theorem optB_sound {pkg : List String} {pm : String → Ren} :
    ∀ {Γ : Ren} {o o' : Option Expr}, optB pkg pm Γ o o' = true → OptR pkg pm Γ o o'
  | _, none, none, _ => .none
  | _, some _, some _, h => by simp only [optB] at h; exact .some (exprB_sound h)
  | _, none, some _, h | _, some _, none, h => by simp [optB] at h
  termination_by structural _ o _ _ => o
theorem argsB_sound {pkg : List String} {pm : String → Ren} :
    ∀ {m Γ : Ren} {as as' : List Expr}, argsB pkg pm m Γ as as' = true → ArgsR pkg pm m Γ as as'
  | _, _, [], [], _ => .nil
  | _, _, e :: _, e' :: _, h => by
    cases e <;> cases e' <;> simp only [argsB, Bool.and_eq_true, Bool.not_eq_true', isNamed] at h <;>
    first
      | exact .named (namedB_sound h.1.1) (exprB_sound h.1.2) (argsB_sound h.2)
      | exact .pos rfl (exprB_sound h.1.2) (argsB_sound h.2)
      | (exfalso; simp at h)
  | _, _, [], _ :: _, h | _, _, _ :: _, [], h => by simp [argsB] at h
  termination_by structural _ _ xs _ _ => xs
theorem blockB_sound {pkg : List String} {pm : String → Ren} :
    ∀ {Γ : Ren} {ss ss' : List Expr}, blockB pkg pm Γ ss ss' = true → BlockR pkg pm Γ ss ss'
  | _, [], [], _ => .nil
  | _, e :: _, e' :: _, h => by
    cases e <;> cases e' <;> simp only [blockB, Bool.and_eq_true, decide_eq_true_eq, beq_iff_eq] at h <;>
    first
      | exact .stmt (exprB_sound h.1) (blockB_sound h.2)
      | (obtain ⟨⟨⟨⟨⟨hm, hd⟩, ht⟩, hn⟩, hv⟩, hr⟩ := h
         subst hm hd
         rw [optTyEq_eq ht]
         exact .decl _ _ _ (namesB_sound hn) (listB_sound hv) (blockB_sound hr))
  | _, [], _ :: _, h | _, _ :: _, [], h => by simp [blockB] at h
  termination_by structural _ ss _ _ => ss
theorem casesB_sound {pkg : List String} {pm : String → Ren} :
    ∀ {Γ : Ren} {cs cs' : List (Pat × Option Expr × Expr)}, casesB pkg pm Γ cs cs' = true →
      CasesR pkg pm Γ cs cs'
  | _, [], [], _ => .nil
  | Γ, (p, g, b) :: cs, (p', g', b') :: cs', h => by
    simp only [casesB] at h
    split at h
    · rename_i ss hp
      simp only [Bool.and_eq_true, List.all_eq_true] at h
      exact .cons (patB_sound hp) (fun Δ hΔ => optB_sound (h.1 Δ hΔ).1)
        (fun Δ hΔ => exprB_sound (h.1 Δ hΔ).2) (casesB_sound h.2)
    · cases h
  | _, [], _ :: _, h | _, _ :: _, [], h => by simp [casesB] at h
  termination_by structural _ cs _ _ => cs
end

/-! ### Declarations and programs -/

theorem funcB_sound {pkg : List String} {pm : String → Ren} {f f' : Func} (h : funcB pkg pm f f' = true) :
    FuncR pkg pm f f' := by
  simp only [funcB, Bool.and_eq_true, beq_iff_eq, decide_eq_true_eq, List.all_eq_true,
    List.mem_range] at h
  obtain ⟨⟨⟨⟨⟨⟨⟨⟨⟨hn, ht⟩, hns⟩, hnd⟩, hnd'⟩, hm⟩, hl⟩, hd⟩, hr⟩, hb⟩ := h
  refine ⟨hn, optTysEq_eq ht, namesB_sound hns, ⟨hnd, hnd'⟩, hm, hl, fun j => ?_, optTyEq_eq hr,
    exprB_sound hb⟩
  by_cases hj : j < f.defaults.length
  · exact optB_sound (hd j hj)
  · have h1 : f.defaults.getD j none = none := by
      rw [List.getD_eq_getElem?_getD, List.getElem?_eq_none (by omega)]; rfl
    have h2 : f'.defaults.getD j none = none := by
      rw [List.getD_eq_getElem?_getD, List.getElem?_eq_none (by omega)]; rfl
    rw [h1, h2]; exact .none

theorem fieldB_sound {pkg : List String} {pm : String → Ren} {a b : Field} (h : fieldB pkg pm a b = true) :
    FieldR pkg pm a b := by
  simp only [fieldB, Bool.and_eq_true] at h
  exact ⟨fieldEq_eq h.1, optB_sound h.2⟩

theorem varB_sound {pkg : List String} {pm : String → Ren} {v w : Variant} (h : varB pkg pm v w = true) :
    VarR pkg pm v w := by
  simp only [varB, Bool.and_eq_true, beq_iff_eq] at h
  exact ⟨h.1.1, h.1.2, fall2B_sound (fun _ _ => fieldB_sound) h.2⟩

theorem globalB_sound {pkg : List String} {pm : String → Ren} {g g' : GlobalDecl}
    (h : globalB pkg pm g g' = true) : GlobalR pkg pm g g' := by
  simp only [globalB, Bool.and_eq_true, beq_iff_eq] at h
  exact ⟨h.1.1.1.1, h.1.1.1.2, h.1.1.2, optTyEq_eq h.1.2, listB_sound h.2⟩

end Check

open Check in
/-- `alphaPkg` holds every function, struct and case name, and every package-level
    variable's name. -/
theorem alphaPkg_decls (ds : Decls) (gs : List GlobalDecl) (x : String)
    (h : (ds.funcs.lookup x).isSome ∨ (ds.structs.lookup x).isSome ∨ (ds.variant? x).isSome) :
    x ∈ alphaPkg ds gs := by
  unfold alphaPkg
  simp only [List.mem_append]
  rcases h with h | h | h
  · obtain ⟨f, hf⟩ := Option.isSome_iff_exists.mp h
    exact Or.inl (Or.inl (Or.inl (List.mem_map.mpr ⟨_, mem_of_lookup hf, rfl⟩)))
  · obtain ⟨f, hf⟩ := Option.isSome_iff_exists.mp h
    exact Or.inl (Or.inl (Or.inr (List.mem_map.mpr ⟨_, mem_of_lookup hf, rfl⟩)))
  · obtain ⟨⟨ty, v⟩, hv⟩ := Option.isSome_iff_exists.mp h
    unfold Decls.variant? at hv
    obtain ⟨s, hs, hfs⟩ := List.exists_of_findSome?_eq_some hv
    obtain ⟨w, hw, _⟩ := Option.map_eq_some_iff.mp hfs
    have hname := List.find?_some hw
    simp only [beq_iff_eq] at hname
    exact Or.inl (Or.inr (List.mem_flatMap.mpr
      ⟨s, hs, List.mem_map.mpr ⟨w, List.mem_of_find?_eq_some hw, hname⟩⟩))

/-- **The checker is sound**: when `alphaCheck` accepts two programs, they are related by
    `AlphaProgram`, with `alphaPkg` and `alphaPm` computed from them. -/
theorem alphaCheck_sound {p p' : Program} (h : alphaCheck p p' = true) :
    AlphaProgram (alphaPkg (collect p).1 (collect p).2) (alphaPm (collect p).1 (collect p').1) p p' := by
  unfold alphaCheck at h
  simp only [Check.declsB, Bool.and_eq_true, beq_iff_eq] at h
  obtain ⟨⟨⟨⟨⟨hf, hs⟩, hsh⟩, hse⟩, hu⟩, hg⟩ := h
  refine ⟨⟨?_, ?_, ?_, hsh, ?_, hu, ?_⟩, Check.fall2B_sound (fun _ _ => Check.globalB_sound) hg, ?_⟩
  · refine Check.fall2B_sound (fun a b hab => ?_) hf
    simp only [Bool.and_eq_true, beq_iff_eq] at hab
    exact ⟨hab.1.1, hab.1.2, Check.funcB_sound hab.2⟩
  · intro f hf
    unfold alphaPm
    rw [hf]
  · refine Check.fall2B_sound (fun a b hab => ?_) hs
    simp only [Bool.and_eq_true, beq_iff_eq] at hab
    exact ⟨hab.1, Check.fall2B_sound (fun _ _ => Check.fieldB_sound) hab.2⟩
  · refine Check.fall2B_sound (fun a b hab => ?_) hse
    simp only [Bool.and_eq_true, beq_iff_eq] at hab
    exact ⟨hab.1, Check.fall2B_sound (fun _ _ => Check.varB_sound) hab.2⟩
  · exact alphaPkg_decls _ _
  · intro g hg x hx
    unfold alphaPkg
    exact List.mem_append_right _ (List.mem_flatMap.mpr ⟨g, hg, hx⟩)

/-- **Programs the checker accepts run the same way** (T2, through `alphaCheck_sound`). -/
theorem alphaCheck_run {p p' : Program} (h : alphaCheck p p' = true) (n : Nat) :
    OutcomeR (run p n).1 (run p' n).1 ∧ (run p n).2 = (run p' n).2 :=
  run_alpha (alphaCheck_sound h) n

/-! ## Not vacuous

A program with a struct field default, a package-level variable, and a function whose
parameters (one with a default that reads the other) are renamed, called with a named
argument that follows the renaming, is related to its renaming: `alphaCheck` accepts the
pair (by evaluation in the kernel), so `alphaCheck_run` applies. -/

section
private def witness (x y : String) : Program :=
  let int : Option Ty := some (.name "int")
  { files := [{ path := "w.gala", package := "main", decls := [
      .struct_ "Cfg" true [⟨"tries", int, false, some (.lit (.int 3))⟩],
      .global .val ["g"] false none [.call (.name "f") [.lit (.int 1)]],
      .func ⟨"f", [⟨x, int⟩, ⟨y, int⟩], [none, some (.name x)], int,
        .binary "+" (.name x) (.name y)⟩,
      .func ⟨"main", [], [], none,
        .block [.call (.name "Println") [.call (.name "f") [.named x (.lit (.int 2))]],
          .call (.name "Println") [.name "g"]]⟩] }] }

private theorem witness_related : alphaCheck (witness "x" "y") (witness "a" "b") = true := by decide +kernel

example (n : Nat) : OutcomeR (run (witness "x" "y") n).1 (run (witness "a" "b") n).1 ∧
    (run (witness "x" "y") n).2 = (run (witness "a" "b") n).2 :=
  alphaCheck_run witness_related n

/-- Renaming `y` to `x`, so that both parameters are named `x` and the default `x` would
    read the wrong one, is rejected. -/
private theorem witness_capture : alphaCheck (witness "x" "y") (witness "x" "x") = false := by decide +kernel
end

end Gala.Spec

