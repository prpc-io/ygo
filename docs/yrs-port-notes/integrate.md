# yrs port notes: Item.integrate and Item.try_squash

> Source: yrs commit `639db2038fa44d09f628a2650fd900e3b109ad1e` (main branch HEAD as of 2026-05-15), file `yrs/src/block.rs`.
> Functions covered:
> - `ItemPtr::integrate` — `block.rs:562-846` (header doc-comment included).
> - `ItemPtr::try_squash` — `block.rs:848-876`.
> - `BlockRange::integrate` (the GC/Skip variant) — `block.rs:1257-1264` (mentioned for completeness; trivial).
> - `Item::repair` — `block.rs:1368-1431`.
> - `ItemContent::try_squash` — `block.rs:1969-1993`.
> - Helper constants & `try_integrate` inner fn (inside integrate body).

---

## 1. integrate — verbatim Rust source

```rust
    // block.rs:562
    /// Integrates current block into block store.
    /// If it returns true, it means that the block should be deleted after being added to a block store.
    pub(crate) fn integrate(&mut self, txn: &mut TransactionMut, offset: u32) -> bool {
        let self_ptr = self.clone();                                                 // 565
        let this = self.deref_mut();                                                 // 566
        let store = txn.store_mut();                                                 // 567
        let encoding = store.offset_kind;                                            // 568
        if offset > 0 {                                                              // 569
            // offset could be > 0 only in context of Update::integrate,             // 570
            // is such case offset kind in use always means Yjs-compatible offset (utf-16) // 571
            this.id.clock += offset;                                                 // 572
            this.left = store                                                        // 573
                .blocks                                                              // 574
                .get_item_clean_end(&ID::new(this.id.client, this.id.clock - 1))     // 575
                .map(|slice| store.materialize(slice));                              // 576
            this.origin = this.left.as_deref().map(|b: &Item| b.last_id());          // 577
            this.content = this                                                      // 578
                .content                                                             // 579
                .splice(offset as usize, OffsetKind::Utf16)                          // 580
                .unwrap();                                                           // 581
            this.len -= offset;                                                      // 582
        }                                                                            // 583

        let parent = match &this.parent {                                            // 585
            TypePtr::Branch(branch) => Some(*branch),                                // 586
            TypePtr::Named(name) => {                                                // 587
                let branch = store.get_or_create_type(name.clone(), TypeRef::Undefined); // 588
                this.parent = TypePtr::Branch(branch);                               // 589
                Some(branch)                                                         // 590
            }                                                                        // 591
            TypePtr::ID(id) => {                                                     // 592
                if let Some(item) = store.blocks.get_item(id) {                      // 593
                    if let Some(branch) = item.as_branch() {                         // 594
                        this.parent = TypePtr::Branch(branch);                       // 595
                        Some(branch)                                                 // 596
                    } else {                                                         // 597
                        None                                                         // 598
                    }                                                                // 599
                } else {                                                             // 600
                    None                                                             // 601
                }                                                                    // 602
            }                                                                        // 603
            TypePtr::Unknown => return true,                                         // 604
        };                                                                           // 605

        let left: Option<&Item> = this.left.as_deref();                              // 607
        let right: Option<&Item> = this.right.as_deref();                            // 608

        let right_is_null_or_has_left = match right {                                // 610
            None => true,                                                            // 611
            Some(i) => i.left.is_some(),                                             // 612
        };                                                                           // 613
        let left_has_other_right_than_self = match left {                            // 614
            Some(i) => i.right != this.right,                                        // 615
            _ => false,                                                              // 616
        };                                                                           // 617

        if let Some(mut parent_ref) = parent {                                       // 619
            if (left.is_none() && right_is_null_or_has_left) || left_has_other_right_than_self { // 620
                // set the first conflicting item                                    // 621
                let mut o = if let Some(left) = left {                               // 622
                    left.right                                                       // 623
                } else if let Some(sub) = &this.parent_sub {                         // 624
                    let mut o = parent_ref.map.get(sub).cloned();                    // 625
                    while let Some(item) = o.as_deref() {                            // 626
                        if item.left.is_some() {                                     // 627
                            o = item.left.clone();                                   // 628
                            continue;                                                // 629
                        }                                                            // 630
                        break;                                                       // 631
                    }                                                                // 632
                    o.clone()                                                        // 633
                } else {                                                             // 634
                    parent_ref.start                                                 // 635
                };                                                                   // 636

                let mut left = this.left.clone();                                    // 638
                let mut conflicting_items = HashSet::new();                          // 639
                let mut items_before_origin = HashSet::new();                        // 640

                // Let c in conflicting_items, b in items_before_origin              // 642
                // ***{origin}bbbb{this}{c,b}{c,b}{o}***                             // 643
                // Note that conflicting_items is a subset of items_before_origin    // 644
                while let Some(item) = o {                                           // 645
                    if Some(item) == this.right {                                    // 646
                        break;                                                       // 647
                    }                                                                // 648

                    items_before_origin.insert(item);                                // 650
                    conflicting_items.insert(item);                                  // 651
                    if this.origin == item.origin {                                  // 652
                        // case 1                                                    // 653
                        if item.id.client < this.id.client {                         // 654
                            left = Some(item.clone());                               // 655
                            conflicting_items.clear();                               // 656
                        } else if this.right_origin == item.right_origin {           // 657
                            // `self` and `item` are conflicting and point to the same integration // 658
                            // points. The id decides which item comes first. Since `self` is to // 659
                            // the left of `item`, we can break here.                // 660
                            break;                                                   // 661
                        }                                                            // 662
                    } else {                                                         // 663
                        if let Some(origin_ptr) = item                               // 664
                            .origin                                                  // 665
                            .as_ref()                                                // 666
                            .and_then(|id| store.blocks.get_item(id))                // 667
                        {                                                            // 668
                            if items_before_origin.contains(&origin_ptr) {           // 669
                                if !conflicting_items.contains(&origin_ptr) {        // 670
                                    left = Some(item.clone());                       // 671
                                    conflicting_items.clear();                       // 672
                                }                                                    // 673
                            } else {                                                 // 674
                                break;                                               // 675
                            }                                                        // 676
                        } else {                                                     // 677
                            break;                                                   // 678
                        }                                                            // 679
                    }                                                                // 680
                    o = item.right.clone();                                          // 681
                }                                                                    // 682
                this.left = left;                                                    // 683
            }                                                                        // 684

            if this.parent_sub.is_none() {                                           // 686
                if let Some(item) = this.left.as_deref() {                           // 687
                    if item.parent_sub.is_some() {                                   // 688
                        this.parent_sub = item.parent_sub.clone();                   // 689
                    } else if let Some(item) = this.right.as_deref() {               // 690
                        this.parent_sub = item.parent_sub.clone();                   // 691
                    }                                                                // 692
                }                                                                    // 693
            }                                                                        // 694

            // reconnect left/right                                                  // 696
            if let Some(left) = this.left.as_deref_mut() {                           // 697
                this.right = left.right.replace(self_ptr);                           // 698
            } else {                                                                 // 699
                let r = if let Some(parent_sub) = &this.parent_sub {                 // 700
                    // update parent map/start if necessary                          // 701
                    let mut r = parent_ref.map.get(parent_sub).cloned();             // 702
                    while let Some(item) = r {                                       // 703
                        if item.left.is_some() {                                     // 704
                            r = item.left;                                           // 705
                        } else {                                                     // 706
                            break;                                                   // 707
                        }                                                            // 708
                    }                                                                // 709
                    r                                                                // 710
                } else {                                                             // 711
                    let start = parent_ref.start.replace(self_ptr);                  // 712
                    start                                                            // 713
                };                                                                   // 714
                this.right = r;                                                      // 715
            }                                                                        // 716

            if let Some(right) = this.right.as_deref_mut() {                         // 718
                right.left = Some(self_ptr);                                         // 719
            } else if let Some(parent_sub) = &this.parent_sub {                      // 720
                // set as current parent value if right === null and this is parentSub // 721
                parent_ref.map.insert(parent_sub.clone(), self_ptr);                 // 722
                if let Some(mut left) = this.left {                                  // 723
                    #[cfg(feature = "weak")]                                         // 724
                    {                                                                // 725
                        if left.info.is_linked() {                                   // 726
                            // inherit links from the block we're overriding         // 727
                            left.info.clear_linked();                                // 728
                            this.info.set_linked();                                  // 729
                            let all_links = &mut txn.store.linked_by;                // 730
                            if let Some(linked_by) = all_links.remove(&left) {       // 731
                                all_links.insert(self_ptr, linked_by);               // 732
                                // since left is being deleted, it will remove       // 733
                                // its links from store.linkedBy anyway              // 734
                            }                                                        // 735
                        }                                                            // 736
                    }                                                                // 737
                    // this is the current attribute value of parent. delete right   // 738
                    txn.delete(left);                                                // 739
                }                                                                    // 740
            }                                                                        // 741

            // adjust length of parent                                               // 743
            if this.parent_sub.is_none() && !this.is_deleted() {                     // 744
                if this.is_countable() {                                             // 745
                    // adjust length of parent                                       // 746
                    parent_ref.block_len += this.len;                                // 747
                    parent_ref.content_len += this.content_len(encoding);            // 748
                }                                                                    // 749
                #[cfg(feature = "weak")]                                             // 750
                match (this.left, this.right) {                                      // 751
                    (Some(l), Some(r)) if l.info.is_linked() || r.info.is_linked() => { // 752
                        crate::types::weak::join_linked_range(self_ptr, txn)         // 753
                    }                                                                // 754
                    _ => {}                                                          // 755
                }                                                                    // 756
            }                                                                        // 757

            // check if this item is in a moved range                                // 759
            let left_moved = this.left.and_then(|i| i.moved);                        // 760
            let right_moved = this.right.and_then(|i| i.moved);                      // 761
            if left_moved.is_some() || right_moved.is_some() {                       // 762
                if left_moved == right_moved {                                       // 763
                    this.moved = left_moved;                                         // 764
                } else {                                                             // 765
                    #[inline]                                                        // 766
                    fn try_integrate(mut item: ItemPtr, txn: &mut TransactionMut) {  // 767
                        let ptr = item.clone();                                      // 768
                        if let ItemContent::Move(m) = &mut item.content {            // 769
                            if !m.is_collapsed() {                                   // 770
                                m.integrate_block(txn, ptr);                         // 771
                            }                                                        // 772
                        }                                                            // 773
                    }                                                                // 774

                    if let Some(ptr) = left_moved {                                  // 776
                        try_integrate(ptr, txn);                                     // 777
                    }                                                                // 778

                    if let Some(ptr) = right_moved {                                 // 780
                        try_integrate(ptr, txn);                                     // 781
                    }                                                                // 782
                }                                                                    // 783
            }                                                                        // 784

            match &mut this.content {                                                // 786
                ItemContent::Deleted(len) => {                                       // 787
                    txn.delete_set.insert(this.id, *len);                            // 788
                    this.mark_as_deleted();                                          // 789
                }                                                                    // 790
                ItemContent::Move(m) => m.integrate_block(txn, self_ptr),            // 791
                ItemContent::Doc(parent_doc, doc) => {                               // 792
                    *parent_doc = Some(txn.doc().clone());                           // 793
                    {                                                                // 794
                        let mut child_txn = doc.transact_mut();                      // 795
                        child_txn.store.parent = Some(self_ptr);                     // 796
                    }                                                                // 797
                    let subdocs = txn.subdocs.get_or_init();                         // 798
                    subdocs.added.insert(DocAddr::new(doc), doc.clone());            // 799
                    if doc.should_load() {                                           // 800
                        subdocs.loaded.insert(doc.addr(), doc.clone());              // 801
                    }                                                                // 802
                }                                                                    // 803
                ItemContent::Format(_, _) => {                                       // 804
                    // @todo searchmarker are currently unsupported for rich text documents // 805
                    // /** @type {AbstractType<any>} */ (item.parent)._searchMarker = null  // 806
                }                                                                    // 807
                ItemContent::Type(branch) => {                                       // 808
                    let ptr = BranchPtr::from(branch);                               // 809
                    #[cfg(feature = "weak")]                                         // 810
                    if let TypeRef::WeakLink(source) = &ptr.type_ref {               // 811
                        source.materialize(txn, ptr);                                // 812
                    }                                                                // 813
                }                                                                    // 814
                _ => {                                                               // 815
                    // other types don't define integration-specific actions         // 816
                }                                                                    // 817
            }                                                                        // 818
            txn.add_changed_type(parent_ref, this.parent_sub.clone());               // 819
            if this.info.is_linked() {                                               // 820
                if let Some(links) = txn.store.linked_by.get(&self_ptr).cloned() {   // 821
                    // notify links about changes                                    // 822
                    for link in links.iter() {                                       // 823
                        txn.add_changed_type(*link, this.parent_sub.clone());        // 824
                    }                                                                // 825
                }                                                                    // 826
            }                                                                        // 827
            let parent_deleted = if let TypePtr::Branch(ptr) = &this.parent {        // 828
                if let Some(block) = ptr.item {                                      // 829
                    block.is_deleted()                                               // 830
                } else {                                                             // 831
                    false                                                            // 832
                }                                                                    // 833
            } else {                                                                 // 834
                false                                                                // 835
            };                                                                       // 836
            if parent_deleted || (this.parent_sub.is_some() && this.right.is_some()) { // 837
                // delete if parent is deleted or if this is not the current attribute value of parent // 838
                true                                                                 // 839
            } else {                                                                 // 840
                false                                                                // 841
            }                                                                        // 842
        } else {                                                                     // 843
            true                                                                     // 844
        }                                                                            // 845
    }                                                                                // 846
```

Note: `txn.merge_blocks` is **not** updated by `integrate` itself. That bookkeeping is done by the caller (the Update apply loop) after a successful integrate. See section 7 below.

---

## 2. integrate — line-by-line explanation

### 2.1 Signature & receiver (lines 564-568)

```rust
pub(crate) fn integrate(&mut self, txn: &mut TransactionMut, offset: u32) -> bool
```

- Defined on `impl ItemPtr`. `self: &mut ItemPtr` is a `NonNull<Item>` newtype with `Deref/DerefMut` to `Item`.
- `self_ptr = self.clone()` (line 565) is a `Copy` of the pointer used as the value to insert into neighbours' `left`/`right` and into `parent.map`/`parent.start`. **Crucial**: yrs writes the pointer-to-self into the doubly-linked list while still holding `&mut` access to the underlying `Item`. We must do the same in Go (write `*Item` self-references into neighbours).
- `this = self.deref_mut()` (line 566) gets `&mut Item` for field access. From here, `this.X` mutates fields of the item being integrated.
- `store = txn.store_mut()` (line 567) and `encoding = store.offset_kind` (line 568) cache the store handle and the configured offset kind (Utf8 / Utf16 / Utf32). The offset kind affects `content_len(encoding)` later.

**Return semantics (preview)**: `true` means *the caller should immediately delete this item after registering it in the block store*. This is **NOT** "drop the item, do not insert" — it is *insert, then mark deleted*. See section 2.13.

### 2.2 Offset adjustment (lines 569-583)

```rust
if offset > 0 { ... }
```

Used only by `Update::integrate` when a remote update arrives partially overlapping with already-known content (the common "we already have clock 0..3 of client X, the update brings 0..7" case — we need to integrate only the suffix 3..7).

- Line 572: bump `id.clock` past the already-integrated prefix.
- Lines 573-576: re-resolve `this.left` to the item ending at `clock - 1`. `get_item_clean_end` returns a `BlockSlice` (pre-split if needed); `materialize` actually performs the split and returns an `ItemPtr`. **This may split an existing item in two.**
- Line 577: recompute `origin` to be the last id of the new left.
- Lines 578-581: trim the content to skip the first `offset` units. `splice(offset, Utf16)` returns the suffix and discards the prefix. **The prefix is silently dropped** — it's the caller's responsibility to have already integrated it.
- Line 582: shrink `len`.

The hard-coded `OffsetKind::Utf16` on line 580 is intentional: updates over the wire use Yjs UTF-16 offsets regardless of what the local store uses.

### 2.3 Parent resolution (lines 585-605)

```rust
let parent = match &this.parent {
    TypePtr::Branch(branch) => Some(*branch),
    TypePtr::Named(name) => {
        let branch = store.get_or_create_type(name.clone(), TypeRef::Undefined);
        this.parent = TypePtr::Branch(branch);
        Some(branch)
    }
    TypePtr::ID(id) => {
        if let Some(item) = store.blocks.get_item(id) {
            if let Some(branch) = item.as_branch() {
                this.parent = TypePtr::Branch(branch);
                Some(branch)
            } else { None }
        } else { None }
    }
    TypePtr::Unknown => return true,
};
```

- `Branch(branch)` — already resolved, just unwrap.
- `Named(name)` — root-level type. `get_or_create_type` lazily creates a `Branch` of `TypeRef::Undefined` (the concrete type is fixed when the user calls `txn.get_text("name")` etc.). The item's `parent` is mutated in-place to the resolved `Branch` so we never re-resolve.
- `ID(id)` — nested type whose parent is another item. Look up the item; if it carries an `ItemContent::Type(branch)`, use that branch. If the item exists but isn't a Type, `parent` stays `None` (will hit the catch-all `else { true }` on line 844 → caller deletes). If the item doesn't exist at all, same outcome.
- `Unknown` — early return `true`. **This is the "drop and let caller retry from pending buffer" path**. The item is malformed/unresolvable; the caller is expected to push it onto the pending queue.

### 2.4 Left/right snapshots & conflict-trigger conditions (lines 607-617)

```rust
let left  = this.left.as_deref();
let right = this.right.as_deref();

let right_is_null_or_has_left = match right { None => true, Some(i) => i.left.is_some() };
let left_has_other_right_than_self = match left { Some(i) => i.right != this.right, _ => false };
```

These are the two flags that gate entry into the YATA conflict-resolution loop. Read them as:

- `right_is_null_or_has_left` — true when there is nothing to our right, OR our `right` is "occupied" (it has someone to its left already, i.e. there are concurrent items between our left and our right).
- `left_has_other_right_than_self` — our `left` exists and it points to something different than what we think our right is. Means somebody slipped in between left and us.

Combined with `left.is_none() && right_is_null_or_has_left`, this catches the "head insert with concurrent contention" case.

### 2.5 Conflict-resolution loop (lines 619-684) — YATA

Wrapped in `if let Some(mut parent_ref) = parent` — if parent resolution failed (None case in 2.3), we skip the entire body and fall through to the final `else { true }` on line 844.

#### 2.5.1 Pick the starting cursor `o` (lines 622-636)

```rust
let mut o = if let Some(left) = left {
    left.right                     // walk right from our nominal left
} else if let Some(sub) = &this.parent_sub {
    let mut o = parent_ref.map.get(sub).cloned();
    while let Some(item) = o.as_deref() {
        if item.left.is_some() { o = item.left.clone(); continue; }
        break;
    }
    o.clone()                      // walk to the head of the map-key chain
} else {
    parent_ref.start               // head of the parent's main list
};
```

- Case A: we have a left → start scanning at `left.right`.
- Case B: no left, but we have a `parent_sub` (map key) → walk the map[key] entry left-most to find the *head* of that key's chain, scan from there.
- Case C: no left, no `parent_sub` → start at `parent.start` (head of the array list).

#### 2.5.2 The walk (lines 638-682)

Two `HashSet<ItemPtr>` track:
- `conflicting_items` — items we are currently "fighting" against for placement.
- `items_before_origin` — every item the cursor has visited (always a superset of conflicting_items per the comment at line 644).

The invariant comment at line 643 reads: `***{origin}bbbb{this}{c,b}{c,b}{o}***` — meaning origin is to the far left, then come "before-origin" items (b), then where we want to insert (this), then conflicting items (c) which are also b, then current cursor (o) at the far right.

For each `item` the cursor visits:
1. **Stop if we hit our nominal right** (line 646) — we've gone past where we belong.
2. Insert into both sets (lines 650-651).
3. **Same-origin branch (line 652-662, "case 1")**:
   - If `item.id.client < this.id.client` — item wins by client ID tiebreaker. We move our left to *after* item, clear the conflicting set, keep walking.
   - Else if `this.right_origin == item.right_origin` — fully tied AND we lose by client ID, so we **break**. Our left stays where it last was.
4. **Different-origin branch (line 663-680)**:
   - Look up `item.origin` in the store.
   - If item's origin was already visited (`items_before_origin.contains(&origin_ptr)`) AND it was NOT a conflict (`!conflicting_items.contains(&origin_ptr)`) → item dominates, advance our left, clear conflicts.
   - If item's origin was visited AND was a conflict → fall through (do nothing, advance cursor).
   - If item's origin was NOT visited → **break** (we're now past origin's transitive reach).
   - If item has no origin or origin is unresolvable → **break**.
5. Advance: `o = item.right.clone()` (line 681).

After the loop, commit the chosen `left` back to `this.left` (line 683).

**Pointer identity note**: `HashSet<ItemPtr>` uses `Hash`/`Eq` — but per `block.rs:913-920`, `ItemPtr::Eq` compares by `self.id() == other.id()`, not pointer address. The `Hash` derive on `ItemPtr` is not in this file but presumably uses the same id-derived hash. So in Go, the set should be keyed by `block.ID`, not by `*Item` pointer. **Or** — and this is what I recommend — keyed by `*Item` pointer, since within a single integrate call no two distinct `*Item` pointers ever share an ID (the store is the source of truth). Pointer identity is faster and equivalent here. See section 7.

### 2.6 ParentSub inheritance (lines 686-694)

```rust
if this.parent_sub.is_none() {
    if let Some(item) = this.left.as_deref() {
        if item.parent_sub.is_some() {
            this.parent_sub = item.parent_sub.clone();
        } else if let Some(item) = this.right.as_deref() {
            this.parent_sub = item.parent_sub.clone();
        }
    }
}
```

If we don't have a `parent_sub` set but a neighbour does, inherit it. **Subtle**: the `else if` is checked only if `left` exists but lacks parent_sub — it does not consider `right` when `left` is absent. This matches Yjs's `Item.integrate` behavior. The `parent_sub` is taken from `right` only when `left` is present. (I read this pattern as a bug-compatible choice with Yjs; do not "fix" it in the Go port.)

### 2.7 Doubly-linked list rewiring (lines 696-741)

#### 2.7.1 Splice into the list (lines 697-716)

```rust
if let Some(left) = this.left.as_deref_mut() {
    this.right = left.right.replace(self_ptr);   // swap: this.right = old left.right; left.right = self_ptr
} else {
    let r = if let Some(parent_sub) = &this.parent_sub {
        // walk parent.map[sub] left-most to find chain head
        let mut r = parent_ref.map.get(parent_sub).cloned();
        while let Some(item) = r {
            if item.left.is_some() { r = item.left; }
            else { break; }
        }
        r
    } else {
        let start = parent_ref.start.replace(self_ptr);   // become the new head, take the old head as our right
        start
    };
    this.right = r;
}
```

- If we have a left → standard linked list insert: take left's `right` as our `right`, set left's `right` to us. `Option::replace` returns the old value and stores the new — so `this.right = left.right.replace(self_ptr)` is `this.right = old_left_right; left.right = self_ptr`.
- If we have no left (head insert):
  - With `parent_sub` → walk the map chain to its head and use that as our right (we don't reassign `parent.map[sub]` here — that happens below at line 722 only when we end up tail-most).
  - Without `parent_sub` → become the new `parent.start`; the old `parent.start` becomes our right.

#### 2.7.2 Wire up our right's back-pointer & map-key tail handling (lines 718-741)

```rust
if let Some(right) = this.right.as_deref_mut() {
    right.left = Some(self_ptr);
} else if let Some(parent_sub) = &this.parent_sub {
    parent_ref.map.insert(parent_sub.clone(), self_ptr);
    if let Some(mut left) = this.left {
        // (weak-link inheritance, omitted under default features)
        txn.delete(left);     // tombstone the previous map-key winner
    }
}
```

- If we have a right → set its `left` to us. Standard.
- If we have NO right (we are tail-most) AND we have a `parent_sub` (map-key write):
  - Update `parent.map[parent_sub] = self_ptr`. We are the new "current value" for this key.
  - If we also had a `left`, that left was the previous winner: **tombstone it** via `txn.delete(left)`. This is the "last write wins" enforcement for map keys.
  - The `weak` feature block (lines 724-737) handles inheritance of weak-link references from the deleted left — **OMIT** in our Go port (we're not implementing weak refs in the first pass; add to tech-debt).

### 2.8 Parent length adjustment (lines 743-757)

```rust
if this.parent_sub.is_none() && !this.is_deleted() {
    if this.is_countable() {
        parent_ref.block_len += this.len;
        parent_ref.content_len += this.content_len(encoding);
    }
    // (weak: join_linked_range — omitted)
}
```

Only update `parent.block_len` / `parent.content_len` when:
- This is a positional insert (`parent_sub.is_none()`) — map writes don't grow parent length.
- This isn't already deleted (e.g. `ItemContent::Deleted` — but at this point in the body `mark_as_deleted` hasn't been called yet for the Deleted variant; it happens lower at line 789. So `is_deleted()` here is true only if the item was already marked deleted before integrate, which is unusual).
- This is countable (`is_countable()` — false for Format markers and Deleted content; true for String/Any/JSON/Embed/Binary/Type/Doc/Move).

### 2.9 Move-range membership (lines 759-784)

```rust
let left_moved  = this.left.and_then(|i| i.moved);
let right_moved = this.right.and_then(|i| i.moved);
if left_moved.is_some() || right_moved.is_some() {
    if left_moved == right_moved {
        this.moved = left_moved;
    } else {
        // re-integrate the move blocks on either side
        try_integrate(left_moved, txn);
        try_integrate(right_moved, txn);
    }
}
```

If both neighbours belong to the same `moved` group, we inherit it. Otherwise, re-integrate the boundary `Move` items so they recompute coverage. **OMIT for first pass** — we're not implementing the Move CRDT yet. Add to tech-debt.

### 2.10 Per-content-type integration (lines 786-818)

```rust
match &mut this.content {
    ItemContent::Deleted(len) => {
        txn.delete_set.insert(this.id, *len);
        this.mark_as_deleted();
    }
    ItemContent::Move(m)    => m.integrate_block(txn, self_ptr),
    ItemContent::Doc(parent_doc, doc) => {
        *parent_doc = Some(txn.doc().clone());
        { let mut child_txn = doc.transact_mut();
          child_txn.store.parent = Some(self_ptr); }
        let subdocs = txn.subdocs.get_or_init();
        subdocs.added.insert(DocAddr::new(doc), doc.clone());
        if doc.should_load() { subdocs.loaded.insert(doc.addr(), doc.clone()); }
    }
    ItemContent::Format(_, _) => { /* searchmarker invalidation, no-op in yrs */ }
    ItemContent::Type(branch) => {
        let ptr = BranchPtr::from(branch);
        // (weak-link materialize — omitted)
    }
    _ => {}
}
```

- **Deleted**: register in the txn's delete-set and mark the flag. (Our Go side stores deleted IDs in `TransactionMut.deletedIDs` — we should mirror this.)
- **Move**: defer — not implementing.
- **Doc** (subdoc): set the parent-doc back-pointer, register as a child with the parent transaction's subdoc set. **Defer** subdoc handling for first pass; mark with FIXME.
- **Format**: comment-only no-op; the JS version invalidates a searchmarker that yrs doesn't have.
- **Type**: yrs only does work when the type is a WeakLink (omitted). For all normal nested types, this is effectively a no-op. **Important**: nothing here back-fills the new branch's `branch.item` field — that must already be set when the Item was constructed (or it's set in `Item::new`/equivalent).
- `_` catch-all: String / Any / JSON / Embed / Binary — no integration-time work.

### 2.11 Change tracking (lines 819-827)

```rust
txn.add_changed_type(parent_ref, this.parent_sub.clone());
if this.info.is_linked() {
    if let Some(links) = txn.store.linked_by.get(&self_ptr).cloned() {
        for link in links.iter() {
            txn.add_changed_type(*link, this.parent_sub.clone());
        }
    }
}
```

- Always record that `parent_ref` (with this `parent_sub`) was changed in this transaction. This drives observer firing at txn commit.
- The `is_linked` weak-ref propagation block: **OMIT** for first pass.

In Go, this is: `txn.changedTypes[parent] = append(..., parent_sub)` or however we structure that map. Currently `TransactionMut.changedTypes` is `map[*block.Branch]struct{}` per the prompt — that's insufficient to track *which keys* changed per branch. We will likely need to widen it to `map[*Branch]map[string]struct{}` or `map[*Branch]map[Optional[string]]struct{}`. Add to tech-debt: **changedTypes shape needs to carry the parent_sub set per branch**.

### 2.12 parent_deleted check (lines 828-836)

```rust
let parent_deleted = if let TypePtr::Branch(ptr) = &this.parent {
    if let Some(block) = ptr.item { block.is_deleted() } else { false }
} else { false };
```

A branch is "parent-deleted" if its back-pointer item exists and is itself deleted. Root-level branches have `branch.item == None` and are never parent-deleted.

### 2.13 Return value (lines 837-845)

```rust
if parent_deleted || (this.parent_sub.is_some() && this.right.is_some()) {
    true   // caller will delete()
} else {
    false  // keep
}
// outer else (parent was None):
true       // caller will delete()
```

Two truth conditions:
1. **Parent is deleted** — auto-tombstone any insert into a dead branch.
2. **Map-key write but not tail-most** (`parent_sub.is_some() && right.is_some()`) — the conflict resolver placed us in the middle of the map-key chain, so we lost the LWW race. Auto-tombstone.

Plus the parent-resolution-failed catch-all: also `true`.

**Caller contract**: If integrate returns `true`, the caller (in `Update::integrate`) calls something like `txn.delete(self_ptr)` immediately. The item is still in the block store; it's just flagged deleted. So `true` here is **NOT** "skip insertion, retry later" — that's signalled by `TypePtr::Unknown` returning early (line 604) where the item *also* gets returned as `true` but the calling Update path treats unresolved-parent specially. Actually — re-reading carefully: line 604 returns `true` meaning "delete me". But the Update layer's pending-buffer logic is keyed on missing `origin` / `right_origin` items, not on integrate's return value. **The "retry from pending" path is implemented in the caller before integrate is even invoked** — `repair` (section 6) and `missing` checks happen first. Integrate is only called once parent + origin + right_origin are all resolvable.

This is critical for our Go port. See section 7's "Pending update buffering" answer.

---

## 3. try_squash — verbatim Rust source

```rust
    // block.rs:848
    /// Squashes two blocks together. Returns true if it succeeded. Squashing is possible only if
    /// blocks are of the same type, their contents are of the same type, they belong to the same
    /// parent data structure, their IDs are sequenced directly one after another and they point to
    /// each other as their left/right neighbors respectively.
    pub(crate) fn try_squash(&mut self, other: ItemPtr) -> bool {                    // 852
        if self.id.client == other.id.client                                         // 853
            && self.id.clock + self.len() == other.id.clock                          // 854
            && other.origin == Some(self.last_id())                                  // 855
            && self.right_origin == other.right_origin                               // 856
            && self.right == Some(other)                                             // 857
            && self.is_deleted() == other.is_deleted()                               // 858
            && (self.redone.is_none() && other.redone.is_none())                     // 859
            && (!self.info.is_linked() && !other.info.is_linked()) // linked items cannot be merged // 860
            && self.moved == other.moved                                             // 861
            && self.content.try_squash(&other.content)                               // 862
        {                                                                            // 863
            self.len = self.content.len(OffsetKind::Utf16);                          // 864
            if let Some(mut right_right) = other.right {                             // 865
                right_right.left = Some(*self);                                      // 866
            }                                                                        // 867
            if other.info.is_keep() {                                                // 868
                self.info.set_keep();                                                // 869
            }                                                                        // 870
            self.right = other.right;                                                // 871
            true                                                                     // 872
        } else {                                                                     // 873
            false                                                                    // 874
        }                                                                            // 875
    }                                                                                // 876
```

---

## 4. try_squash — explanation

`try_squash(self, other)` attempts to merge two adjacent items `self` and `other` (where `other` should be `self.right`) into a single longer item. It returns true on success; on success, `self.len` grows, `self.right` is updated, `other.right.left` is rewired, and the caller is expected to drop `other` from the block store.

The refusal predicates (all must hold to merge):

- **A. Same client** — `self.id.client == other.id.client`. Different-client items can never share an ID range.
- **B. Adjacent clocks** — `self.id.clock + self.len() == other.id.clock`. Strict numerical contiguity, no gap.
- **C. Origin chain matches** — `other.origin == Some(self.last_id())`. `other` was integrated as the immediate right of `self`'s last sub-id.
- **D. Same right_origin** — both items wanted the same right neighbour at integrate time (this implies they were inserted into the same conflict region).
- **E. Currently adjacent in the linked list** — `self.right == Some(other)`. ID adjacency without linked-list adjacency is insufficient because something concurrent might sit between them.
- **F. Same deleted state** — `self.is_deleted() == other.is_deleted()`. Don't merge a live and a tombstoned run.
- **G. Neither is in an undo-redo trail** — `redone.is_none()` for both.
- **H. Neither is weak-linked** — `!is_linked()` for both. (We can drop the link guard in our first-pass Go port since we don't implement weak refs, but keep an extension point.)
- **I. Same `moved` membership** — `self.moved == other.moved` (Option equality; both None or both Some(same Move id)). Drop in first pass since we don't implement Move yet.
- **J. Content is per-variant squashable** — `self.content.try_squash(&other.content)` actually performs the content concat and returns false if the content variant doesn't support merging or the variants don't match.

On success (lines 864-871):
1. `self.len = self.content.len(Utf16)` — content has been concatenated; recompute length (always in UTF-16 units for storage consistency).
2. If `other.right` exists, rewire its `left` back-pointer to `self` (since `other` is going away).
3. If `other.info.is_keep()`, propagate the keep flag onto `self` (an undo manager marker).
4. `self.right = other.right` — skip past `other`.

Note: **try_squash does not update `parent.map` or `parent.start`**. It assumes `other` is not pointed-to by either (otherwise `other` would have been a chain-head, which would have meant `self.right == Some(other)` is false because `self` would have been to the right or equal). The caller (in the main txn commit / merge phase, via `txn.merge_blocks`) must not feed a chain-head as `other` to try_squash.

---

## 5. Content::try_squash

```rust
    // block.rs:1969
    /// Tries to squash two item content structures together.
    /// Returns `true` if this method had any effect on current [ItemContent] (modified it).
    /// Otherwise returns `false`.
    pub fn try_squash(&mut self, other: &Self) -> bool {                             // 1972
        //TODO: change `other` to Self (not ref) and return type to Option<Self> (none if merge suceeded) // 1973
        match (self, other) {                                                        // 1974
            (ItemContent::Any(v1), ItemContent::Any(v2)) => {                        // 1975
                v1.append(&mut v2.clone());                                          // 1976
                true                                                                 // 1977
            }                                                                        // 1978
            (ItemContent::Deleted(v1), ItemContent::Deleted(v2)) => {                // 1979
                *v1 = *v1 + *v2;                                                     // 1980
                true                                                                 // 1981
            }                                                                        // 1982
            (ItemContent::JSON(v1), ItemContent::JSON(v2)) => {                      // 1983
                v1.append(&mut v2.clone());                                          // 1984
                true                                                                 // 1985
            }                                                                        // 1986
            (ItemContent::String(v1), ItemContent::String(v2)) => {                  // 1987
                v1.push_str(v2.as_str());                                            // 1988
                true                                                                 // 1989
            }                                                                        // 1990
            _ => false,                                                              // 1991
        }                                                                            // 1992
    }                                                                                // 1993
```

Squashable variants:
- **Any** — append the `Vec<Any>` payloads.
- **Deleted** — sum the lengths (it's a tombstone-run length).
- **JSON** — append the `Vec<String>` payloads.
- **String** — UTF-8 string concat (yrs uses `SmallString`).

Non-squashable: **Binary, Embed, Format, Type, Doc, Move**. Each is "one item per". Embeds and binaries are atomic per-insert; Format markers carry attribute deltas that don't combine; Type/Doc/Move are per-instance.

There's a `TODO` at line 1973 in upstream — non-actionable for us.

---

## 6. Item::repair — verbatim and explanation

```rust
    // block.rs:1368
    pub(crate) fn repair(&mut self, store: &mut Store) -> Result<(), UpdateError> {  // 1368
        if let Some(origin) = self.origin.as_ref() {                                 // 1369
            self.left = store                                                        // 1370
                .blocks                                                              // 1371
                .get_item_clean_end(origin)                                          // 1372
                .map(|slice| store.materialize(slice));                              // 1373
        }                                                                            // 1374

        if let Some(origin) = self.right_origin.as_ref() {                           // 1376
            self.right = store                                                       // 1377
                .blocks                                                              // 1378
                .get_item_clean_start(origin)                                        // 1379
                .map(|slice| store.materialize(slice));                              // 1380
        }                                                                            // 1381

        // We have all missing ids, now find the items                               // 1383
        // [comment block omitted from quote — see source 1385-1390]

        self.parent = match &self.parent {                                           // 1392
            TypePtr::Branch(branch_ptr) => TypePtr::Branch(*branch_ptr),             // 1393
            TypePtr::Unknown => match (self.left, self.right) {                      // 1394
                (Some(item), _) if item.parent != TypePtr::Unknown => {              // 1395
                    self.parent_sub = item.parent_sub.clone();                       // 1396
                    item.parent.clone()                                              // 1397
                }                                                                    // 1398
                (_, Some(item)) if item.parent != TypePtr::Unknown => {              // 1399
                    self.parent_sub = item.parent_sub.clone();                       // 1400
                    item.parent.clone()                                              // 1401
                }                                                                    // 1402
                _ => TypePtr::Unknown,                                               // 1403
            },                                                                       // 1404
            TypePtr::Named(name) => {                                                // 1405
                let branch = store.get_or_create_type(name.clone(), TypeRef::Undefined); // 1406
                TypePtr::Branch(branch)                                              // 1407
            }                                                                        // 1408
            TypePtr::ID(id) => {                                                     // 1409
                let ptr = store.blocks.get_item(id);                                 // 1410
                if let Some(item) = ptr {                                            // 1411
                    match &item.content {                                            // 1412
                        ItemContent::Type(branch) => {                               // 1413
                            TypePtr::Branch(BranchPtr::from(branch.as_ref()))        // 1414
                        }                                                            // 1415
                        ItemContent::Deleted(_) => TypePtr::Unknown,                 // 1416
                        other => {                                                   // 1417
                            return Err(UpdateError::InvalidParent(                   // 1418
                                id.clone(),                                          // 1419
                                other.get_ref_number(),                              // 1420
                            ))                                                       // 1421
                        }                                                            // 1422
                    }                                                                // 1423
                } else {                                                             // 1424
                    TypePtr::Unknown                                                 // 1425
                }                                                                    // 1426
            }                                                                        // 1427
        };                                                                           // 1428

        Ok(())                                                                       // 1430
    }                                                                                // 1431
```

### Repair behaviour

`repair` is called by the Update apply loop **before** `integrate`. It does three things:

1. **Resolve `left` from `origin`** (lines 1369-1374): if we have an `origin` ID but no resolved `left` pointer, look it up via `get_item_clean_end` (which returns the item ending exactly at the origin's last clock — splitting if needed). `materialize` performs the split.
2. **Resolve `right` from `right_origin`** (lines 1376-1381): symmetric, via `get_item_clean_start`.
3. **Fix up parent** (lines 1392-1428):
   - Already a `Branch` → leave alone (clone the BranchPtr).
   - `Unknown` → **inherit from a neighbour** (the load-bearing case for our port):
     - If left exists and has a known parent, take left's parent + parent_sub.
     - Else if right exists and has a known parent, take right's parent + parent_sub.
     - Else stay Unknown.
   - `Named(name)` → resolve via `get_or_create_type` (creates an `Undefined`-kind root branch lazily).
   - `ID(id)` → look up the item; if it's a Type, use its branch; if it's a Deleted run, mark Unknown; otherwise return `Err(UpdateError::InvalidParent)`.

**Why we need this for Go**: until our types layer has `get_or_create_type`, integrate's `Named` arm will fail. Repair shows us the fallback — Unknown-parent neighbours can be resolved by inheriting from already-integrated neighbours. For initial wiring, our Go port can:
- Skip the Named arm by hardcoding root types ahead of integration in tests.
- Implement only the `Branch` and `Unknown→neighbour` arms. Document the `Named` and `ID` arms as TODO.

---

## 7. Concrete Go translation choices

### 7.1 IntegrateContext interface shape

Item.Integrate needs the following from its context (a TransactionMut). Define this in `internal/block` to keep the package free of an import cycle on `internal/doc`:

```go
// IntegrateContext is the subset of TransactionMut that Item.Integrate consumes.
type IntegrateContext interface {
    // Store accessors
    GetItem(id ID) *Item                          // store.blocks.get_item
    GetItemCleanStart(id ID) *Item                // returns sliced item starting at id.clock
    GetItemCleanEnd(id ID) *Item                  // returns sliced item ending at id.clock (inclusive)
    Materialize(item *Item) *Item                 // no-op in our port if GetItemClean* already split
    OffsetKind() OffsetKind                       // store.offset_kind

    // Root-type resolution (for TypePtr::Named) — TODO: stub returning nil for first pass
    GetOrCreateType(name string, kind TypeRef) *Branch

    // Tombstoning (for map-key LWW + Deleted content)
    Delete(item *Item)                            // txn.delete(item)
    InsertDeleteSet(id ID, length uint32)         // txn.delete_set.insert

    // Change tracking
    AddChangedType(parent *Branch, parentSub *string)

    // Subdoc registration — defer; stub for first pass
    // RegisterSubdoc(item *Item, doc *Doc)
}
```

Notes:
- `Materialize` may collapse with `GetItemCleanStart/End` if those already perform the split eagerly. Confirm against our `BlockStore` API; if they do, drop `Materialize` from the interface.
- `GetOrCreateType` returning `nil` for an unknown name is acceptable in tests where root types are pre-created; integrate will treat nil as "parent unresolved" and return its drop-equivalent.
- `Delete` is the recursive tombstone path — it must mark `info.deleted`, append to the txn's `deleted_set`, and recursively delete children for Type contents. We don't have that helper yet; **add to tech-debt as `txn.Delete(item)` requirement**.

### 7.2 Branch fields required

Confirmed from the integrate body, the **minimum** Branch field set is:

| Field | Type | Used at lines | Purpose |
|---|---|---|---|
| `Start` | `*Item` | 635, 712 | Head of the positional list. Mutated to point at new head. |
| `Map` | `map[string]*Item` | 625, 702, 722 | Map-key → tail item. |
| `Item` | `*Item` (back-ref to owning Item) | 829 | nil for root branches; set for nested branches. Used to test `parent_deleted`. |
| `BlockLen` | `uint64` | 747 | Sum of `len` of countable, non-deleted, positional items. |
| `ContentLen` | `uint64` | 748 | Sum of `content_len(encoding)` for the same. |

The prompt's listing is correct. Additional fields you'll need shortly (not for integrate proper, but for surrounding code):
- `TypeRef` (kind tag — Array/Map/Text/XmlElement/etc.) — needed for `get_or_create_type`.
- `Name` (for root branches) — needed for `Item::encode` and reverse lookup.

Not needed by integrate but flagged by other call sites:
- `linked_by` / `is_linked` / `weak`-related fields — defer.
- `_searchMarker` — defer (yrs doesn't have it; only Yjs JS does).

### 7.3 HashSet mappings

yrs uses `HashSet<ItemPtr>` for `conflicting_items` and `items_before_origin`. Per `block.rs:913-920`, `ItemPtr::Eq` compares **by ID** (`self.id() == other.id()`). The `Hash` derive on `ItemPtr` is not in this file, but consistency with `Eq` requires it to be ID-derived.

For the Go port, since within a single integrate call the store guarantees one `*Item` per ID, **pointer-identity is equivalent** to ID-identity. Use:

```go
conflictingItems := map[*block.Item]struct{}{}
itemsBeforeOrigin := map[*block.Item]struct{}{}
```

This is faster than hashing IDs and semantically identical given the invariant. Add a comment at the declaration site explaining why pointer identity is sound here.

### 7.4 Return value

Mirror yrs exactly: `func (i *Item) Integrate(ctx IntegrateContext, offset uint32) bool` where **true means "the caller must immediately tombstone this item"**. Do not invert. Inverting would require translating the boolean at every call site and we will lose 1:1 traceability with the Rust source.

Document this in the Go doc comment verbatim:

```go
// Integrate inserts this item into the block store at its target position, performing
// YATA conflict resolution. If it returns true, the caller MUST immediately call
// ctx.Delete(item): the item is now in the store but should be tombstoned (parent
// deleted, lost map-key LWW race, or parent unresolvable).
```

### 7.5 Pending update buffering

yrs **does not handle pending buffering inside integrate**. The pending-buffer logic lives in the Update apply loop (file `yrs/src/update.rs` — outside scope of this note) and runs *before* integrate. Specifically:

1. The Update layer collects all incoming items.
2. For each item, before calling integrate, it checks if `origin`/`right_origin`/parent-`ID` references resolve in the store.
3. If any reference is unresolved, the item goes onto a pending queue, keyed by the missing client/clock.
4. When future updates arrive that supply the missing clocks, pending items are drained and re-attempted.
5. Only items with all references resolved get to call `repair()` then `integrate()`.

For our first-pass port, we don't yet have an Update apply loop or pending buffer. **Strategy**:

- Make `Integrate` strict: if `origin != nil && ctx.GetItem(*origin) == nil`, return an error sentinel (or: panic in tests, since the only caller is direct test code).
- Add to **tech-debt.md**: "Pending update buffering not implemented. Integrate currently assumes all dependencies are pre-resolved by the caller. Required before we can apply remote Updates from the wire."
- Recommended Go signature with explicit error:

  ```go
  // ErrIntegrateMissingDep is returned when integrate cannot resolve origin/right_origin/parent.
  // First-pass port: callers must pre-resolve. Once we have an Update apply loop with
  // a pending queue, this becomes a signal to enqueue.
  var ErrIntegrateMissingDep = errors.New("integrate: missing dependency")

  func (i *Item) Integrate(ctx IntegrateContext, offset uint32) (drop bool, err error)
  ```

- Then mirror yrs's `TypePtr::Unknown → return true` as `return true, nil` (caller deletes), but on missing origin/right_origin return `false, ErrIntegrateMissingDep`. The caller can then decide to either drop or queue.

### 7.6 Parent.Map writes

```go
parent.Map[*item.ParentSub] = item   // line 722 equivalent
```

Direct map assignment. Synchronization is provided by `TransactionMut` holding the doc's RWMutex in write mode for its lifetime. No additional locking needed inside Integrate. Document at the field:

```go
// Map is the map-key → tail-item mapping for map-like Branch types.
// All writes occur inside TransactionMut and are serialized by the doc's
// write lock. Concurrent reads outside any transaction MUST hold the doc's
// read lock; Integrate does not lock.
type Branch struct {
    ...
    Map map[string]*Item
    ...
}
```

---

## 8. Tests we will write

Each scenario below is one focused behaviour. Use `internal/block` tests with a fake `IntegrateContext` (a small struct with maps standing in for the BlockStore).

1. **Insert into empty parent (head case, no parent_sub)**
   `parent.Start == nil; item.left == nil; item.right == nil` → after: `parent.Start == item; item.right == nil; parent.BlockLen == item.len; integrate returns false`.

2. **Insert with origin pointing to existing item, no concurrent**
   Pre-existing item A integrated. New item B has `origin = A.LastID`, `right_origin = nil`. Expect: `A.right == B; B.left == A; parent.BlockLen += B.len`. Returns false.

3. **Two concurrent inserts at same origin — client-id tiebreaker**
   Same parent, both items have `origin = A.LastID`, `right_origin = nil`. Integrate B (client=2) then C (client=1) into a fresh replica; integrate C then B into another. Expect both replicas converge to the same `A → C → B` order (lower client wins the left position; per line 654 `item.id.client < this.id.client` means the *iterated* item moves to our left, so the lower-client item ends up further left). Verify by walking `parent.Start.right` chain in both replicas.

4. **Map-key set: previous map[key] item gets tombstoned**
   Pre-existing item A with `parent_sub = "k"` is in `parent.Map["k"]`. Integrate B with `parent_sub = "k"`, `origin = A.LastID`, `right_origin = nil`. Expect: `parent.Map["k"] == B; A.IsDeleted() == true; integrate returns false`.
   Verify `ctx.Delete(A)` was called exactly once.

5. **Insert with parent_sub but right is non-nil → auto-delete**
   Set up so that conflict resolution lands the new item with a non-nil right and a parent_sub. Expect integrate to return `true`. The simplest construction: have two items A and B already in `parent.Map["k"]`'s chain (A then B); insert C with `parent_sub = "k"`, `origin = A.LastID`, `right_origin = B.ID`. C splices between A and B (right_origin is B which is non-null). Returns true.

6. **Insert into deleted parent → auto-delete**
   Construct a Branch whose `Item` back-ref points to an Item with `info.deleted == true`. Insert any item with that branch as parent. Returns true.

7. **Insert with unresolvable origin → ErrIntegrateMissingDep (Go-specific)**
   `item.origin = ID{client:99, clock:5}` where client 99 isn't in the store. Expect: `(false, ErrIntegrateMissingDep)`. This is the gap-filling behaviour we'll wire to a pending buffer later.

8. **Squash: two adjacent String items same client → merged**
   Integrate item A=("hi", client=1, clock=0). Integrate item B=("there", client=1, clock=2, origin=A.LastID). Call `A.TrySquash(B)`. Expect: `A.len == 7; A.Content == "hithere"; A.right == B.right; returns true`.

9. **Squash refused: different deletion state**
   Same setup as #8 but mark B deleted before TrySquash. Expect: returns false.

10. **Squash refused: not adjacent in linked list**
    Setup A → C → B (where A, B are merge candidates by ID but C sits between). `A.TrySquash(B)` returns false because `A.right != Some(B)`.

11. **ParentSub inheritance from left neighbour**
    Insert A with `parent_sub = "k"`. Insert B with no `parent_sub`, `left = A`, `right = nil`. After integrate: `B.parent_sub == "k"`. (Verifies lines 686-694.)

---

## 9. Open questions / FIXMEs in source

Searching `block.rs` for `FIXME / TODO / @todo / XXX / HACK`:

- **Line 805** (inside integrate, Format arm):
  ```
  // @todo searchmarker are currently unsupported for rich text documents
  // /** @type {AbstractType<any>} */ (item.parent)._searchMarker = null
  ```
  yrs intentionally doesn't implement search markers. Yjs JS invalidates them here. No action for us — we're not implementing search markers either.

- **Line 1940** (inside `ItemContent::splice`, just outside our quoted region):
  ```
  //TODO: do we need that in Rust?
  ```
  Not relevant to integrate.

- **Line 1973** (inside `ItemContent::try_squash`):
  ```
  //TODO: change `other` to Self (not ref) and return type to Option<Self> (none if merge suceeded)
  ```
  Cosmetic API improvement; ignore.

No FIXMEs in `Item::repair` or `Item::try_squash` themselves.

### Things our port needs that yrs has but we have NOT discussed in this doc:

- **`txn.delete(item)`** — recursive tombstone. Used at line 739. We need to implement this in TransactionMut. Required for test #4 above.
- **`txn.delete_set.insert(id, len)`** — used at line 788 for Deleted content. Maps to our `TransactionMut.deletedIDs` but that's currently `[]block.ID` and the yrs version maps id → length runs (an `IdSet`). Widen the structure or accept that contiguous tombstones get squashed at commit time.
- **`txn.add_changed_type(branch, parent_sub)`** — currently our `TransactionMut.changedTypes` is `map[*block.Branch]struct{}` which loses the `parent_sub` dimension. **Tech-debt: widen to `map[*Branch]map[Optional[string]]struct{}` or equivalent.** Without this, observers can't distinguish "key X changed" from "key Y changed" on the same map branch.
- **`Item.moved` field** — referenced at lines 760-784 and 861. We don't have this. Defer (no Move implementation in first pass).
- **`Item.redone` field** — referenced at line 859. We don't have this. Defer.
- **`Item.info.is_keep()` / `set_keep()`** — referenced at lines 868-869. UndoManager-related. Defer.
- **`Item.info.is_linked()` / `set_linked()` / `clear_linked()`** — weak-link feature. Defer entirely.
- **Subdoc support (`ItemContent::Doc` integration arm, lines 792-803)** — defer; will not be exercised by initial tests.

Add all the above to `tech-debt.md` so we don't lose them.
