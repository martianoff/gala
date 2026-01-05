# TODO

- [x] Proper handling pattern matching / unapply
- [ ] Basic immutable data structures
- [x] Type parameters in methods (e.g. Option.Map) in GALA
- [x] Generalize Option monad transpiler pattern that is used for Map, FlatMap, ForEach and others that transforms a: A with a.Map(b B): B to std.Map(a A, b B)[A, B]. The idea is to let GALA users to create similar functions inside GALA code base. Implement Either[A, B] monad in GALA as an example
- [x] Example tests via `bazel test`
- [ ] Support filters in pattern matching
- [x] Avoid gala style pattern matching
- [ ] Can we use Some/None as structs in GALA?
- [ ] Imported packages in GALA are analyzed with analyzers so they can support GALA features like generic methods
