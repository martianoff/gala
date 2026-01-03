package parser

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestEmptyLineRequirement(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "No empty line after package",
			input: `package main
val x = 10`,
			wantErr: true,
		},
		{
			name: "Empty line after package",
			input: `package main

val x = 10`,
			wantErr: false,
		},
		{
			name: "No empty line after import",
			input: `package main

import "fmt"
val x = 10`,
			wantErr: true,
		},
		{
			name: "Empty line after import",
			input: `package main

import "fmt"

val x = 10`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Parse(tt.input)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for input: %s", tt.input)
			} else {
				assert.NoError(t, err, "Unexpected error for input: %s", tt.input)
			}
		})
	}
}
