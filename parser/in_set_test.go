package parser

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func countSets(n *node) (c int) {
	if n == nil {
		return 0
	}
	if n.cmp != nil && (n.cmp.strSet != nil || n.cmp.intSet != nil) {
		c++
	}
	return c + countSets(n.left) + countSets(n.right)
}

func strList(r *rand.Rand, n int) string {
	pool := []string{"a", "A", "u1", "U1", "", "ß", "é", "é", "1.2.3", "5", "true", "null", "x y", `q\"q`, "日本", "İ", "i"}
	parts := make([]string, n)
	for i := range parts {
		var s string
		if r.Intn(3) == 0 {
			s = pool[r.Intn(len(pool))]
		} else {
			s = fmt.Sprintf("u%d", r.Intn(n*2+1))
		}
		parts[i] = `"` + s + `"` // (pool entry with \" is already escaped)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func intList(r *rand.Rand, n int, bad bool) string {
	parts := make([]string, n)
	for i := range parts {
		switch r.Intn(6) {
		case 0:
			parts[i] = "0"
		case 1:
			parts[i] = fmt.Sprint(r.Intn(10)) // grammar has no negative list ints
		default:
			parts[i] = fmt.Sprint(r.Intn(n * 2))
		}
	}
	if bad && n > 2 {
		parts[r.Intn(n)] = "99999999999999999999" // overflow: parse error, partial list (listErr)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func TestInSetDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	type kv = map[string]interface{}
	inputs := []kv{nil, {}}
	vals := []interface{}{"u1", "U1", "u3", "a", "A", "", "é", "é", "ß", "SS", "5", "1.2.3", "null", "i", "İ", "日本",
		0, 1, 3, -1, -0, 5, 15, int64(3), int32(3), uint(3), 3.0, 2.0, 0.0, math.Copysign(0, -1), math.NaN(), math.Inf(1), float32(3),
		true, false, nil, []string{"u1"}, []int{3}, kv{"a": 1}, testStringer{"u1"}, testStringer{"U1"}, fmt.Stringer(nil), "\xff"}
	for _, v := range vals {
		inputs = append(inputs, kv{"s": v, "n": v, "u": kv{"s": v, "n": v}})
	}
	inputs = append(inputs, kv{"n": 3, "s": "u1"}, kv{"n": "x", "s": 3}, kv{"other": 1})

	var rules []string
	for _, sz := range []int{1, 7, inSetMinLen - 1, inSetMinLen, inSetMinLen + 1, 200} {
		for k := 0; k < 2; k++ {
			sl, il, ilBad := strList(r, sz), intList(r, sz, false), intList(r, sz, true)
			rules = append(rules,
				"s in "+sl, "s IN "+sl, "u.s in "+sl, "not (s in "+sl+")", "NOT (n in "+il+")",
				"n in "+il, "u.n in "+il, "n in "+ilBad, "n in "+ilBad+" or s in "+sl,
				"s in "+sl+" and n in "+il, "(s in "+sl+") or (n in "+ilBad+")",
				"s in "+sl+" or s in "+strList(r, sz), "missing in "+sl, "missing in "+il,
				"s in "+sl+" or n in "+il+" or s eq \"u1\"",
				"n in "+il+" or n in "+intList(r, sz, false),
				"s in "+sl+" or s pr and not (n in "+il+")",
			)
		}
	}
	// float and mixed lists
	rules = append(rules, `n in [1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0]`, `n in [1,2,3,4,5,6,7,8,9,1.5]`, `s in ["a","b","c","d","e","f","g","h",1]`,
		`s in ["a","b","c","d","e","f","g","h","i"] or n in [1,2,3,4,5,6,7,8,9]`, `n in [0,0,0,0,0,0,0,0,0]`, `n in [-0,1,2,3,4,5,6,7,8]`)

	sets, total, skipped := 0, 0, 0
	for _, rule := range rules {
		repeated, err := NewEvaluator(rule)
		if err != nil {
			skipped++
			continue
		}
		tree := repeated.tree
		for i, in := range inputs {
			wantRet, wantErr, wantDebug := visitorProcess(tree, in)
			fresh := &Evaluator{rule: rule, tree: tree}
			for name, ev := range map[string]*Evaluator{"fresh": fresh, "repeated": repeated} {
				gotRet, gotErr := ev.Process(in)
				total++
				if gotRet != wantRet || errString(gotErr) != errString(wantErr) || errString(ev.LastDebugErr()) != errString(wantDebug) {
					t.Errorf("%.80q input %d %v (%s): got %v/%q/%q want %v/%q/%q", rule, i, in, name, gotRet, errString(gotErr), errString(ev.LastDebugErr()), wantRet, errString(wantErr), errString(wantDebug))
				}
			}
		}
		sets += countSets(repeated.root)
	}
	t.Logf("skipped(invalid)=%d rules=%d evaluations=%d compiled set nodes=%d", skipped, len(rules), total, sets)
	if sets == 0 {
		t.Fatal("no hash set was ever built: test is vacuous")
	}
}

// the hash set must be read-only after compile: hammer one evaluator concurrently.
func TestInSetConcurrent(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	ev, _ := NewEvaluator("s in " + strList(r, 300))
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				ev.Process(map[string]interface{}{"s": fmt.Sprintf("u%d", (i+g)%700)})
			}
		}(g)
	}
	wg.Wait()
}

func BenchmarkInLongList(b *testing.B) {
	const n = 300000
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`"user-%d"`, i)
	}
	in := map[string]interface{}{"s": "nope"}
	for _, mode := range []string{"linear", "hashset"} {
		ev, err := NewEvaluator("s in [" + strings.Join(parts, ",") + "]")
		if err != nil {
			b.Fatal(err)
		}
		ev.Process(in) // compile
		if mode == "linear" {
			clearSets(ev.root)
		}
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ev.Process(in)
			}
		})
	}
}

func clearSets(n *node) {
	if n == nil {
		return
	}
	if n.cmp != nil {
		n.cmp.strSet, n.cmp.intSet = nil, nil
	}
	clearSets(n.left)
	clearSets(n.right)
}
