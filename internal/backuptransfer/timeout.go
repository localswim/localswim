// Package backuptransfer shares HTTP time budgets for streamed native ZIP recovery.
package backuptransfer

import "time"

const BodyIdle = 30 * time.Second

// UploadBudget bounds one body transfer. Unknown lengths get the base window.
func UploadBudget(size int64) time.Duration {
	return sizeBudget(size, 8<<20, BodyIdle, 2*time.Hour)
}

// ReplyBudget leaves time for archive validation, repair, and the HTTP response.
func ReplyBudget(size int64) time.Duration {
	return sizeBudget(size, 1<<20, time.Minute, 6*time.Hour)
}

func ClientBudget(size int64) time.Duration {
	return UploadBudget(size) + ReplyBudget(size) + 5*time.Second
}

func sizeBudget(size, bytesPerSecond int64, base, ceiling time.Duration) time.Duration {
	seconds := min(max(size, 0)/bytesPerSecond, int64((ceiling-base)/time.Second))
	return base + time.Duration(seconds)*time.Second
}
