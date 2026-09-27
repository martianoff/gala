package parser

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestParseExpressionAt pins the contract the transpiler relies on when it
// re-parses expression text held apart from its file (interpolations, declared
// defaults): tokens carry the seeded absolute position, and text the
// expression rule does not consume is an error rather than silently dropped.
func TestParseExpressionAt(t *testing.T) {
	t.Run("tokens carry the seeded position", func(t *testing.T) {
		expr, err := ParseExpressionAt("(a int) =>\n    a + 1", 12, 30, "default value")
		require.NoError(t, err)
		assert.Equal(t, 12, expr.GetStart().GetLine())
		assert.Equal(t, 30, expr.GetStart().GetColumn())
		// A token on a following line starts at that line's own column.
		assert.Equal(t, 13, expr.GetStop().GetLine())
		assert.Equal(t, 8, expr.GetStop().GetColumn())
	})

	t.Run("no position parses at 1:0", func(t *testing.T) {
		expr, err := ParseExpressionAt("x + 1", 0, 0, "default value")
		require.NoError(t, err)
		assert.Equal(t, 1, expr.GetStart().GetLine())
		assert.Equal(t, 0, expr.GetStart().GetColumn())
	})

	t.Run("unconsumed text is an error", func(t *testing.T) {
		_, err := ParseExpressionAt("x y", 4, 2, "default value")
		require.Error(t, err)
		var se *galaerr.SyntaxError
		require.True(t, errors.As(err, &se), "want a SyntaxError, got %T: %v", err, err)
		assert.Contains(t, se.Error(), `unexpected "y" in default value`)
		assert.Equal(t, 4, se.Line)
		assert.Equal(t, 4, se.Column)
	})

	t.Run("concurrent parses", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := ParseExpressionAt("(a, b) => if (a > b) a else b", 3, 7, "default value")
				assert.NoError(t, err)
			}()
		}
		wg.Wait()
	})
}
