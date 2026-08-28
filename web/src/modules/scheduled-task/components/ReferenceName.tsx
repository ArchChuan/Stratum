import { Tooltip } from 'antd';

/**
 * 引用实体名称单元格：默认展示 name，原始 id 通过 hover 悬浮提示展示。
 * name 缺失（引用实体已删除或尚未解析）时回退展示原始 id。
 */
export function ReferenceName({ name, id }: { name?: string; id: string }) {
  if (!name) {
    return <span>{id}</span>;
  }
  return <Tooltip title={id}>{name}</Tooltip>;
}
