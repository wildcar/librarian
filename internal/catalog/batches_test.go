package catalog

import "testing"

func TestBatchPercent(t *testing.T) {
	for _, c := range []struct {
		p    BatchProgress
		want int
	}{
		{BatchProgress{Originals: 14}, 0},
		{BatchProgress{Originals: 14, Read: 7}, 33},                                  // 7 of 14 read, none split
		{BatchProgress{Originals: 1, Unsupported: 1}, 100},                           // nothing readable: done
		{BatchProgress{Originals: 2, Read: 2, Split: 2, Documents: 4, Ready: 3}, 87}, // one document left
		{BatchProgress{Originals: 22, Read: 16, Unsupported: 6, Split: 16, Documents: 32, Ready: 32}, 100},
	} {
		if got := c.p.Percent(); got != c.want {
			t.Errorf("%+v: %d%%, want %d%%", c.p, got, c.want)
		}
	}
}
