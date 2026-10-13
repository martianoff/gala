package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuild_SealedCaseFieldDefaultsAcrossPackages builds a program whose
// sealed type, with field defaults, is declared in another package: the
// importer constructs it leaving fields out, by position and by name.
func TestBuild_SealedCaseFieldDefaultsAcrossPackages(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/casedefaults", map[string]string{
		"lights/lights.gala": `package lights

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}
`,
		"main.gala": `package main

import "example.com/casedefaults/lights"

func describe(l lights.Light) string = l match {
    case lights.On(p, r) => s"on $p in $r"
    case lights.Off()    => "off"
}

func main() {
    Println(describe(lights.On()), describe(lights.On(Room = "Hall")), describe(lights.On(5)))
}
`,
	})
	assert.Equal(t, "on|30|in|Living|on|30|in|Hall|on|5|in|Living", buildAndRun(t, projectDir))
}
