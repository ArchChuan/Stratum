package constants

const (
	// MaxPromptContentBytes caps the size of a single prompt template.
	MaxPromptContentBytes = 64 * 1024

	// MaxBindingsPerScope limits how many prompt bindings a single
	// tenant or agent scope can create.
	MaxBindingsPerScope = 50

	// MaxPromptVersionsPerKey caps total versions (draft + published +
	// archived) for a single prompt key.
	MaxPromptVersionsPerKey = 200
)
