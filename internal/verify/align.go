package verify

import (
	"github.com/jollaman999/why-hwp/internal/model"
)

// pair is one step of an alignment. o and n index the original and the new
// block lists; -1 means the block has no counterpart (n == -1: removed,
// o == -1: added). When both are set the texts are equal or the pair is a
// change.
type pair struct{ o, n int }

const (
	// lcsLimit caps the cells of the LCS table over the part of the two
	// documents that differs (int32 cells, so 40 MB).
	lcsLimit = 10_000_000
	// gapLimit caps the cells of the similarity table used to pair the
	// unmatched blocks between two equal blocks.
	gapLimit = 250_000
	// minPairScore is the lowest score at which an unmatched original
	// block and an unmatched new block count as one changed block.
	minPairScore = 0.3
)

// align matches the blocks of orig and cur in reading order. Blocks of the
// same kind with equal text are matched by a longest common subsequence;
// between two matched blocks, the leftovers are paired as changes by text
// similarity and location, and the rest are removals and additions.
// approx is true when the documents differ too much for the LCS table and
// the middle part was paired by position instead.
func align(orig, cur []model.Block) (pairs []pair, approx bool) {
	ids := map[string]int{}
	key := func(b model.Block) int {
		k := string(b.Kind) + "\x00" + b.Text
		id, ok := ids[k]
		if !ok {
			id = len(ids)
			ids[k] = id
		}
		return id
	}
	a := make([]int, len(orig))
	for i, b := range orig {
		a[i] = key(b)
	}
	b := make([]int, len(cur))
	for i, blk := range cur {
		b[i] = key(blk)
	}

	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pairs = append(pairs, pair{pre, pre})
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var matches []pair
	if len(ma)*len(mb) <= lcsLimit {
		matches = lcs(ma, mb)
	} else {
		approx = true
	}
	oi, ni := pre, pre
	for _, m := range matches {
		pairs = append(pairs, pairGap(orig, cur, oi, pre+m.o, ni, pre+m.n)...)
		pairs = append(pairs, pair{pre + m.o, pre + m.n})
		oi, ni = pre+m.o+1, pre+m.n+1
	}
	pairs = append(pairs, pairGap(orig, cur, oi, len(a)-suf, ni, len(b)-suf)...)
	for i := 0; i < suf; i++ {
		pairs = append(pairs, pair{len(a) - suf + i, len(b) - suf + i})
	}
	return pairs, approx
}

// lcs returns the index pairs of one longest common subsequence of a and b.
func lcs(a, b []int) []pair {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return nil
	}
	w := m + 1
	t := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i*w+j] = t[(i+1)*w+j+1] + 1
			} else if t[(i+1)*w+j] >= t[i*w+j+1] {
				t[i*w+j] = t[(i+1)*w+j]
			} else {
				t[i*w+j] = t[i*w+j+1]
			}
		}
	}
	var out []pair
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, pair{i, j})
			i++
			j++
		case t[(i+1)*w+j] >= t[i*w+j+1]:
			i++
		default:
			j++
		}
	}
	return out
}

// pairGap aligns orig[o0:o1] with cur[n0:n1], blocks none of which have an
// equal counterpart. Two blocks of the same kind become a change when they
// are similar enough or sit at the same location; the order of both lists
// is kept. Removals at one position come before additions.
func pairGap(orig, cur []model.Block, o0, o1, n0, n1 int) []pair {
	n, m := o1-o0, n1-n0
	if n == 0 && m == 0 {
		return nil
	}
	score := func(i, j int) float64 {
		ob, nb := orig[o0+i], cur[n0+j]
		if ob.Kind != nb.Kind {
			return 0
		}
		s := similarity(ob.Text, nb.Text)
		if ob.Loc == nb.Loc {
			s += 0.5
		}
		if s < minPairScore {
			return 0
		}
		return s
	}

	var out []pair
	if n*m > gapLimit {
		// Too large to score every pair: pair by position within each kind.
		i, j := 0, 0
		for i < n || j < m {
			switch {
			case i < n && j < m && orig[o0+i].Kind == cur[n0+j].Kind:
				out = append(out, pair{o0 + i, n0 + j})
				i++
				j++
			case i < n && (j >= m || orig[o0+i].Kind != cur[n0+j].Kind):
				out = append(out, pair{o0 + i, -1})
				i++
			default:
				out = append(out, pair{-1, n0 + j})
				j++
			}
		}
		return out
	}

	w := m + 1
	t := make([]float64, (n+1)*w)
	sc := make([]float64, n*m)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			s := score(i, j)
			sc[i*m+j] = s
			best := t[(i+1)*w+j]
			if v := t[i*w+j+1]; v > best {
				best = v
			}
			if s > 0 {
				if v := t[(i+1)*w+j+1] + s; v > best {
					best = v
				}
			}
			t[i*w+j] = best
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && sc[i*m+j] > 0 && t[i*w+j] == t[(i+1)*w+j+1]+sc[i*m+j]:
			out = append(out, pair{o0 + i, n0 + j})
			i++
			j++
		case i < n && (j >= m || t[i*w+j] == t[(i+1)*w+j]):
			out = append(out, pair{o0 + i, -1})
			i++
		default:
			out = append(out, pair{-1, n0 + j})
			j++
		}
	}
	return out
}

// similarity is the Dice coefficient of the rune bigrams of a and b, 0 to 1.
func similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		if a == b {
			return 1
		}
		return 0
	}
	count := map[[2]rune]int{}
	for i := 0; i+1 < len(ra); i++ {
		count[[2]rune{ra[i], ra[i+1]}]++
	}
	common := 0
	for i := 0; i+1 < len(rb); i++ {
		k := [2]rune{rb[i], rb[i+1]}
		if count[k] > 0 {
			count[k]--
			common++
		}
	}
	return 2 * float64(common) / float64(len(ra)+len(rb)-2)
}
