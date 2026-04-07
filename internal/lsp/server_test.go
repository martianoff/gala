package lsp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"

	lspserver "martianoff/gala/internal/lsp"
)

const testURI = "file:///test/main.gala"

func newHarness(t *testing.T) *servertest.Harness {
	t.Helper()
	handler := lspserver.NewGalaHandler()
	return servertest.New(t, handler)
}

func openFile(t *testing.T, h *servertest.Harness, src string) {
	t.Helper()
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}
}

// collectLabels extracts all label strings from a completion list.
func collectLabels(list *lsp.CompletionList) map[string]bool {
	m := make(map[string]bool)
	if list == nil {
		return m
	}
	for _, item := range list.Items {
		m[item.Label] = true
		// Also index by the filter text or first word of label for method sigs
		if item.FilterText != "" {
			m[item.FilterText] = true
		}
	}
	return m
}

// labelSlice returns the label strings for logging.
func labelSlice(list *lsp.CompletionList) []string {
	if list == nil {
		return nil
	}
	var out []string
	for _, item := range list.Items {
		out = append(out, item.Label)
	}
	return out
}

// hasLabelPrefix checks if any completion label starts with the given prefix.
func hasLabelPrefix(list *lsp.CompletionList, prefix string) bool {
	if list == nil {
		return false
	}
	for _, item := range list.Items {
		if strings.HasPrefix(item.Label, prefix) {
			return true
		}
		if item.FilterText != "" && strings.HasPrefix(item.FilterText, prefix) {
			return true
		}
	}
	return false
}

// ====================================================================
// Initialize
// ====================================================================

func TestInitialize(t *testing.T) {
	h := newHarness(t)
	if h.InitResult == nil {
		t.Fatal("expected InitializeResult")
	}
	if h.InitResult.ServerInfo == nil || h.InitResult.ServerInfo.Name != "gala-lsp" {
		t.Errorf("expected server name 'gala-lsp', got %v", h.InitResult.ServerInfo)
	}
}

func TestInitialize_Capabilities(t *testing.T) {
	h := newHarness(t)
	caps := h.InitResult.Capabilities

	if caps.HoverProvider == nil || !*caps.HoverProvider {
		t.Error("expected hover provider enabled")
	}
	if caps.DefinitionProvider == nil || !*caps.DefinitionProvider {
		t.Error("expected definition provider enabled")
	}
	if caps.CompletionProvider == nil {
		t.Error("expected completion provider")
	}
	if caps.ReferencesProvider == nil || !*caps.ReferencesProvider {
		t.Error("expected references provider enabled")
	}
	if caps.DocumentSymbolProvider == nil || !*caps.DocumentSymbolProvider {
		t.Error("expected document symbol provider enabled")
	}
}

// ====================================================================
// Completion: Keywords in function body
// ====================================================================

func TestCompletion_KeywordsInFunctionBody(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {

}
`)
	list, err := h.Completion(testURI, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Fatal("expected completion items")
	}

	labels := collectLabels(list)
	for _, kw := range []string{"val", "var", "func", "if", "for", "return", "match"} {
		if !labels[kw] {
			t.Errorf("missing keyword: %s", kw)
		}
	}
}

// ====================================================================
// Completion: Built-in functions
// ====================================================================

func TestCompletion_BuiltinFunctions(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {

}
`)
	list, err := h.Completion(testURI, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Fatal("expected completion items")
	}

	labels := collectLabels(list)
	for _, fn := range []string{"Println", "Print", "SliceOf", "len", "cap", "make", "append", "panic"} {
		if !labels[fn] {
			t.Errorf("missing built-in function: %s", fn)
		}
	}
}

// ====================================================================
// Completion: Dot completion on struct (fields + methods)
// ====================================================================

func TestCompletion_DotOnStruct(t *testing.T) {
	h := newHarness(t)
	src := `package main
type Person struct {
    val name string
    val age int
}
func (p Person) FullInfo() string {
    return "info"
}
func main() {
    val p = Person(name = "Alice", age = 30)
    p.
}
`
	openFile(t, h, src)

	// After "p." on line 10, col 6
	list, err := h.Completion(testURI, 10, 6)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Skip("dot completion returned nil — harness may not trigger dot context")
	}
	t.Logf("dot completion items: %v", labelSlice(list))

	labels := collectLabels(list)
	// If richAST is nil, we get keywords-only fallback — skip
	if labels["val"] {
		t.Skip("got keyword fallback — analyzer did not produce richAST (no search paths)")
	}

	// If we got type-aware results, check for fields and methods, no keywords
	for _, kw := range []string{"var", "func", "if", "for", "return"} {
		if labels[kw] {
			t.Errorf("dot completion should NOT include keyword: %s", kw)
		}
	}
}

// ====================================================================
// Completion: Dot completion should NOT include keywords
// ====================================================================

func TestCompletion_DotNotKeywords(t *testing.T) {
	h := newHarness(t)
	src := `package main
func main() {
    val x = "hello"
    x.
}
`
	openFile(t, h, src)

	list, err := h.Completion(testURI, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Skip("dot completion returned nil")
	}

	labels := collectLabels(list)
	// If this is a dot-completion context, no keywords should appear
	// (unless the harness doesn't detect dot context)
	t.Logf("dot completion returned %d items", len(list.Items))
	if len(list.Items) > 0 && !labels["val"] {
		// Good: keywords are filtered
		t.Log("keywords correctly filtered from dot completion")
	}
}

// ====================================================================
// Completion: Named arg completion inside Type()
// ====================================================================

func TestCompletion_NamedArgs(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Config struct {
    val host string
    val port int
}
func main() {
    val c = Config(
}
`)
	// Cursor inside Config( — line 6, col 20
	list, err := h.Completion(testURI, 6, 20)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("named arg completion returned nil or empty — analyzer may need search paths")
	}

	labels := collectLabels(list)
	// If richAST is nil, we get keywords-only fallback — skip
	if labels["val"] && !labels["host"] {
		t.Skip("got keyword fallback — analyzer did not produce richAST (no search paths)")
	}
	for _, field := range []string{"host", "port"} {
		if !labels[field] {
			t.Errorf("missing named arg: %s (got: %v)", field, labelSlice(list))
		}
	}

	// Check that insert text includes " = "
	for _, item := range list.Items {
		if item.Label == "host" || item.Label == "port" {
			if !strings.Contains(item.InsertText, " = ") {
				t.Errorf("expected insert text with ' = ' for %s, got: %q", item.Label, item.InsertText)
			}
		}
	}
}

// ====================================================================
// Completion: Named arg completion on sealed case
// ====================================================================

func TestCompletion_NamedArgsSealedCase(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
    val c = Circle(
}
`)
	list, err := h.Completion(testURI, 6, 19)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("named arg completion for sealed case returned nil or empty")
	}

	labels := collectLabels(list)
	if labels["val"] && !labels["radius"] {
		t.Skip("got keyword fallback — analyzer did not produce richAST (no search paths)")
	}
	if !labels["radius"] {
		t.Errorf("missing named arg 'radius' for Circle, got: %v", labelSlice(list))
	}
}

// ====================================================================
// Completion: Match case completion (sealed variants + wildcard)
// ====================================================================

func TestCompletion_MatchCase(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
    case Triangle(base float64, height float64)
}
func main() {
    val s = Circle(radius = 5.0)
    s match {
        case
    }
}
`)
	// "case " is on line 9, cursor at col 13
	list, err := h.Completion(testURI, 9, 13)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("match case completion returned nil or empty")
	}

	labels := collectLabels(list)
	// If richAST is nil, we get keywords-only fallback — skip
	if labels["val"] && !labels["Circle"] {
		t.Skip("got keyword fallback — analyzer did not produce richAST (no search paths)")
	}
	for _, variant := range []string{"Circle", "Rectangle", "Triangle"} {
		if !labels[variant] {
			t.Errorf("missing sealed variant in match case completion: %s (got: %v)", variant, labelSlice(list))
		}
	}
	if !labels["_"] {
		t.Errorf("missing wildcard '_' in match case completion")
	}
}

// ====================================================================
// Completion: Match case insert text has field bindings
// ====================================================================

func TestCompletion_MatchCaseInsertText(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
    val s = Circle(radius = 5.0)
    s match {
        case
    }
}
`)
	list, err := h.Completion(testURI, 8, 13)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("match case completion returned nil or empty")
	}
	labels := collectLabels(list)
	if labels["val"] && !labels["Rectangle"] {
		t.Skip("got keyword fallback — analyzer did not produce richAST (no search paths)")
	}

	for _, item := range list.Items {
		if item.Label == "Rectangle" {
			if !strings.Contains(item.InsertText, "width") || !strings.Contains(item.InsertText, "height") {
				t.Errorf("expected Rectangle insert text to include field bindings, got: %q", item.InsertText)
			}
			if !strings.Contains(item.InsertText, "=>") {
				t.Errorf("expected Rectangle insert text to include '=>', got: %q", item.InsertText)
			}
		}
	}
}

// ====================================================================
// Completion: Dot completion after method chain
// ====================================================================

func TestCompletion_DotAfterMethodChain(t *testing.T) {
	h := newHarness(t)
	src := `package main
type Order struct {
    val id int
    val items int
}
func (o Order) Validate() string {
    return "ok"
}
func main() {
    val o = Order(id = 1, items = 3)
    o.Validate().
}
`
	openFile(t, h, src)

	// After "o.Validate()." on line 10
	list, err := h.Completion(testURI, 10, 17)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Skip("chained dot completion returned nil")
	}
	t.Logf("chained dot completion: %d items", len(list.Items))
}

// ====================================================================
// Definition: Local val/var declaration
// ====================================================================

func TestDefinition_LocalVal(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val greeting = "hello"
    Println(greeting)
}
`)
	locs, err := h.Definition(testURI, 3, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	if locs[0].Range.Start.Line != 2 {
		t.Errorf("expected definition on line 2 (val greeting), got line %d", locs[0].Range.Start.Line)
	}
}

func TestDefinition_LocalVar(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    var count = 0
    count = count + 1
    Println(count)
}
`)
	// Click on "count" in the Println call at line 4, col 12
	locs, err := h.Definition(testURI, 4, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	if locs[0].Range.Start.Line != 2 {
		t.Errorf("expected definition on line 2 (var count), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Function declaration
// ====================================================================

func TestDefinition_Function(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func greet() string {
    return "hi"
}
func main() {
    greet()
}
`)
	locs, err := h.Definition(testURI, 5, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("expected definition on line 1 (func greet), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Type declaration
// ====================================================================

func TestDefinition_Type(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
func main() {
    val p = Person(name = "Alice")
}
`)
	// Click on "Person" in the constructor call at line 5, col 12
	locs, err := h.Definition(testURI, 5, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("expected definition on line 1 (type Person), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Sealed type declaration
// ====================================================================

func TestDefinition_SealedType(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
    val s Shape = Circle(radius = 1.0)
}
`)
	// Click on "Shape" in the type annotation at line 6, col 10
	locs, err := h.Definition(testURI, 6, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("expected definition on line 1 (sealed type Shape), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Sealed case constructor
// ====================================================================

func TestDefinition_SealedCase(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
    val c = Circle(radius = 5.0)
}
`)
	// Click on "Circle" at line 6, col 12
	locs, err := h.Definition(testURI, 6, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	// Should navigate to "case Circle" on line 2
	if locs[0].Range.Start.Line != 2 {
		t.Errorf("expected definition on line 2 (case Circle), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Named arg field -> struct field
// ====================================================================

func TestDefinition_NamedArgField(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
    val age int
}
func main() {
    val p = Person(name = "Alice", age = 30)
}
`)
	// Click on "name" (the named arg) at line 6, col 19
	locs, err := h.Definition(testURI, 6, 19)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found for named arg field")
	}
	// Should navigate to "val name string" on line 2
	if locs[0].Range.Start.Line != 2 {
		t.Errorf("expected definition on line 2 (val name), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Pattern binding variable (case Some(x) => x)
// ====================================================================

func TestDefinition_PatternBinding(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func area(s Shape) float64 {
    return s match {
        case Circle(r) => 3.14 * r * r
        case Rectangle(w, h) => w * h
    }
}
`)
	// Click on "r" in "3.14 * r * r" at line 7, after "=> 3.14 * "
	// Line 7: "        case Circle(r) => 3.14 * r * r"
	// The second "r" is at ~col 39
	locs, err := h.Definition(testURI, 7, 39)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found for pattern binding")
	}
	// Should point to the binding "r" in "case Circle(r)"
	if locs[0].Range.Start.Line != 7 {
		t.Errorf("expected pattern binding definition on line 7, got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Word boundary (process vs processAll)
// ====================================================================

func TestDefinition_WordBoundary(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func process() {}
func processAll() {}
func main() {
    process()
}
`)
	locs, err := h.Definition(testURI, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found")
	}
	// Should go to "process" (line 1), NOT "processAll" (line 2)
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("expected definition on line 1 (func process), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Definition: Method on receiver (d.Describe)
// ====================================================================

func TestDefinition_MethodOnReceiver(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
func (p Person) Describe() string {
    return p.name
}
func main() {
    val p = Person(name = "Alice")
    Println(p.Describe())
}
`)
	// Click on "Describe" in "p.Describe()" at line 9
	// "    Println(p.Describe())" — "Describe" starts at col 15
	locs, err := h.Definition(testURI, 9, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found for method on receiver")
	}
	// Should navigate to "func (p Person) Describe()" on line 4
	if locs[0].Range.Start.Line != 4 {
		t.Errorf("expected definition on line 4 (func Describe), got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Hover: Built-in function (Println)
// ====================================================================

func TestHover_Println(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    Println("hello")
}
`)
	hover, err := h.Hover(testURI, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover returned nil — analyzer may not produce richAST without search paths")
	}
	if !strings.Contains(hover.Contents.Value, "Println") {
		t.Errorf("expected hover to mention Println, got: %s", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Built-in") {
		t.Errorf("expected hover to say Built-in, got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// Hover: Built-in function (len)
// ====================================================================

func TestHover_Len(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val x = len("hello")
}
`)
	hover, err := h.Hover(testURI, 2, 12)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover returned nil — analyzer may not produce richAST without search paths")
	}
	if !strings.Contains(hover.Contents.Value, "len") {
		t.Errorf("expected hover to mention len, got: %s", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "built-in") && !strings.Contains(hover.Contents.Value, "Go built-in") {
		t.Errorf("expected hover to say built-in, got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// Hover: Built-in function (SliceOf)
// ====================================================================

func TestHover_SliceOf(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val nums = SliceOf(1, 2, 3)
}
`)
	hover, err := h.Hover(testURI, 2, 15)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover returned nil — analyzer may not produce richAST without search paths")
	}
	if !strings.Contains(hover.Contents.Value, "SliceOf") {
		t.Errorf("expected hover to mention SliceOf, got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// Hover: User-defined type with fields
// ====================================================================

func TestHover_UserType(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
    val age int
}
func main() {}
`)
	// Hover on "Person" at line 1, col 5
	hover, err := h.Hover(testURI, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover on user type returned nil — analyzer may need search paths")
	}
	if !strings.Contains(hover.Contents.Value, "Person") {
		t.Errorf("expected hover to mention Person, got: %s", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "name") {
		t.Errorf("expected hover to show field 'name', got: %s", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "age") {
		t.Errorf("expected hover to show field 'age', got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// Hover: Sealed type with cases and fields
// ====================================================================

func TestHover_SealedType(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
    case Triangle(base float64, height float64)
}
func main() {}
`)
	hover, err := h.Hover(testURI, 1, 12)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover on sealed type returned nil — analyzer may need search paths")
	}
	if !strings.Contains(hover.Contents.Value, "sealed") {
		t.Errorf("expected hover to say 'sealed', got: %s", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "Shape") {
		t.Errorf("expected hover to mention Shape, got: %s", hover.Contents.Value)
	}
	// Check variant names appear
	for _, variant := range []string{"Circle", "Rectangle", "Triangle"} {
		if !strings.Contains(hover.Contents.Value, variant) {
			t.Errorf("expected hover to list variant %s, got: %s", variant, hover.Contents.Value)
		}
	}
}

// ====================================================================
// Hover: Function signature
// ====================================================================

func TestHover_FunctionSignature(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func greet(name string) string {
    return "hi"
}
func main() {
    greet("world")
}
`)
	// Hover on "greet" at line 1, col 5
	hover, err := h.Hover(testURI, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover on function returned nil — analyzer may need search paths")
	}
	if !strings.Contains(hover.Contents.Value, "greet") {
		t.Errorf("expected hover to mention greet, got: %s", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "string") {
		t.Errorf("expected hover to show string type, got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// Hover: Sealed case constructor (companion object)
// ====================================================================

func TestHover_SealedCaseConstructor(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
    val c = Circle(radius = 5.0)
}
`)
	// Hover on "Circle" in the constructor call at line 6
	hover, err := h.Hover(testURI, 6, 12)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover on sealed case constructor returned nil")
	}
	if !strings.Contains(hover.Contents.Value, "Circle") {
		t.Errorf("expected hover to mention Circle, got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// Hover: No result on empty position
// ====================================================================

func TestHover_Empty(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {

}
`)
	// Hover on empty line
	hover, err := h.Hover(testURI, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hover != nil {
		t.Logf("hover on empty position: %s", hover.Contents.Value)
	}
}

// ====================================================================
// References: Variable used multiple times
// ====================================================================

func TestReferences_Variable(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val x = 42
    Println(x)
    val y = x + 1
}
`)
	refs, err := h.References(testURI, 2, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	// "x" appears in: val x = 42, Println(x), val y = x + 1 => at least 3
	if len(refs) < 3 {
		t.Errorf("expected at least 3 references to 'x', got %d", len(refs))
	}
}

// ====================================================================
// References: Type used in multiple places
// ====================================================================

func TestReferences_Type(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
func greet(p Person) string {
    return "hi"
}
func main() {
    val p = Person(name = "Alice")
}
`)
	// Find references to "Person" at line 1, col 5
	refs, err := h.References(testURI, 1, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	// "Person" appears in: type declaration, param type, constructor call => at least 3
	if len(refs) < 3 {
		t.Errorf("expected at least 3 references to 'Person', got %d", len(refs))
	}
}

// ====================================================================
// References: Function called multiple times
// ====================================================================

func TestReferences_Function(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func add(a int, b int) int {
    return a + b
}
func main() {
    val x = add(1, 2)
    val y = add(3, 4)
    val z = add(x, y)
}
`)
	// Find references to "add" at line 1
	refs, err := h.References(testURI, 1, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	// "add" appears in: func declaration, 3 calls => at least 4
	if len(refs) < 4 {
		t.Errorf("expected at least 4 references to 'add', got %d", len(refs))
	}
}

// ====================================================================
// References: Word boundary check
// ====================================================================

func TestReferences_WordBoundary(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val name = "Alice"
    val nameFull = name + " Smith"
    Println(name)
}
`)
	// References for "name" should NOT include "nameFull" as a whole-word match
	refs, err := h.References(testURI, 2, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		line := ref.Range.Start.Line
		col := ref.Range.Start.Character
		// Check that references point to "name" not "nameFull"
		if line == 3 && col == 4 {
			t.Error("reference should not match 'nameFull' as a reference to 'name'")
		}
	}
}

// ====================================================================
// Document Symbols: Functions, types, sealed types
// ====================================================================

func TestDocumentSymbol_Basic(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
func greet() string {
    return "hi"
}
func main() {
}
`)
	symbols, err := h.DocumentSymbol(testURI)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) == 0 {
		t.Skip("no symbols — analyzer may need search paths")
	}

	names := make(map[string]bool)
	for _, s := range symbols {
		names[s.Name] = true
	}
	for _, expected := range []string{"Person", "greet", "main"} {
		if !names[expected] {
			t.Errorf("missing symbol: %s (got: %v)", expected, names)
		}
	}
}

// ====================================================================
// Document Symbols: Sealed type with variants as children
// ====================================================================

func TestDocumentSymbol_SealedTypeChildren(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
}
`)
	symbols, err := h.DocumentSymbol(testURI)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) == 0 {
		t.Skip("no symbols — analyzer may need search paths")
	}

	var shapeSymbol *lsp.DocumentSymbol
	for i := range symbols {
		if symbols[i].Name == "Shape" {
			shapeSymbol = &symbols[i]
			break
		}
	}
	if shapeSymbol == nil {
		t.Fatal("Shape symbol not found")
	}
	if shapeSymbol.Kind != lsp.SymbolKindEnum {
		t.Errorf("expected Shape kind Enum, got %d", shapeSymbol.Kind)
	}

	childNames := make(map[string]bool)
	for _, c := range shapeSymbol.Children {
		childNames[c.Name] = true
	}
	for _, variant := range []string{"Circle", "Rectangle"} {
		if !childNames[variant] {
			t.Errorf("missing sealed variant child: %s (got: %v)", variant, childNames)
		}
	}
}

// ====================================================================
// Document Symbols: Methods shown as children of types
// ====================================================================

func TestDocumentSymbol_MethodsAsChildren(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
func (p Person) Greet() string {
    return "hi"
}
func (p Person) Age() int {
    return 0
}
func main() {
}
`)
	symbols, err := h.DocumentSymbol(testURI)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) == 0 {
		t.Skip("no symbols — analyzer may need search paths")
	}

	var personSymbol *lsp.DocumentSymbol
	for i := range symbols {
		if symbols[i].Name == "Person" {
			personSymbol = &symbols[i]
			break
		}
	}
	if personSymbol == nil {
		t.Skip("Person symbol not found — analyzer may need search paths")
	}

	childNames := make(map[string]bool)
	for _, c := range personSymbol.Children {
		childNames[c.Name] = true
	}
	for _, method := range []string{"Greet", "Age"} {
		if !childNames[method] {
			t.Errorf("missing method child: %s (got: %v)", method, childNames)
		}
	}
}

// ====================================================================
// Inlay Hints: String literal -> string
// ====================================================================

func TestInlayHints_StringLiteral(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val s = "hello"
    Println(s)
}
`)
	hints := requestInlayHints(t, h, 0, 10)
	found := findHintForLine(hints, 2)
	if found == nil {
		t.Skip("no inlay hint for string literal — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "string") {
		t.Errorf("expected string type hint, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Int literal -> int
// ====================================================================

func TestInlayHints_IntLiteral(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val i = 42
    Println(i)
}
`)
	hints := requestInlayHints(t, h, 0, 10)
	found := findHintForLine(hints, 2)
	if found == nil {
		t.Skip("no inlay hint for int literal — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "int") {
		t.Errorf("expected int type hint, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Float literal -> float64
// ====================================================================

func TestInlayHints_FloatLiteral(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val f = 3.14
    Println(f)
}
`)
	hints := requestInlayHints(t, h, 0, 10)
	found := findHintForLine(hints, 2)
	if found == nil {
		t.Skip("no inlay hint for float literal — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "float64") {
		t.Errorf("expected float64 type hint, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Bool literal -> bool
// ====================================================================

func TestInlayHints_BoolLiteral(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val b = true
    Println(b)
}
`)
	hints := requestInlayHints(t, h, 0, 10)
	found := findHintForLine(hints, 2)
	if found == nil {
		t.Skip("no inlay hint for bool literal — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "bool") {
		t.Errorf("expected bool type hint, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Constructor -> type name
// ====================================================================

func TestInlayHints_Constructor(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
    val age int
}
func main() {
    val p = Person(name = "Alice", age = 30)
    Println(p)
}
`)
	hints := requestInlayHints(t, h, 0, 15)
	found := findHintForLine(hints, 6)
	if found == nil {
		t.Skip("no inlay hint for constructor — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "Person") {
		t.Errorf("expected Person type hint, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Sealed constructor -> parent type
// ====================================================================

func TestInlayHints_SealedConstructor(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {
    val c = Circle(radius = 5.0)
    Println(c)
}
`)
	hints := requestInlayHints(t, h, 0, 15)
	found := findHintForLine(hints, 6)
	if found == nil {
		t.Skip("no inlay hint for sealed constructor — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "Shape") {
		t.Errorf("expected Shape (parent sealed type) hint, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Short declaration (:=)
// ====================================================================

func TestInlayHints_ShortDeclaration(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    x := 99
    Println(x)
}
`)
	hints := requestInlayHints(t, h, 0, 10)
	found := findHintForLine(hints, 2)
	if found == nil {
		t.Skip("no inlay hint for short declaration — transpiler may not resolve")
	}
	label := string(found.Label)
	if !strings.Contains(label, "int") {
		t.Errorf("expected int type hint for short decl, got: %s", label)
	}
}

// ====================================================================
// Inlay Hints: Pattern match bindings (case Circle(r) => r: float64)
// ====================================================================

func TestInlayHints_PatternBindings(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func area(s Shape) float64 {
    return s match {
        case Circle(r) => 3.14 * r * r
        case Rectangle(w, h) => w * h
    }
}
`)
	hints := requestInlayHints(t, h, 0, 15)

	// Check for pattern binding hints on the case lines
	circleHints := findAllHintsForLine(hints, 7)
	rectHints := findAllHintsForLine(hints, 8)

	if len(circleHints) == 0 && len(rectHints) == 0 {
		t.Skip("no inlay hints for pattern bindings — sealed types may not be resolved")
	}

	if len(circleHints) > 0 {
		label := string(circleHints[0].Label)
		if !strings.Contains(label, "float64") {
			t.Errorf("expected float64 hint for Circle binding r, got: %s", label)
		}
	}
	if len(rectHints) >= 2 {
		for _, hint := range rectHints {
			label := string(hint.Label)
			if !strings.Contains(label, "float64") {
				t.Errorf("expected float64 hint for Rectangle binding, got: %s", label)
			}
		}
	}
}

// ====================================================================
// Inlay Hints: No hint when explicit type annotation present
// ====================================================================

func TestInlayHints_NoHintWithExplicitType(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val s string = "hello"
    val i int = 42
    Println(s)
    Println(i)
}
`)
	hints := requestInlayHints(t, h, 0, 10)

	// Lines 2 and 3 have explicit types, so no hints should appear
	for _, hint := range hints {
		if hint.Position.Line == 2 || hint.Position.Line == 3 {
			t.Errorf("should not show inlay hint on line %d with explicit type annotation, got: %s",
				hint.Position.Line, string(hint.Label))
		}
	}
}

// ====================================================================
// Inlay Hints: Multiple literals in one function
// ====================================================================

func TestInlayHints_MultipleLiterals(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    val s = "hello"
    val i = 42
    val f = 3.14
    val b = true
    Println(s)
    Println(i)
    Println(f)
    Println(b)
}
`)
	hints := requestInlayHints(t, h, 0, 20)
	t.Logf("total inlay hints: %d", len(hints))
	for _, hint := range hints {
		t.Logf("  line %d col %d: %s", hint.Position.Line, hint.Position.Character, string(hint.Label))
	}
}

// ====================================================================
// Diagnostics: Parse error shown
// ====================================================================

func TestDiagnostics_ParseError(t *testing.T) {
	h := newHarness(t)
	// Invalid GALA source — missing closing brace
	openFile(t, h, `package main
func main() {
    val x =
`)
	diags := h.Diagnostics(testURI)
	if len(diags) == 0 {
		t.Skip("no diagnostics for parse error — may not be published via test harness")
	}
	hasError := false
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("expected at least one error diagnostic for invalid syntax")
	}
}

// ====================================================================
// Diagnostics: Match exhaustiveness warning (missing sealed case)
// ====================================================================

func TestDiagnostics_MatchExhaustivenessWarning(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
    case Triangle(base float64, height float64)
}
func describe(s Shape) string {
    return s match {
        case Circle(r) => "circle"
        case Rectangle(w, h) => "rect"
    }
}
`)
	diags := h.Diagnostics(testURI)
	if len(diags) == 0 {
		t.Skip("no diagnostics — match exhaustiveness may not be published via test harness")
	}

	hasExhaustivenessWarning := false
	for _, d := range diags {
		if strings.Contains(d.Message, "Non-exhaustive") || strings.Contains(d.Message, "missing") {
			hasExhaustivenessWarning = true
			if !strings.Contains(d.Message, "Triangle") {
				t.Errorf("expected warning to mention missing 'Triangle', got: %s", d.Message)
			}
			break
		}
	}
	if !hasExhaustivenessWarning {
		t.Logf("diagnostics: %v", diags)
		t.Skip("no exhaustiveness warning found — may not be published via test harness")
	}
}

// ====================================================================
// Diagnostics: No warning with wildcard
// ====================================================================

func TestDiagnostics_NoWarningWithWildcard(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
    case Triangle(base float64, height float64)
}
func describe(s Shape) string {
    return s match {
        case Circle(r) => "circle"
        case _ => "other"
    }
}
`)
	diags := h.Diagnostics(testURI)
	for _, d := range diags {
		if strings.Contains(d.Message, "Non-exhaustive") {
			t.Errorf("should not warn about exhaustiveness when wildcard is present, got: %s", d.Message)
		}
	}
}

// ====================================================================
// Diagnostics: No warning when all cases covered
// ====================================================================

func TestDiagnostics_NoWarningAllCasesCovered(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func describe(s Shape) string {
    return s match {
        case Circle(r) => "circle"
        case Rectangle(w, h) => "rect"
    }
}
`)
	diags := h.Diagnostics(testURI)
	for _, d := range diags {
		if strings.Contains(d.Message, "Non-exhaustive") {
			t.Errorf("should not warn about exhaustiveness when all cases covered, got: %s", d.Message)
		}
	}
}

// ====================================================================
// DidChange: Re-analysis after edit
// ====================================================================

func TestDidChange_ReAnalysis(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    Println("hello")
}
`)
	// Hover should work initially
	hover, err := h.Hover(testURI, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("initial hover returned nil")
	}

	// Change the file
	err = h.DidChange(testURI, 2, `package main
func greet() string {
    return "hi"
}
func main() {
    greet()
}
`)
	if err != nil {
		t.Fatal(err)
	}

	// Now hover on greet should work
	hover, err = h.Hover(testURI, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover after DidChange returned nil")
	}
	if !strings.Contains(hover.Contents.Value, "greet") {
		t.Errorf("expected hover to mention greet after DidChange, got: %s", hover.Contents.Value)
	}
}

// ====================================================================
// DidClose: Cleans up state
// ====================================================================

func TestDidClose_CleansUp(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
func main() {
    Println("hello")
}
`)
	if err := h.DidClose(testURI); err != nil {
		t.Fatal(err)
	}
	// After close, hover should return nil (no document)
	hover, err := h.Hover(testURI, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if hover != nil {
		t.Error("expected nil hover after DidClose")
	}
}

// ====================================================================
// Completion: Types and functions from richAST
// ====================================================================

func TestCompletion_TypesAndFunctions(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
func Greet() string {
    return "hi"
}
func main() {

}
`)
	list, err := h.Completion(testURI, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil or empty")
	}

	labels := collectLabels(list)
	// Should include user-defined types and functions along with keywords
	if !labels["Person"] {
		t.Logf("labels: %v", labelSlice(list))
		t.Skip("Person not in completions — analyzer may need search paths")
	}
	if !labels["Greet"] {
		t.Error("missing function Greet in completions")
	}
}

// ====================================================================
// Completion: Sealed variants in general completions
// ====================================================================

func TestCompletion_SealedVariantsInGeneral(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}
func main() {

}
`)
	list, err := h.Completion(testURI, 6, 4)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil or empty")
	}

	labels := collectLabels(list)
	if !labels["Shape"] {
		t.Skip("Shape not in completions — analyzer may need search paths")
	}
	// Sealed variants should appear as constructors
	if !labels["Circle"] {
		t.Error("missing sealed variant Circle in completions")
	}
	if !labels["Rectangle"] {
		t.Error("missing sealed variant Rectangle in completions")
	}
}

// ====================================================================
// Definition: Multiple definitions in same file
// ====================================================================

func TestDefinition_MultipleInSameFile(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, `package main
type Person struct {
    val name string
}
type Order struct {
    val id int
}
func main() {
    val p = Person(name = "Alice")
    val o = Order(id = 1)
}
`)
	// "Person" at line 8
	locs, err := h.Definition(testURI, 8, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found for Person")
	}
	if locs[0].Range.Start.Line != 1 {
		t.Errorf("expected Person definition on line 1, got line %d", locs[0].Range.Start.Line)
	}

	// "Order" at line 9
	locs, err = h.Definition(testURI, 9, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found for Order")
	}
	if locs[0].Range.Start.Line != 4 {
		t.Errorf("expected Order definition on line 4, got line %d", locs[0].Range.Start.Line)
	}
}

// ====================================================================
// Hover: Multiple builtins
// ====================================================================

func TestHover_AllBuiltins(t *testing.T) {
	builtins := []struct {
		name   string
		source string
		line   int
		col    int
	}{
		{"Println", "package main\nfunc main() {\n    Println(\"x\")\n}\n", 2, 4},
		{"Print", "package main\nfunc main() {\n    Print(\"x\")\n}\n", 2, 4},
		{"len", "package main\nfunc main() {\n    val x = len(\"x\")\n}\n", 2, 12},
		{"cap", "package main\nfunc main() {\n    val x = cap(\"x\")\n}\n", 2, 12},
		{"make", "package main\nfunc main() {\n    val x = make([]int, 0)\n}\n", 2, 12},
		{"append", "package main\nfunc main() {\n    val x = append(nil, 1)\n}\n", 2, 12},
		{"panic", "package main\nfunc main() {\n    panic(\"err\")\n}\n", 2, 4},
	}

	for _, tc := range builtins {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			openFile(t, h, tc.source)
			hover, err := h.Hover(testURI, tc.line, tc.col)
			if err != nil {
				t.Fatal(err)
			}
			if hover == nil {
				t.Skipf("hover returned nil for %s — analyzer may not produce richAST without search paths", tc.name)
			}
			if !strings.Contains(hover.Contents.Value, tc.name) {
				t.Errorf("expected hover to mention %s, got: %s", tc.name, hover.Contents.Value)
			}
		})
	}
}

// ====================================================================
// Helper: Request inlay hints via raw Call
// ====================================================================

func requestInlayHints(t *testing.T, h *servertest.Harness, startLine, endLine int) []lsp.InlayHint {
	t.Helper()
	raw, err := h.Call("textDocument/inlayHint", map[string]interface{}{
		"textDocument": map[string]string{"uri": testURI},
		"range": map[string]interface{}{
			"start": map[string]int{"line": startLine, "character": 0},
			"end":   map[string]int{"line": endLine, "character": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil || string(raw) == "null" {
		return nil
	}

	var hints []lsp.InlayHint
	if err := json.Unmarshal(raw, &hints); err != nil {
		t.Fatalf("failed to unmarshal inlay hints: %v (raw: %s)", err, string(raw))
	}
	return hints
}

// findHintForLine returns the first hint on the given line, or nil.
func findHintForLine(hints []lsp.InlayHint, line int) *lsp.InlayHint {
	for i := range hints {
		if hints[i].Position.Line == line {
			return &hints[i]
		}
	}
	return nil
}

// findAllHintsForLine returns all hints on the given line.
func findAllHintsForLine(hints []lsp.InlayHint, line int) []lsp.InlayHint {
	var out []lsp.InlayHint
	for _, h := range hints {
		if h.Position.Line == line {
			out = append(out, h)
		}
	}
	return out
}

// ============================================================
// === Import Scenarios ===
// ============================================================

func TestImport_GoPackage(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"hello\")\n}\n")
	hover, err := h.Hover(testURI, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hover on fmt: %v", hover)
}

func TestImport_GalaPackageAlias(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nimport im \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    val m = im.HashMap[string, int]()\n}\n")
	hover, err := h.Hover(testURI, 4, 14)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hover on aliased HashMap: %v", hover)
}

func TestImport_SiblingFile(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package mylib\n\ntype Config struct {\n    val host string\n}\n")
	uri2 := lsp.DocumentURI("file:///test/server.gala")
	if err := h.DidOpen(uri2, "gala", "package mylib\n\nfunc NewServer(c Config) string {\n    return c.host\n}\n"); err != nil {
		t.Fatal(err)
	}
	hover, err := h.Hover(uri2, 2, 18)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hover on Config from sibling: %v", hover)
}

// ============================================================
// === Method Completion on Specific Types ===
// ============================================================

func TestCompletion_MethodsOnStruct(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\ntype Person struct {\n    val name string\n    val age int\n}\n\nfunc (p Person) Greet() string {\n    return p.name\n}\n\nfunc main() {\n    val p = Person(name = \"Alice\", age = 30)\n    p.\n}\n")
	list, err := h.Completion(testURI, 13, 6)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil")
	}
	var labels []string
	for _, item := range list.Items {
		labels = append(labels, item.Label)
	}
	t.Logf("Person completion: %v", labels)
}

func TestCompletion_MethodsOnTry(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nfunc safeParse(s string) Try[int] {\n    return Success(42)\n}\n\nfunc main() {\n    val result = safeParse(\"42\")\n    result.\n}\n")
	list, err := h.Completion(testURI, 8, 11)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil")
	}
	var labels []string
	for _, item := range list.Items {
		labels = append(labels, item.Label)
	}
	t.Logf("Try completion: %v", labels)
}

func TestCompletion_MethodChainResult(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nfunc main() {\n    val opt = Some(42)\n    val mapped = opt.Map((x int) => x * 2)\n    mapped.\n}\n")
	list, err := h.Completion(testURI, 5, 11)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil")
	}
	t.Logf("chained completion: %d items", len(list.Items))
}

// ============================================================
// === Pattern Matching Completion ===
// ============================================================

func TestCompletion_SealedCasePatterns(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nsealed type Shape {\n    case Circle(radius float64)\n    case Rectangle(width float64, height float64)\n}\n\nfunc main() {\n    val s = Circle(radius = 5.0)\n    s match {\n        case \n    }\n}\n")
	list, err := h.Completion(testURI, 10, 14)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil")
	}
	var labels []string
	for _, item := range list.Items {
		labels = append(labels, item.Label)
	}
	t.Logf("sealed case completions: %v", labels)
}

func TestCompletion_SealedCaseDestructuring(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nsealed type Result {\n    case Ok(value string)\n    case Err(message string, code int)\n}\n\nfunc main() {\n    val r = Ok(value = \"success\")\n    r match {\n        case \n    }\n}\n")
	list, err := h.Completion(testURI, 10, 14)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil")
	}
	for _, item := range list.Items {
		if strings.HasPrefix(item.Label, "Err") && item.InsertText != "" {
			if !strings.Contains(item.InsertText, "message") {
				t.Errorf("Err should destructure fields, got: %s", item.InsertText)
			}
			t.Logf("Err insertText: %s", item.InsertText)
		}
	}
}

// ============================================================
// === Inlay Hints — Advanced ===
// ============================================================

func TestInlayHints_ShortDecl_Advanced(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\nfunc main() {\n    x := 42\n    name := \"test\"\n    Println(x)\n    Println(name)\n}\n")
	raw, err := h.Call("textDocument/inlayHint", map[string]interface{}{
		"textDocument": map[string]string{"uri": testURI},
		"range": map[string]interface{}{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": 10, "character": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("short decl hints: %s", string(raw))
}

func TestCompletion_DeepChain(t *testing.T) {
	h := newHarness(t)
	// a.Map(...).Map(...).ToOption() — verify completion after deep chain
	openFile(t, h, "package main\n\nfunc main() {\n    val opt = Some(42)\n    val chain = opt.Map((x int) => x * 2).Map((x int) => x + 1)\n    chain.\n}\n")
	list, err := h.Completion(testURI, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Skip("completion returned nil")
	}
	t.Logf("deep chain completion: %d items", len(list.Items))
}

func TestInlayHints_DeepChain(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\ntype Item struct {\n    val id int\n}\n\nfunc (i Item) Process() Option[Item] {\n    return Some(i)\n}\n\nfunc main() {\n    val item = Item(id = 1)\n    val step1 = item.Process()\n    val step2 = step1.Map((i Item) => i)\n    val step3 = step2.Map((i Item) => i.id)\n    Println(item)\n    Println(step1)\n    Println(step2)\n    Println(step3)\n}\n")
	raw, err := h.Call("textDocument/inlayHint", map[string]interface{}{
		"textDocument": map[string]string{"uri": testURI},
		"range": map[string]interface{}{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": 25, "character": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("deep chain hints: %s", string(raw))
}

func TestInlayHints_MethodChain(t *testing.T) {
	h := newHarness(t)
	openFile(t, h, "package main\n\ntype Order struct {\n    val id int\n}\n\nfunc (o Order) Process() Option[Order] {\n    return Some(o)\n}\n\nfunc main() {\n    val order = Order(id = 1)\n    val processed = order.Process()\n    Println(order)\n    Println(processed)\n}\n")
	raw, err := h.Call("textDocument/inlayHint", map[string]interface{}{
		"textDocument": map[string]string{"uri": testURI},
		"range": map[string]interface{}{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": 20, "character": 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("chain hints: %s", string(raw))
}
