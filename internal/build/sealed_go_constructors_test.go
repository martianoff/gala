package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuild_SealedGoConstructorsFromHandWrittenGo builds a package whose
// hand-written Go builds its sealed type's values with the generated
// constructors, which apply the GALA field defaults a composite literal skips.
func TestBuild_SealedGoConstructorsFromHandWrittenGo(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/goctors", map[string]string{
		"lights/lights.gala": `package lights

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}
`,
		"lights/describe.go": `package lights

func Describe() string {
	return NewLight().String() + " " + NewLightOn(WithLightOnPower(5)).String() + " " + NewLightOff().String()
}
`,
		"main.gala": `package main

import "example.com/goctors/lights"

func main() {
    Println(lights.Describe())
}
`,
	})
	assert.Equal(t, "On(30,|Living)|On(5,|Living)|Off()", buildAndRun(t, projectDir))
}
