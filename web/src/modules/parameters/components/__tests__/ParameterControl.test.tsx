import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ParameterDefinition } from '../../model/parameters';
import { ParameterControl } from '../ParameterControl';

vi.mock('@/modules/llm', () => ({
  llmApi: {
    listModels: vi.fn().mockResolvedValue([]),
    listProviders: vi.fn().mockResolvedValue([]),
  },
}));

const embeddingDef = (): ParameterDefinition => ({
  key: 'memory.embedding_model',
  scope: 'platform',
  category: '记忆',
  display_name: '记忆嵌入模型',
  value_type: 'string',
  default: '',
  description: '',
  optimizable: false,
  sensitive: false,
  visual_hint: { control: 'embedding_model' },
});

describe('ParameterControl', () => {
  it('renders an embedding model combobox for embedding_model hint', () => {
    render(<ParameterControl def={embeddingDef()} />);
    expect(screen.getByRole('combobox')).toBeTruthy();
  });

  it('renders a model directory combobox for model hint (extraction/reflection models)', () => {
    render(
      <ParameterControl
        def={{
          key: 'memory.extraction_model',
          scope: 'platform',
          category: '记忆',
          display_name: '提取模型',
          value_type: 'string',
          default: '',
          description: '',
          optimizable: false,
          sensitive: false,
          visual_hint: { control: 'model' },
        }}
      />,
    );
    expect(screen.getByRole('combobox')).toBeTruthy();
  });
});
