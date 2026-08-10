# First experience with Go

The learner confirmed that this project is their first exposure to Go and that a lesson beginning with handlers, interfaces, and proxy terminology felt jarring. Future sessions must establish basic Go syntax and file organization before introducing HTTP abstractions; familiarity should be inferred only from completed exercises.

## Evidence

In the first retrieval exercise, the learner correctly identified `func main()` as the executable entry point and explained `proxy.New` as calling `New` inside the `proxy` package. They initially confused the `internal/` directory with the module declaration in `go.mod`.

## Implications

The learner can proceed to values, variables, and calls, while module/package/file organization needs one brief reinforcement before deeper project navigation.

## Second checkpoint

The learner correctly explained that flags are defined before they are parsed. They partially conflated `:=` (create a new variable and assign its initial value) with `=` (assign a new value to an existing variable), and initially interpreted the parentheses in `flag.Parse()` as the command-line arguments. Reinforce function-call syntax and the distinction between a call's explicit arguments and `flag.Parse` reading process arguments before introducing pointers.

## Evidence of correction

When asked to evaluate `name := "Go"` followed by `name = "HTTP"`, the learner correctly stated that the first line creates and assigns the variable and the second changes its value.

## Implications

The distinction between declaration/initial assignment and later assignment is now a usable foundation for explaining pointer dereferencing in the flag setup.
