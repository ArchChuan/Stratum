package application

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"go.uber.org/zap"
)

type CreateDefinitionCommand struct {
	Name        string
	Description string
	Spec        domain.Spec
	InputSchema domain.InputSchema
}

type UpdateDefinitionCommand struct {
	Name             string
	Description      string
	Spec             domain.Spec
	InputSchema      domain.InputSchema
	ExpectedRevision int64
}

type DefinitionService struct {
	definitions  port.DefinitionRepository
	versions     port.VersionRepository
	newID        func() string
	failureAudit auditport.FailureAuditRecorder
	bindings     port.SkillBindingResolver
	logger       *zap.Logger
}

func NewDefinitionService(definitions port.DefinitionRepository, versions port.VersionRepository, newID func() string) *DefinitionService {
	return &DefinitionService{definitions: definitions, versions: versions, newID: newID, logger: zap.NewNop()}
}

// SetFailureAuditRecorder 注入失败资源操作审计。未注入时跳过记录。
func (s *DefinitionService) SetFailureAuditRecorder(r auditport.FailureAuditRecorder) {
	s.failureAudit = r
}

// SetSkillBindingResolver 注入 agent 技能绑定解析器，用于校验 skill 节点的
// agent-skill 引用关系。未注入时跳过绑定校验（测试/降级）。
func (s *DefinitionService) SetSkillBindingResolver(r port.SkillBindingResolver) {
	s.bindings = r
}

// validateSkillBindings 校验 spec 中所有 skill 节点引用的 agent 确实启用了该技能。
// resolver 存在但查询失败则传播错误（fail-closed），不允许静默放行。
func (s *DefinitionService) validateSkillBindings(ctx context.Context, tenantID string, spec domain.Spec) error {
	if s.bindings == nil {
		return nil
	}
	cache := make(map[string][]string, len(spec.Nodes))
	for _, node := range spec.Nodes {
		if node.Type != domain.NodeTypeSkill || node.AgentID == "" || node.SkillID == "" {
			continue
		}
		allowed, ok := cache[node.AgentID]
		if !ok {
			var err error
			allowed, err = s.bindings.AgentAllowedSkills(ctx, tenantID, node.AgentID)
			if err != nil {
				return err
			}
			cache[node.AgentID] = allowed
		}
		if err := domain.ValidateSkillBinding(allowed, node.SkillID); err != nil {
			return err
		}
	}
	return nil
}

// SetLogger 注入日志器（默认 Nop，测试与生产均可覆盖）。
func (s *DefinitionService) SetLogger(l *zap.Logger) {
	if l != nil {
		s.logger = l
	}
}
func (s *DefinitionService) Create(ctx context.Context, tenantID string, cmd CreateDefinitionCommand, actorID string) (*domain.Definition, error) {
	definition, err := domain.NewDefinition(s.newID(), cmd.Name, cmd.Description, cmd.Spec, normalizeInputSchema(cmd.InputSchema))
	if err != nil {
		return nil, err
	}
	// 草稿保存也强制图完整性（含环检测）：用户拖拽新边时前端已阻止成环，
	// 这里作为 fail-closed 兜底，避免非法拓扑流入存储。允许空图（画一半先保存）。
	if err := domain.ValidateSpecGraph(definition.Spec); err != nil {
		return nil, err
	}
	if err := s.validateSkillBindings(ctx, tenantID, definition.Spec); err != nil {
		return nil, err
	}
	ev, err := newWorkflowChangeAudit(definition.ID, auditdomain.ChangeOpCreate, actorID, nil, workflowSafeProjection(definition))
	if err != nil {
		return nil, err
	}
	if err := s.definitions.CreateDefinition(ctx, tenantID, definition, ev); err != nil {
		s.recordFailure(ctx, definition.ID, "create", err)
		return nil, err
	}
	return definition, nil
}

func (s *DefinitionService) Update(ctx context.Context, tenantID, id string, cmd UpdateDefinitionCommand, actorID string) (*domain.Definition, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	before := workflowSafeProjection(definition)
	if err := definition.UpdateDraft(cmd.Name, cmd.Description, cmd.Spec, cmd.ExpectedRevision, normalizeInputSchema(cmd.InputSchema)); err != nil {
		return nil, err
	}
	// 与 Create 一致：草稿更新强制图完整性（含环检测），fail-closed。
	if err := domain.ValidateSpecGraph(definition.Spec); err != nil {
		return nil, err
	}
	if err := s.validateSkillBindings(ctx, tenantID, definition.Spec); err != nil {
		return nil, err
	}
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpUpdate, actorID, before, workflowSafeProjection(definition))
	if err != nil {
		return nil, err
	}
	if err := s.definitions.UpdateDefinition(ctx, tenantID, definition, cmd.ExpectedRevision, "", ev); err != nil {
		s.recordFailure(ctx, id, "update", err)
		return nil, err
	}
	return definition, nil
}

func (s *DefinitionService) Delete(ctx context.Context, tenantID, id string, actorID string) error {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return err
	}
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpDelete, actorID, workflowSafeProjection(definition), nil)
	if err != nil {
		return err
	}
	return s.definitions.DeleteDefinition(ctx, tenantID, id, ev)
}

func normalizeInputSchema(schema domain.InputSchema) domain.InputSchema {
	if schema.TaskLabel == "" && schema.TaskDescription == "" && len(schema.Fields) == 0 {
		return domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{}}
	}
	return schema
}

func (s *DefinitionService) Validate(ctx context.Context, tenantID, id string) error {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return err
	}
	return domain.ValidateSpec(definition.Spec)
}

func (s *DefinitionService) Get(ctx context.Context, tenantID, id string) (*domain.Definition, error) {
	return s.definitions.GetDefinition(ctx, tenantID, id)
}

func (s *DefinitionService) GetVersion(ctx context.Context, tenantID, id string) (*domain.Version, error) {
	return s.versions.GetVersion(ctx, tenantID, id)
}

func (s *DefinitionService) Publish(ctx context.Context, tenantID, id string, actorID string) (*domain.Version, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	// 发布前同样校验 skill 节点绑定关系：发布即对外生效，非法绑定必须 fail-closed。
	if err := s.validateSkillBindings(ctx, tenantID, definition.Spec); err != nil {
		return nil, err
	}
	projection := workflowSafeProjection(definition)
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpPublish, actorID, projection, projection)
	if err != nil {
		return nil, err
	}
	if publisher, ok := s.versions.(port.AtomicVersionPublisher); ok {
		version, err := publisher.CreateNextVersion(ctx, tenantID, definition, s.newID(), "", ev)
		if err != nil {
			s.recordFailure(ctx, id, "publish", err)
			return nil, err
		}
		return version, nil
	}
	number, err := s.versions.NextVersionNumber(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	version, err := definition.Publish(s.newID(), number)
	if err != nil {
		return nil, err
	}
	if err := s.versions.CreateVersion(ctx, tenantID, version, ev); err != nil {
		s.recordFailure(ctx, id, "publish", err)
		return nil, err
	}
	return version, nil
}

// Rollback 把生效指针指回历史已发布版本，不产生新版本。
// 目标版本必须存在且归属于同一工作流，否则 fail-closed 返回 ErrNotFound。
func (s *DefinitionService) Rollback(ctx context.Context, tenantID, id, versionID string, actorID string) (*domain.Definition, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	version, err := s.versions.GetVersion(ctx, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	if version.DefinitionID != definition.ID {
		return nil, domain.ErrNotFound
	}
	before := workflowSafeProjection(definition)
	after := workflowSafeProjection(definition)
	after["active_version_id"] = versionID
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpRollback, actorID, before, after)
	if err != nil {
		return nil, err
	}
	if err := s.versions.SetActiveVersion(ctx, tenantID, id, versionID, ev); err != nil {
		s.recordFailure(ctx, id, "rollback", err)
		return nil, err
	}
	updated, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// recordFailure 旁路记录一次失败的工作流创建/更新/发布（best-effort）。
// 记录失败仅 WARN，不改变主流程错误。
func (s *DefinitionService) recordFailure(ctx context.Context, id, op string, err error) {
	if s.failureAudit == nil {
		return
	}
	if recordErr := s.failureAudit.Record(ctx, auditport.ResourceFailure{
		ResourceKind: auditdomain.ResourceKindWorkflow,
		ResourceID:   id,
		Operation:    op,
		ErrorCode:    auditport.ClassifyFailure(err),
	}); recordErr != nil {
		s.logger.Warn("failed to record workflow failure audit",
			zap.String("definition_id", id),
			zap.String("op", op),
			zap.Error(recordErr))
	}
}
