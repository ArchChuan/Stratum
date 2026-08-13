package port

// SkillRefClassifier 判定 workflow skill 节点引用是否指向系统内置 skill。
// workflow domain 不 import skill domain(结构约束),由组合根 wiring 注入
// `strings.HasPrefix(id, "builtin:")` 实现;nil(未接线)时 DefinitionService
// 对 skill 节点 fail closed,禁止未知判定放行。
type SkillRefClassifier interface {
	IsBuiltinSkill(skillID string) bool
}
