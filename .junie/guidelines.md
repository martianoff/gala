# Golang Guidelines

## 1. Organize Project Structure

* Follow a domain-driven or feature-based structure rather than organizing by technical layers
* Keep related functionality together to improve code discoverability
* Use a consistent naming convention for packages and files
* Keep implementations isolated from interfaces to minimize dependencies
* Make code more readable and maintainable

Maintain this folder structure:
internal/parser/grammar - gala grammar in ANTLR4 format
internal/transpiler/generator - generate GO code from GALA AST tree
internal/transpiler/transformer - transform GALA AST tree to GO AST tree
std - GALA standard library, contains common classes and functions that are going to be imported into GO code as required by transpiler, for example `Immutable` class

## 2. Dependency Injection with Explicit Construction

* Create service structs with explicit dependencies passed via constructors
* Avoid global variables or singletons
* Use interfaces to define dependencies for better testability

## 3. Error Handling

* Define custom error types for different error categories using the errors package

## 4. Effective Testing

* Write unit tests for business logic
* Use Go's testing package and testify for assertions
* Prefer table-driven tests
* Use multi-line input when new lines are required
_* Implement integration tests for critical paths_

## 5. Configuration Management

* Use environment variables for configuration
* Implement secure handling of secrets
* Provide sensible defaults

## 6. Context Propagation

* Use context for request scoped values and cancellation
* Propagate context through all layers of the application
* Set appropriate timeouts

## 7. Graceful Shutdown

* Implement graceful shutdown to handle in-flight requests
* Close resources properly when shutting down
* Use appropriate timeouts for shutdown

## 8. Whether Junie should run tests to check the correctness of the proposed solution
Yes, it should include unit tests to verify the functionality end-to-end. Please follow a pattern of existing tests.

## 9. How does Junie run tests?
Use bazel to run tests `bazel test //...`

## 10. Whether Junie should build the project before submitting the result
Yes, the project should be buildable with bazel. Code under "examples" should be executable with bazel and shouldn't return compiler errors.

## 11. Code-style-related instructions
Follow golang best practices
For each implementation of interface add compiler safe validator like `var _ Interface = (*Implementation)(nil)`

## 12. Whether Junie generate BUILD files?
Yes, use `bazel run //:gazelle` to create BUILD files

## 13. Update go dependencies
If needed, use the following commands to update BUILD files and configuration:
```shell
go mod tidy
bazel run //:gazelle
bazel run //:gazelle-update-repos
bazel run //:gazelle
bazel mod tidy
```

## 14. Strict rules
- Do not modify code generated files internal/parser/grammar/*.go

## 15. Documentation
- When you make changes to the grammar or add new features, update the docs/GALA.MD file with corresponding changes.
