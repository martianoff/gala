# TODO

- [x] Proper handling pattern matching / unapply
- [ ] Basic immutable data structures
- [x] Type parameters in methods (e.g. Option,Map) in GALA
- [x] Generalize Option monad transpiler pattern that is used for Map, FlatMap, ForEach and others that transforms a: A with a.Map(b B): B to std.Map(a A, b B)[A, B]. The idea is to let GALA users to create similar functions inside GALA code base. Implement Either[A, B] monad in GALA as an example
- [x] Example tests via `bazel test`
- [x] Support filters in pattern matching
- [x] Avoid gala style pattern matching
- [x] Support Some/None as structs in GALA
- [x] Pattern matching for types
- [ ] Remove special casing for Tuples
- [ ] Extend Tuples class to Tuple3, Tuple4 ... Tuple10. Make (A, B) a synonym of Tuple(A,B), (A, B, C) a synonym of Tuple(A,B,C) and so on. Synonyms should work for construction and pattern matching
- [x] Imported packages in GALA are analyzed with analyzers so they can support GALA features like generic methods
- [ ] Dependency management
- [x] Test framework
- [x] JetBrains language plugin
