package wiring

import (
	"github.com/byteBuilderX/stratum/internal/prompt/application"
	"github.com/byteBuilderX/stratum/internal/prompt/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Prompt groups the prompt registry services for wiring consumers.
type Prompt struct {
	Registry *application.RegistryService
	AB       *application.ABService
}

func buildPrompt(db *pgxpool.Pool) *Prompt {
	if db == nil {
		return nil
	}
	prompts := persistence.NewPgPromptRepo(db)
	bindings := persistence.NewPgBindingRepo(db)
	return &Prompt{
		Registry: application.NewRegistryService(prompts, bindings),
		AB:       application.NewABService(bindings, prompts),
	}
}
