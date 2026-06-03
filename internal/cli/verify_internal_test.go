package cli

import (
	"strings"
	"testing"
)

// TestRingBuffer covers the bounded streaming sink used by runVerify to cap
// captured output. The contract: only the LAST `cap` bytes are retained, and
// once the ring has wrapped a "... (truncated)" prefix appears in String().
func TestRingBuffer(t *testing.T) {
	t.Run("small writes within capacity", func(t *testing.T) {
		r := newRingBuffer(16)
		r.Write([]byte("hello "))
		r.Write([]byte("world"))
		got := r.String()
		if got != "hello world" {
			t.Errorf("got %q, want %q", got, "hello world")
		}
	})

	t.Run("single write exceeding capacity keeps only tail", func(t *testing.T) {
		r := newRingBuffer(8)
		r.Write([]byte("0123456789ABCDEF"))
		got := r.String()
		if !strings.HasPrefix(got, "... (truncated)\n") {
			t.Errorf("expected truncation prefix, got %q", got)
		}
		if !strings.HasSuffix(got, "89ABCDEF") {
			t.Errorf("expected last 8 chars, got %q", got)
		}
	})

	t.Run("many small writes that overflow", func(t *testing.T) {
		r := newRingBuffer(10)
		for i := 0; i < 100; i++ {
			r.Write([]byte("xxxx"))
		}
		got := r.String()
		if !strings.HasPrefix(got, "... (truncated)\n") {
			t.Errorf("expected truncation prefix on overflow, got %q", got)
		}
		// Last 10 bytes should all be 'x'
		body := strings.TrimPrefix(got, "... (truncated)\n")
		if len(body) != 10 {
			t.Errorf("body length = %d, want 10; got %q", len(body), body)
		}
	})

	t.Run("zero capacity is a no-op sink", func(t *testing.T) {
		r := newRingBuffer(0)
		n, _ := r.Write([]byte("anything"))
		if n != len("anything") {
			t.Errorf("zero-cap Write returned n=%d, want %d", n, len("anything"))
		}
		if r.String() != "" {
			t.Errorf("zero-cap String = %q, want empty", r.String())
		}
	})

	t.Run("exact capacity boundary", func(t *testing.T) {
		r := newRingBuffer(5)
		r.Write([]byte("12345"))
		got := r.String()
		// At exact-fill, head wraps to 0 and filled becomes true, so the
		// truncated prefix appears. The body should still be the correct 5 bytes.
		if !strings.HasSuffix(got, "12345") {
			t.Errorf("expected suffix '12345', got %q", got)
		}
	})

	t.Run("growing across the boundary", func(t *testing.T) {
		r := newRingBuffer(6)
		r.Write([]byte("abcd"))      // head=4, not filled
		r.Write([]byte("ef"))        // head=6 wraps to 0, filled=true
		body := strings.TrimPrefix(r.String(), "... (truncated)\n")
		if body != "abcdef" {
			t.Errorf("body = %q, want 'abcdef'", body)
		}
		r.Write([]byte("ghij")) // overwrite some
		body2 := strings.TrimPrefix(r.String(), "... (truncated)\n")
		if body2 != "efghij" {
			t.Errorf("body after overflow = %q, want 'efghij'", body2)
		}
	})
}
