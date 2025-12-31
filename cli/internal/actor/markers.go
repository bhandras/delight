package actor

// InputBase is a helper that can be embedded into input structs to satisfy the
// Input interface.
type InputBase struct{}

// isActorInput implements Input.
func (InputBase) isActorInput() {}

// EffectBase is a helper that can be embedded into effect structs to satisfy
// the Effect interface.
type EffectBase struct{}

// isActorEffect implements Effect.
func (EffectBase) isActorEffect() {}

