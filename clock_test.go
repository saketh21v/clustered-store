package distcluststore

import (
	"testing"
)

func Test_CompareClocks(t *testing.T) {
	c1 := HybridVecClock{
		Vec: []uint64{2, 0, 0},
	}
	c2 := HybridVecClock{
		Vec: []uint64{2, 0, 1},
	}
	if c1.compare(c2) != CmpGt {
		t.Errorf("Error: Expected c2 > c1")
	}
}
