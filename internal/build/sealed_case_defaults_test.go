package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuild_SealedCaseFieldDefaultsAcrossPackages builds a program whose
// sealed type, with field defaults on its default case, is declared in
// another package: the importer constructs it leaving fields out, and reads a
// zero value of it (an omitted struct field) as that case built with them.
func TestBuild_SealedCaseFieldDefaultsAcrossPackages(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/casedefaults", map[string]string{
		"lights/lights.gala": `package lights

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}

struct Home(Name string, L Light)
`,
		"main.gala": `package main

import "example.com/casedefaults/lights"

func describe(l lights.Light) string = l match {
    case lights.On(p, r) => s"on $p in $r"
    case lights.Off()    => "off"
}

func main() {
    val home = lights.Home{Name: "flat"}
    Println(describe(home.L), home.L == lights.On(), describe(lights.On(Room = "Hall")))
}
`,
	})
	assert.Equal(t, "on|30|in|Living|true|on|30|in|Hall", buildAndRun(t, projectDir))
}
