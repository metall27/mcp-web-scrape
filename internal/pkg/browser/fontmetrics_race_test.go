package browser

import "testing"

// Concurrency smoke for FontMockSupportedPlatforms (review #113 #1):
// the reference cache is read from concurrent scrape goroutines.
func TestFontMockPlatformsConcurrent(t *testing.T) {
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				if p := FontMockSupportedPlatforms(); p == nil {
					t.Error("nil platforms")
					return
				}
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
}
