package lsp_test

import (
	"strings"
	"testing"
)

// A method declared in another file than its type: hover on its declaration,
// in the file being edited, shows its documentation. The analysis of that file
// collects the method before it reads the type's file, and has to record the
// method's own file for the hover to find the declaration.
func TestHover_MethodDeclaredInAnotherFileThanItsType(t *testing.T) {
	h := newHarness(t)
	dir := createTestProject(t, []testProjectFile{
		{Name: "widget.gala", Src: "package main\n\nstruct Widget(N int)\n"},
		{Name: "fluent.gala", Src: "package main\n\n// AsFixed pins the widget.\nfunc (w Widget) AsFixed() int = w.N\n\nfunc main() {\n    Println(Widget(1).AsFixed())\n}\n"},
	})
	uri := openProjectFile(t, h, dir, "fluent.gala")

	// Line 3 declares AsFixed, at characters 16-22.
	hover, err := h.Hover(uri, 3, 18)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil || !strings.Contains(hover.Contents.Value(), "AsFixed pins the widget.") {
		t.Errorf("hover at the declaration of AsFixed = %v, want its doc comment", hover)
	}
}
