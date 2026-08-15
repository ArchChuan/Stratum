import { z } from 'zod';

// 统一参数注册表的 wire 形状：定义在代码（后端 registry），值在
// platform_settings / agents.parameters。前端只消费 schema 渲染控件 + 读写平台值。

export const visualHintSchema = z
  .object({
    control: z.enum(['slider', 'select', 'toggle', 'textarea', 'number', 'model']),
    min: z.number().optional(),
    max: z.number().optional(),
    step: z.number().optional(),
    options: z.array(z.unknown()).optional(),
    unit: z.string().optional(),
  })
  .passthrough();
export type VisualHint = z.infer<typeof visualHintSchema>;

export const parameterDefinitionSchema = z
  .object({
    key: z.string(),
    scope: z.enum(['platform', 'resource']),
    category: z.string().optional().default(''),
    display_name: z.string().optional().default(''),
    description: z.string().optional().default(''),
    value_type: z.enum(['int', 'float', 'bool', 'string']),
    default: z.unknown().optional(),
    visual_hint: visualHintSchema,
    optimizable: z.boolean().optional().default(false),
    sensitive: z.boolean().optional().default(false),
  })
  .passthrough();
export type ParameterDefinition = z.infer<typeof parameterDefinitionSchema>;

// PlatformValues:key → 当前生效的平台层值(0=unset 由后端按定义裁剪)。
export type PlatformValues = Record<string, unknown>;

export interface PlatformSettingsFormValues {
  [key: string]: number | string | boolean | undefined;
}
