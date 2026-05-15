package block

import (
	"math"
	"sort"
	"testing"
)

func TestID_Equal(t *testing.T) {
	cases := []struct {
		name string
		a, b ID
		want bool
	}{
		{"both zero", ID{}, ID{}, true},
		{"same nonzero", ID{1, 2}, ID{1, 2}, true},
		{"different client", ID{1, 2}, ID{2, 2}, false},
		{"different clock", ID{1, 2}, ID{1, 3}, false},
		{"max values", ID{math.MaxUint64, math.MaxUint64}, ID{math.MaxUint64, math.MaxUint64}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != tc.want {
				t.Errorf("%v.Equal(%v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := tc.b.Equal(tc.a); got != tc.want {
				t.Errorf("symmetry: %v.Equal(%v) = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func TestID_Less(t *testing.T) {
	cases := []struct {
		name string
		a, b ID
		want bool
	}{
		{"zero < one", ID{0, 0}, ID{0, 1}, true},
		{"client breaks tie", ID{1, 100}, ID{2, 0}, true},
		{"clock orders within client", ID{5, 1}, ID{5, 2}, true},
		{"equal not less", ID{3, 3}, ID{3, 3}, false},
		{"reverse not less", ID{2, 0}, ID{1, 100}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Less(tc.b); got != tc.want {
				t.Errorf("%v.Less(%v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestID_LessIsStrictTotalOrder(t *testing.T) {
	ids := []ID{
		{0, 0},
		{0, 1},
		{1, 0},
		{1, 1},
		{1, math.MaxUint64},
		{2, 0},
		{math.MaxUint64, 0},
		{math.MaxUint64, math.MaxUint64},
	}
	for i, a := range ids {
		if a.Less(a) {
			t.Errorf("%v.Less(self) must be false", a)
		}
		for j, b := range ids {
			if i == j {
				continue
			}
			ab := a.Less(b)
			ba := b.Less(a)
			eq := a.Equal(b)
			switch {
			case eq && (ab || ba):
				t.Errorf("equal IDs cannot be less: %v <=> %v", a, b)
			case !eq && ab == ba:
				t.Errorf("strict order violated: %v.Less(%v)=%v %v.Less(%v)=%v", a, b, ab, b, a, ba)
			}
		}
	}
}

func TestID_LessTransitivity(t *testing.T) {
	ids := []ID{
		{1, 5},
		{1, 10},
		{2, 0},
		{2, 5},
		{3, 0},
	}
	for i, a := range ids {
		for j, b := range ids {
			for k, c := range ids {
				if i == j || j == k || i == k {
					continue
				}
				if a.Less(b) && b.Less(c) && !a.Less(c) {
					t.Errorf("transitivity broken: %v < %v < %v but not %v < %v", a, b, c, a, c)
				}
			}
		}
	}
}

func TestID_SortInterop(t *testing.T) {
	ids := []ID{
		{3, 7},
		{1, 99},
		{2, 0},
		{1, 0},
		{3, 0},
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Less(ids[j]) })
	want := []ID{{1, 0}, {1, 99}, {2, 0}, {3, 0}, {3, 7}}
	for i, w := range want {
		if !ids[i].Equal(w) {
			t.Errorf("sort[%d] = %v, want %v", i, ids[i], w)
		}
	}
}

func TestID_String(t *testing.T) {
	cases := []struct {
		in   ID
		want string
	}{
		{ID{}, "0:0"},
		{ID{1, 2}, "1:2"},
		{ID{42, 0}, "42:0"},
		{ID{math.MaxUint64, math.MaxUint64}, "18446744073709551615:18446744073709551615"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestID_IsZero(t *testing.T) {
	if !(ID{}).IsZero() {
		t.Error("zero value must be IsZero")
	}
	if (ID{0, 1}).IsZero() {
		t.Error("zero client + nonzero clock is not zero")
	}
	if (ID{1, 0}).IsZero() {
		t.Error("nonzero client + zero clock is not zero")
	}
}

func FuzzIDLessAntisymmetric(f *testing.F) {
	f.Add(uint64(0), uint64(0), uint64(0), uint64(0))
	f.Add(uint64(1), uint64(2), uint64(3), uint64(4))
	f.Add(uint64(math.MaxUint64), uint64(0), uint64(0), uint64(math.MaxUint64))
	f.Fuzz(func(t *testing.T, ac, acl, bc, bcl uint64) {
		a := ID{Client: ac, Clock: acl}
		b := ID{Client: bc, Clock: bcl}
		if a.Equal(b) {
			if a.Less(b) || b.Less(a) {
				t.Errorf("equal but ordered: %v vs %v", a, b)
			}
			return
		}
		if a.Less(b) == b.Less(a) {
			t.Errorf("antisymmetry: %v.Less(%v)=%v %v.Less(%v)=%v",
				a, b, a.Less(b), b, a, b.Less(a))
		}
	})
}
