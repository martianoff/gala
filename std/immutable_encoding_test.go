package std

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lineItem is what the transpiler emits for `struct LineItem(SKU string, Qty int)`.
type lineItem struct {
	SKU Immutable[string] `json:"sku"`
	Qty Immutable[int]
}

// cents has a pointer-receiver MarshalJSON, which a plain Go field of an
// addressable struct would use.
type cents int64

func (c *cents) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%d.%02d"`, *c/100, *c%100)), nil
}

// The YAML Marshaler and v2-style Unmarshaler interfaces, which yaml.v2 and
// yaml.v3 both check for.
type yamlMarshaler interface {
	MarshalYAML() (any, error)
}

type yamlUnmarshaler interface {
	UnmarshalYAML(unmarshal func(any) error) error
}

var (
	_ yamlMarshaler   = Immutable[int]{}
	_ yamlUnmarshaler = (*Immutable[int])(nil)
)

func TestImmutableJSON(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"scalar", NewImmutable(42), `42`},
		{"string", NewImmutable("a-b"), `"a-b"`},
		{"struct fields with tag", lineItem{SKU: NewImmutable("A-1"), Qty: NewImmutable(3)}, `{"sku":"A-1","Qty":3}`},
		{"nil pointer", NewImmutable[*int](nil), `null`},
		{"nested", NewImmutable(NewImmutable([]string{"x"})), `["x"]`},
		{"pointer-receiver marshaler", NewImmutable(cents(1250)), `"12.50"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

// An immutable field escapes HTML characters exactly as a plain field does,
// whichever escaping the encoder is set to.
func TestImmutableJSONEscapeHTML(t *testing.T) {
	type plain struct{ S string }
	type wrapped struct{ S Immutable[string] }
	const text = "<a & b>"
	encode := func(v any, escapeHTML bool) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(escapeHTML)
		require.NoError(t, enc.Encode(v))
		return buf.String()
	}
	for _, escapeHTML := range []bool{true, false} {
		assert.Equal(t, encode(plain{text}, escapeHTML), encode(wrapped{NewImmutable(text)}, escapeHTML), "escapeHTML=%v", escapeHTML)
	}
}

func TestImmutableJSONRoundTrip(t *testing.T) {
	var item lineItem
	require.NoError(t, json.Unmarshal([]byte(`{"sku":"B-2","Qty":7}`), &item))
	assert.Equal(t, "B-2", item.SKU.Get())
	assert.Equal(t, 7, item.Qty.Get())

	var bad lineItem
	assert.Error(t, json.Unmarshal([]byte(`{"Qty":"seven"}`), &bad))
}

func TestImmutableYAMLConvention(t *testing.T) {
	v, err := NewImmutable(5).MarshalYAML()
	require.NoError(t, err)
	assert.Equal(t, 5, v)

	var i Immutable[string]
	require.NoError(t, i.UnmarshalYAML(func(target any) error {
		*target.(*string) = "decoded"
		return nil
	}))
	assert.Equal(t, "decoded", i.Get())
}

func TestImmutableFormat(t *testing.T) {
	item := lineItem{SKU: NewImmutable("A-1"), Qty: NewImmutable(3)}
	tests := []struct {
		format string
		args   []any
		want   string
	}{
		{"%v", []any{item}, "{A-1 3}"},
		{"%+v", []any{item}, "{SKU:A-1 Qty:3}"},
		{"%q", []any{NewImmutable("x")}, `"x"`},
		{"%5d|%-4d|", []any{NewImmutable(42), NewImmutable(7)}, "   42|7   |"},
		{"%.2f", []any{NewImmutable(3.14159)}, "3.14"},
		{"%v", []any{NewImmutable[error](fmt.Errorf("boom"))}, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			assert.Equal(t, tt.want, fmt.Sprintf(tt.format, tt.args...))
		})
	}
}
