package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

// SkillActivationView is a context-neutral projection of a resolved skill
// revision, exposing exactly the instruction-bundle fields a consumer needs to
// activate the skill. It deliberately carries no agent-context types, so the
// skill context stays free of reverse dependencies; the composition root maps
// this view onto whatever shape a consumer's port requires.
type SkillActivationView struct {
	SkillID      string
	RevisionID   string
	Name         string
	Description  string
	Instructions string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

// defaultObjectSchema is the fallback activation schema: the simplified skill
// model carries no explicit input/output contract, so activations accept a free
// object payload by default.
func defaultObjectSchema() map[string]any {
	return map[string]any{"type": "object"}
}

// ResolveActivation returns the activatable projection of a skill revision.
//
// An empty revisionID resolves the skill's active revision; a non-empty one
// resolves that exact revision. Only published/candidate revisions are
// activatable — draft, deprecated, or missing revisions yield found=false with
// no error, matching the raw-SQL semantics this method replaces
// (status IN ('published','candidate')). The revision's name/description take
// precedence, falling back to the skill product's own values when blank.
func (s *VersionService) ResolveActivation(
	ctx context.Context, skillID, revisionID string,
) (SkillActivationView, bool, error) {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return SkillActivationView{}, false, err
	}
	if !ok {
		return SkillActivationView{}, false, nil
	}

	var revision domain.SkillRevision
	if revisionID == "" {
		revision, ok, err = s.repo.GetActiveRevision(ctx, skillID)
	} else {
		revision, ok, err = s.repo.GetRevision(ctx, skillID, revisionID)
	}
	if err != nil {
		return SkillActivationView{}, false, err
	}
	if !ok {
		return SkillActivationView{}, false, nil
	}
	if revision.Status != domain.VersionStatusPublished &&
		revision.Status != domain.VersionStatusCandidate {
		return SkillActivationView{}, false, nil
	}

	name := revision.Name
	if name == "" {
		name = skill.Name
	}
	description := revision.Description
	if description == "" {
		description = skill.Description
	}
	return SkillActivationView{
		SkillID:      skill.ID,
		RevisionID:   revision.ID,
		Name:         name,
		Description:  description,
		Instructions: revision.Instructions,
		InputSchema:  defaultObjectSchema(),
		OutputSchema: defaultObjectSchema(),
	}, true, nil
}
