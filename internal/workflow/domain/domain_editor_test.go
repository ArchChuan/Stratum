package domain

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefinitionCarriesEditorMetadata(t *testing.T) {
	def, err := NewDefinition("d1", "Research", "desc", Spec{}, defaultInputSchema())
	require.NoError(t, err)

	// created_by 创建时为空（Create 服务层写入）；editors 列表默认空切片。
	require.Equal(t, "", def.CreatedBy)
	require.Nil(t, def.Editors)

	def.CreatedBy = "u-1"
	def.Editors = []string{"u-1", "u-2"}
	require.Equal(t, "u-1", def.CreatedBy)
	require.Equal(t, []string{"u-1", "u-2"}, def.Editors)
}

func TestErrEditorNotEligibleExists(t *testing.T) {
	// 非白名单成员写白名单时，store 用该哨兵 wrap，errors.Is 可判定。
	require.True(t, errors.Is(fmt.Errorf("store: %w", ErrEditorNotEligible), ErrEditorNotEligible))
}
