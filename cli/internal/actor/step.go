package actor

// Step applies a reducer to a single (state, input) pair and returns the next
// state and effects.
//
// This is a testing utility for reducer-level unit tests. It does not execute
// effects.
func Step[S any](state S, input Input, reducer ReducerFunc[S]) (S, []Effect) {
	return reducer(state, input)
}

