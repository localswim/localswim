package backuptransfer

import (
	"math"
	"testing"
	"time"
)

func TestM86ZIPTimeBudgetsAreFiniteAndSizeAware(t *testing.T) {
	for _, test := range []struct {
		size          int64
		upload, reply time.Duration
	}{
		{-1, 30 * time.Second, time.Minute},
		{0, 30 * time.Second, time.Minute},
		{96 << 20, 42 * time.Second, 156 * time.Second},
		{math.MaxInt64, 2 * time.Hour, 6 * time.Hour},
	} {
		if upload, reply := UploadBudget(test.size), ReplyBudget(test.size); upload != test.upload || reply != test.reply {
			t.Errorf("size=%d upload=%v reply=%v", test.size, upload, reply)
		}
		if ClientBudget(test.size) != test.upload+test.reply+5*time.Second {
			t.Errorf("client did not cover both server phases at size %d", test.size)
		}
	}
}
